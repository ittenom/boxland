// Boxland — player-facing asset catalog endpoint.
//
// The web renderer needs three things to draw a sprite frame:
//
//   1. The sheet's CDN URL (per-asset, per-variant).
//   2. The sheet's grid metadata (cell size, columns, rows) so it can
//      compute a source rect from a frame index.
//   3. The asset's animation rows (`walk_north` etc.) so the
//      animation system on the client can map a server-supplied
//      anim_id back to its (frame_from, frame_to, fps, direction).
//
// One round trip per game session, batched: the boot path collects
// every asset id the entity types reference and asks for them all at
// once. AssetIDs the renderer encounters later (a new entity entered
// AOI carrying an unfamiliar asset_id) trigger a single coalesced
// fetch — the client lib batches concurrent demands so a snapshot of
// 50 sprite types doesn't fan out to 50 requests.
//
// Auth: any authenticated player can hit it. Asset names + URLs are
// not sensitive (they're visible on every public map's tile palette
// already), and gating per-map would force the renderer to round-trip
// every time it crossed a chunk boundary — wasteful. The catalog rows
// themselves are immutable per-publish, so the response is fully
// cacheable.

package playerweb

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"boxland/server/internal/assets"
)

// catalogResponse is the JSON body returned by GET /play/asset-catalog.
type catalogResponse struct {
	Assets []catalogAsset `json:"assets"`
}

// catalogAsset is one asset's renderable description.
type catalogAsset struct {
	ID          int64                `json:"id"`
	Name        string               `json:"name"`
	Kind        string               `json:"kind"`
	URL         string               `json:"url"`            // CDN-fronted public URL
	GridW       int                  `json:"grid_w"`         // frame width in texture pixels
	GridH       int                  `json:"grid_h"`         // frame height in texture pixels
	Cols        int                  `json:"cols"`           // 0 for non-uniform sheets (Aseprite)
	Rows        int                  `json:"rows"`
	FrameCount  int                  `json:"frame_count"`
	Animations  []catalogAnimation   `json:"animations"`
}

// catalogAnimation is one persisted animation row, in the wire shape
// the client renderer consumes directly.
type catalogAnimation struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	FrameFrom int32  `json:"frame_from"`
	FrameTo   int32  `json:"frame_to"`
	FPS       int32  `json:"fps"`
	Direction string `json:"direction"` // "forward" | "reverse" | "pingpong"
}

// MaxCatalogIDs caps a single request. Generous enough that any real
// game's full asset set fits, tight enough that a malicious enumerator
// can't pull the entire DB in one shot.
const MaxCatalogIDs = 256

// getAssetCatalog handles GET /play/asset-catalog?ids=1,2,3.
//
// Returns one entry per requested id. Missing ids are silently
// dropped (the renderer treats absence the same as a missing frame —
// it skips the draw rather than rendering a placeholder).
func getAssetCatalog(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Assets == nil || d.ObjectStore == nil {
			http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		ids, err := parseCatalogIDs(r.URL.Query().Get("ids"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(ids) == 0 {
			writeCatalog(w, catalogResponse{Assets: []catalogAsset{}})
			return
		}

		// One query per surface (assets + animations) — both batched
		// across every requested id. Two round trips total regardless
		// of how many ids the client asked for.
		rows, err := d.Assets.ListByIDs(r.Context(), ids)
		if err != nil {
			http.Error(w, "list assets: "+err.Error(), http.StatusInternalServerError)
			return
		}
		anims, err := d.Assets.ListAnimationsByAssetIDs(r.Context(), ids)
		if err != nil {
			http.Error(w, "list animations: "+err.Error(), http.StatusInternalServerError)
			return
		}

		out := catalogResponse{Assets: make([]catalogAsset, 0, len(rows))}
		for _, a := range rows {
			// Sheet metadata is folded into assets.metadata_json by
			// the upload pipeline. Audio assets carry a different
			// shape; ignore unmarshal failures (renderer treats a
			// zero grid as "ask for frame 0 only" which still draws).
			var sheet assets.SheetMetadata
			if len(a.MetadataJSON) > 0 {
				_ = json.Unmarshal(a.MetadataJSON, &sheet)
			}
			entry := catalogAsset{
				ID:         a.ID,
				Name:       a.Name,
				Kind:       string(a.Kind),
				URL:        d.ObjectStore.PublicURL(a.ContentAddressedPath),
				GridW:      sheet.GridW,
				GridH:      sheet.GridH,
				Cols:       sheet.Cols,
				Rows:       sheet.Rows,
				FrameCount: sheet.FrameCount,
			}
			if rs, ok := anims[a.ID]; ok {
				entry.Animations = make([]catalogAnimation, 0, len(rs))
				for _, r := range rs {
					entry.Animations = append(entry.Animations, catalogAnimation{
						ID:        r.ID,
						Name:      r.Name,
						FrameFrom: r.FrameFrom,
						FrameTo:   r.FrameTo,
						FPS:       r.FPS,
						Direction: string(r.Direction),
					})
				}
			} else {
				entry.Animations = []catalogAnimation{}
			}
			out.Assets = append(out.Assets, entry)
		}

		writeCatalog(w, out)
	}
}

// parseCatalogIDs splits the comma-separated id list, drops blanks,
// dedups, and enforces MaxCatalogIDs. Returns a clean slice of int64.
func parseCatalogIDs(raw string) ([]int64, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > MaxCatalogIDs {
		return nil, errors.New("ids: too many; max " + strconv.Itoa(MaxCatalogIDs))
	}
	seen := make(map[int64]struct{}, len(parts))
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("ids: bad id " + strconv.Quote(p))
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func writeCatalog(w http.ResponseWriter, body catalogResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Catalog rows mutate at publish-time only; a 60-second cache with
	// stale-while-revalidate keeps the boot path snappy after one warm
	// fetch without making publish updates feel laggy.
	w.Header().Set("Cache-Control", "public, max-age=60, stale-while-revalidate=600")
	_ = json.NewEncoder(w).Encode(body)
}
