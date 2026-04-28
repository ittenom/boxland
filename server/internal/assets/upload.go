package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"boxland/server/internal/persistence"
)

// jsonMarshal is a thin wrapper for marshalling metadata into the JSONB
// column. Kept local so callers don't need to import encoding/json.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// MaxUploadBytes caps a single uploaded file. PNG sprite sheets and small
// audio clips both fit comfortably; raise here if the bake job ever
// produces variants larger than the source.
const MaxUploadBytes = 16 * 1024 * 1024 // 16 MiB

// UploadResult is the outcome of one HTTP upload.
//
// Tilemap-eligible PNGs (multi-cell 32×32 grids) are uploaded as
// `KindSpriteAnimated` assets; the handler in internal/designer is
// responsible for then creating a `tilemaps` row on top via the
// internal/tilemaps service. TilemapEligible flags this case;
// TilemapCells + TilemapMeta carry the pre-flight slice data so the
// handler can hand them straight to tilemaps.Service.Create without
// re-decoding the PNG.
type UploadResult struct {
	Asset      *Asset
	Reused     bool   // true if an asset with the same content_addressed_path already existed
	OriginalFn string // original filename from the multipart form

	// TilemapEligible is true when the uploaded PNG matches the
	// "multi-cell 32×32 grid" heuristic. The handler should create a
	// tilemap on top of the asset and fan out per-cell tile entities.
	TilemapEligible bool
	TilemapCells    []TileCell
	TilemapMeta     TileSheetMetadata
	// PngBody is the raw uploaded PNG bytes — surfaced only when
	// TilemapEligible is true so the handler can pass them straight
	// to tilemaps.Service.Create (which computes per-cell pixel +
	// edge-strip hashes). Empty for every other path.
	PngBody []byte

	SpriteImport *ImportResult
}

// SupportedContentTypes maps the sniffed MIME type to the kind it produces.
// We use sniffing (not the client-supplied type) because the client may lie
// or guess wrong. Anything outside this set is rejected with 415.
//
// PNGs default to KindSprite; the upload pipeline upgrades to
// KindSpriteAnimated when it detects a multi-frame strip (sprite-sheet
// importer found multiple frames) or a multi-cell tilemap grid.
var SupportedContentTypes = map[string]struct {
	Kind   Kind
	Format string
}{
	"image/png":   {KindSprite, "png"},
	"audio/wav":   {KindAudio, "wav"},
	"audio/x-wav": {KindAudio, "wav"},
	"audio/wave":  {KindAudio, "wav"},
	"audio/ogg":   {KindAudio, "ogg"},
	"audio/mpeg":  {KindAudio, "mp3"},
}

const (
	// KindOverrideSpriteSheet and KindOverrideAnimatedSprite are
	// upload-form values. Both produce a KindSpriteAnimated asset
	// (since both describe a multi-frame strip); the importer persists
	// frame metadata + animation rows alongside the asset row.
	KindOverrideSpriteSheet    = "sprite_sheet"
	KindOverrideAnimatedSprite = "animated_sprite"
	// KindOverrideTilemap is the upload-form value used by the
	// "Upload tilemap" entry point. The result is still a
	// KindSpriteAnimated asset; the handler then creates a tilemap row
	// on top.
	KindOverrideTilemap = "tilemap"
)

// NormalizeUploadKind maps UI-only upload modes onto persisted asset kinds.
// Sprite sheets, animated sprites, and tilemaps all persist as
// `KindSpriteAnimated` — the structured "this is a tilemap" object is a
// separate row in the tilemaps table, not a kind discriminator on assets.
func NormalizeUploadKind(raw string) Kind {
	switch strings.TrimSpace(raw) {
	case "", "auto":
		return ""
	case KindOverrideSpriteSheet, KindOverrideAnimatedSprite, KindOverrideTilemap:
		return KindSpriteAnimated
	default:
		return Kind(raw)
	}
}

// Errors returned by Upload. Stable for HTTP handler mapping.
var (
	ErrNoFile                 = errors.New("upload: no file in request")
	ErrTooLarge               = errors.New("upload: file exceeds size limit")
	ErrUnsupportedContentType = errors.New("upload: unsupported content type")
)

// Upload reads the first file from a multipart request, validates it, pushes
// it to object storage at a content-addressed path, and inserts an asset row.
//
// kindOverride lets the handler force a kind (e.g. an image arriving on the
// "tile sheets" tab is a tile, not a sprite). Pass "" to use the default
// from SupportedContentTypes.
//
// The caller (HTTP handler in internal/designer) supplies the designer id
// from the session; this method makes no auth decisions of its own.
//
// Single-file path retained for the pixel-editor export (postAssetReplace)
// which always sends one PNG. Multi-file uploads from the asset modal go
// through UploadMany.
func (s *Service) Upload(
	ctx context.Context,
	r *http.Request,
	store *persistence.ObjectStore,
	designerID int64,
	kindOverride Kind,
) (*UploadResult, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxUploadBytes+1)
	if err := r.ParseMultipartForm(MaxUploadBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, maxBytesErr.Limit)
		}
		return nil, fmt.Errorf("parse multipart: %w", err)
	}
	_, header, err := r.FormFile("file")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, ErrNoFile
		}
		return nil, fmt.Errorf("read file: %w", err)
	}
	return s.uploadFromHeader(ctx, header, nil, store, designerID, kindOverride)
}

// UploadMany reads every file under the multipart "files" field and runs
// each through the same content-addressed pipeline. Returns one
// UploadResult per file in the order the browser sent them. A failed
// file produces an entry with a non-nil Err and does not stop the rest;
// callers (HTTP handler) summarize the totals to the user.
//
// The total request size is capped at MaxUploadBytes * MaxFilesPerUpload
// to keep memory predictable. Per-file size still honors MaxUploadBytes
// individually.
func (s *Service) UploadMany(
	ctx context.Context,
	r *http.Request,
	store *persistence.ObjectStore,
	designerID int64,
	kindOverride Kind,
) ([]MultiUploadResult, error) {
	totalCap := int64(MaxUploadBytes) * int64(MaxFilesPerUpload)
	r.Body = http.MaxBytesReader(nil, r.Body, totalCap+1)
	if err := r.ParseMultipartForm(totalCap); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, fmt.Errorf("%w: %d bytes total", ErrTooLarge, maxBytesErr.Limit)
		}
		return nil, fmt.Errorf("parse multipart: %w", err)
	}
	if r.MultipartForm == nil {
		return nil, ErrNoFile
	}
	headers := r.MultipartForm.File["files"]
	// Back-compat: a single file under "file" still works through this path.
	if len(headers) == 0 {
		headers = r.MultipartForm.File["file"]
	}
	if len(headers) == 0 {
		return nil, ErrNoFile
	}
	if len(headers) > MaxFilesPerUpload {
		return nil, fmt.Errorf("%w: %d files (max %d)", ErrTooLarge, len(headers), MaxFilesPerUpload)
	}

	out := make([]MultiUploadResult, 0, len(headers))
	for _, h := range headers {
		// Pass the full sibling list so a PNG can pick up its
		// matching .json sidecar without a parser-override.
		res, err := s.uploadFromHeader(ctx, h, headers, store, designerID, kindOverride)
		entry := MultiUploadResult{OriginalFn: h.Filename}
		if err != nil {
			entry.Err = err
			out = append(out, entry)
			continue
		}
		entry.Asset = res.Asset
		entry.Reused = res.Reused
		entry.TilemapEligible = res.TilemapEligible
		entry.TilemapCells = res.TilemapCells
		entry.TilemapMeta = res.TilemapMeta
		entry.PngBody = res.PngBody
		entry.SpriteImport = res.SpriteImport
		out = append(out, entry)
	}
	return out, nil
}

// uploadFromHeader is the shared per-file pipeline used by both Upload
// and UploadMany. Reads, dedups, stores, inserts.
//
// `siblings` is the full multipart-form file slice for multi-file
// uploads (so a PNG can pick up its sidecar JSON automatically). Pass
// nil for single-file uploads.
func (s *Service) uploadFromHeader(
	ctx context.Context,
	header *multipart.FileHeader,
	siblings []*multipart.FileHeader,
	store *persistence.ObjectStore,
	designerID int64,
	kindOverride Kind,
) (*UploadResult, error) {
	if header == nil {
		return nil, ErrNoFile
	}
	if header.Size > MaxUploadBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, header.Size)
	}
	file, err := header.Open()
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, MaxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > MaxUploadBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, MaxUploadBytes)
	}
	if len(body) == 0 {
		return nil, ErrNoFile
	}

	sniffed := http.DetectContentType(body)
	supported, ok := SupportedContentTypes[normalizeContentType(sniffed)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedContentType, sniffed)
	}

	// kindOverride arrives as the RAW form value
	// ("" / "sprite" / "sprite_sheet" / "animated_sprite" / "tilemap"
	// / "audio" / "ui_panel" — see the upload modal). We capture the
	// raw signal here BEFORE normalizing because two raw values
	// ("tilemap" and "animated_sprite") collapse to the same persisted
	// Kind (sprite_animated), but only the former should auto-create
	// a tilemap. The handler is allowed to pass an already-normalized
	// Kind too — programmatic callers do — so anything that matches a
	// canonical Kind string also flows through.
	rawOverride := string(kindOverride)
	kind := supported.Kind
	if kindOverride != "" {
		kind = NormalizeUploadKind(rawOverride)
	}
	// Auto-detect tilemap-eligible PNGs:
	//   1) Explicit "tilemap" override — always eligible (single-cell
	//      sheets included; designer asked for it).
	//   2) No kind hint AND a multi-cell 32×32 grid — auto-promote.
	// An explicit "sprite_sheet" / "animated_sprite" override stays a
	// frame strip, not a tilemap, even when the grid slices cleanly.
	tilemapEligible := false
	switch {
	case rawOverride == KindOverrideTilemap:
		tilemapEligible = true
		kind = KindSpriteAnimated
	case rawOverride == "" && supported.Kind == KindSprite && isAutoTileSheet(body):
		tilemapEligible = true
		kind = KindSpriteAnimated
	}
	originalName := header.Filename
	displayName := defaultDisplayName(originalName)

	// Tilemap pre-flight: slice into 32×32 cells before we touch
	// object storage so a malformed sheet (odd dimensions, fully
	// transparent) fails fast with a clean error instead of leaving
	// an unusable asset row behind. The cells + metadata feed into
	// the tilemap creation flow in the handler.
	var tilemapCells []TileCell
	var tilemapMeta TileSheetMetadata
	if tilemapEligible {
		cells, md, err := SliceTileSheet(body)
		if err != nil {
			// Bad PNG; demote to a regular sprite so the upload still
			// succeeds (designer can re-upload as a sprite_animated
			// strip if they want).
			tilemapEligible = false
		} else {
			tilemapCells = cells
			tilemapMeta = md
		}
	}

	// Sprite-sheet pre-flight: auto-detect the importer (Aseprite if
	// a sidecar is alongside, otherwise uniform-grid auto-slice with
	// canonical 32×32 cells), and synthesize walk_*/idle animations
	// for the common 4-row top-down layout. Importer failures here
	// are fatal (same policy as tile pre-flight): a bad PNG should
	// never produce an asset row with no animations.
	//
	// Sidecar source: an upload header named "<base>.json" alongside
	// "<base>.png" — we check the multipart form for it. The form is
	// parsed once at the top of (Upload|UploadMany), so accessing
	// r.MultipartForm here is safe.
	var spriteImport *ImportResult
	if (kind == KindSprite || kind == KindSpriteAnimated) && s.Importers != nil {
		sidecar := readSidecar(header, siblings)
		ir, err := DefaultSpriteImport(ctx, s.Importers, header.Filename, body, sidecar, AutoSliceConfig{})
		if err != nil {
			return nil, err
		}
		spriteImport = ir
		// A sprite import that found multiple frames upgrades the
		// asset to KindSpriteAnimated. The animations table carries
		// the frame ranges; the asset row's kind flags the renderer.
		if kind == KindSprite && spriteImport != nil && spriteImport.SheetMetadata.FrameCount > 1 {
			kind = KindSpriteAnimated
		}
	}

	key := persistence.ContentAddressedKey("assets", body)

	if existing, err := s.FindByContentPath(ctx, kind, key); err == nil {
		// Re-uploads of an existing sheet still surface the slice
		// info so the handler can backfill any cells that lacked a
		// tile entity (e.g. designer ran the upload before tilemap
		// auto-slice shipped, or deleted some cells and wants them
		// back).
		//
		// Symmetrically for sprites: if the existing row predates the
		// upload-time animation persistence (or had its rows wiped by
		// a manual cleanup), backfill them so the runtime catalog
		// always sees a populated set on a re-upload. Idempotent —
		// ReplaceAnimations with the same input is a no-op net of
		// the DELETE+INSERT pair.
		if spriteImport != nil && len(spriteImport.Animations) > 0 {
			rows, lerr := s.ListAnimations(ctx, existing.ID)
			if lerr == nil && len(rows) == 0 {
				if rerr := s.ReplaceAnimations(ctx, existing.ID, spriteImport.Animations); rerr != nil {
					slog.Warn("upload: backfill animations on reuse",
						"asset_id", existing.ID, "err", rerr)
				}
			}
		}
		return &UploadResult{
			Asset: existing, Reused: true, OriginalFn: originalName,
			TilemapEligible: tilemapEligible,
			TilemapCells:    tilemapCells, TilemapMeta: tilemapMeta,
			PngBody:      pngBodyForResult(tilemapEligible, body),
			SpriteImport: spriteImport,
		}, nil
	} else if !errors.Is(err, ErrAssetNotFound) {
		return nil, fmt.Errorf("dedup lookup: %w", err)
	}

	if err := store.Put(ctx, key, sniffed, bytes.NewReader(body), int64(len(body))); err != nil {
		return nil, fmt.Errorf("upload to storage: %w", err)
	}

	metadata := []byte("{}")
	switch {
	case kind == KindAudio:
		md, err := InspectAudio(body)
		if err == nil {
			if b, jerr := jsonMarshal(md); jerr == nil {
				metadata = b
			}
		}
	case tilemapEligible:
		// Tilemap-eligible uploads carry the slice metadata on the
		// asset row so the tilemap viewer can render the grid even
		// before the tilemap row is wired up by the handler.
		if b, jerr := MarshalTileSheetMetadata(tilemapMeta); jerr == nil {
			metadata = b
		}
	case (kind == KindSprite || kind == KindSpriteAnimated) && spriteImport != nil:
		// Persist the sheet metadata (grid, frame count, source) so
		// the runtime catalog can rebuild source rects without
		// re-parsing the PNG. See web/src/game/catalog.ts.
		if b, jerr := jsonMarshal(spriteImport.SheetMetadata); jerr == nil {
			metadata = b
		}
	}

	// Compute dominant color for image kinds so the "sort by color"
	// view in the library doesn't need a backfill pass for fresh
	// uploads. Failure is non-fatal; the lazy backfill in
	// Service.EnsureDominantColors picks up any holes later.
	var dominantColor *int64
	if kind == KindSprite || kind == KindSpriteAnimated || kind == KindUIPanel {
		if packed, ok := ComputeDominantColor(body); ok {
			v := int64(packed)
			dominantColor = &v
		}
	}

	asset, err := s.Create(ctx, CreateInput{
		Kind:                 kind,
		Name:                 displayName,
		ContentAddressedPath: key,
		OriginalFormat:       supported.Format,
		MetadataJSON:         metadata,
		Tags:                 []string{},
		DominantColor:        dominantColor,
		CreatedBy:            designerID,
	})
	if err != nil {
		if errors.Is(err, ErrNameInUse) {
			altName := uniquify(displayName, key)
			asset, err = s.Create(ctx, CreateInput{
				Kind:                 kind,
				Name:                 altName,
				ContentAddressedPath: key,
				OriginalFormat:       supported.Format,
				MetadataJSON:         metadata,
				Tags:                 []string{},
				DominantColor:        dominantColor,
				CreatedBy:            designerID,
			})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	// Persist parsed animations once the asset row exists. Failures
	// here downgrade to a structured warn-log: the asset is already
	// usable (renderer falls back to frame 0), and a subsequent
	// re-import or designer-driven edit can backfill rows. We do
	// NOT roll back the asset row — half-imported sheets are still
	// preferable to a hard upload failure.
	if spriteImport != nil && len(spriteImport.Animations) > 0 {
		if err := s.ReplaceAnimations(ctx, asset.ID, spriteImport.Animations); err != nil {
			slog.Warn("upload: persist animations",
				"asset_id", asset.ID, "name", asset.Name, "err", err)
		}
	}

	return &UploadResult{
		Asset: asset, Reused: false, OriginalFn: originalName,
		TilemapEligible: tilemapEligible,
		TilemapCells:    tilemapCells, TilemapMeta: tilemapMeta,
		PngBody:      pngBodyForResult(tilemapEligible, body),
		SpriteImport: spriteImport,
	}, nil
}

// pngBodyForResult surfaces the PNG bytes only when the upload is
// tilemap-eligible. Plain sprite / audio / ui_panel uploads don't need
// them on the result, and surfacing them anyway would inflate the
// caller's working set for no benefit.
func pngBodyForResult(tilemapEligible bool, body []byte) []byte {
	if !tilemapEligible {
		return nil
	}
	out := make([]byte, len(body))
	copy(out, body)
	return out
}

// MaxFilesPerUpload caps how many files a single multi-file upload
// request may carry. Sane upper bound for "drag a folder of tiles in";
// designers with larger sets will run multiple uploads.
const MaxFilesPerUpload = 64

// MultiUploadResult is the per-file outcome from UploadMany. Either
// Asset is populated (success) or Err is set (this single file failed
// — others in the batch may have succeeded).
//
// TilemapCells is populated when the resulting asset is tilemap-
// eligible: it lists every 32×32 sub-cell of the sheet (with NonEmpty
// true for cells that contain at least one opaque pixel). The handler
// uses it to build a `tilemaps` row + per-cell tile entity_types so
// the sheet becomes paintable in the Mapmaker palette without any
// extra step. Empty for plain sprite/audio assets.
type MultiUploadResult struct {
	Asset      *Asset
	Reused     bool
	OriginalFn string
	Err        error

	TilemapEligible bool
	TilemapCells    []TileCell
	TilemapMeta     TileSheetMetadata
	// PngBody — see UploadResult.PngBody. Surfaced for tilemap-
	// eligible uploads so the handler can hand them to
	// tilemaps.Service.Create without re-fetching from object storage.
	PngBody []byte

	SpriteImport *ImportResult
}

// isAutoTileSheet returns true for plain PNG uploads that should default to
// tile-sheet slicing: any valid, non-empty, 32px-divisible grid larger than a
// single cell. Explicit Sprite/Sprite sheet/Animated sprite selections bypass
// this and stay sprite assets.
func isAutoTileSheet(body []byte) bool {
	cells, _, err := SliceTileSheet(body)
	if err != nil || len(cells) <= 1 {
		return false
	}
	return true
}

// SpriteSheetSummary describes the persisted frame metadata for a sprite
// upload. Handlers use it for upload-result captions and JSON responses.
type SpriteSheetSummary struct {
	Frames     int    `json:"frames"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
	GridW      int    `json:"grid_w"`
	GridH      int    `json:"grid_h"`
	Source     string `json:"source"`
	Animations int    `json:"animations"`
}

// SpriteSummaryFromImport converts an upload-time import result into the
// small presentation shape users need in the upload summary.
func SpriteSummaryFromImport(ir *ImportResult) SpriteSheetSummary {
	if ir == nil {
		return SpriteSheetSummary{}
	}
	md := ir.SheetMetadata
	frames := md.FrameCount
	if frames == 0 {
		frames = len(ir.Frames)
	}
	return SpriteSheetSummary{
		Frames:     frames,
		Cols:       md.Cols,
		Rows:       md.Rows,
		GridW:      md.GridW,
		GridH:      md.GridH,
		Source:     md.Source,
		Animations: len(ir.Animations),
	}
}

// SpriteSummaryFromMetadata decodes a previously persisted sprite sheet's
// metadata. It returns a zero summary for ordinary single-frame sprites.
func SpriteSummaryFromMetadata(raw []byte) SpriteSheetSummary {
	var md SheetMetadata
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return SpriteSheetSummary{}
	}
	if err := json.Unmarshal(raw, &md); err != nil {
		return SpriteSheetSummary{}
	}
	return SpriteSheetSummary{
		Frames: md.FrameCount,
		Cols:   md.Cols,
		Rows:   md.Rows,
		GridW:  md.GridW,
		GridH:  md.GridH,
		Source: md.Source,
	}
}

func (s SpriteSheetSummary) IsSheet() bool {
	return s.Frames > 1 || s.Cols > 1 || s.Rows > 1
}

func (s SpriteSheetSummary) String() string {
	if !s.IsSheet() {
		return ""
	}
	out := strconv.Itoa(s.Frames) + " frames"
	if s.Animations > 0 {
		out += " · " + strconv.Itoa(s.Animations) + " animations"
	}
	return out
}

// readSidecar locates and reads an Aseprite-style JSON sidecar from
// `siblings` whose base name matches `header`. Aseprite's "Export
// Sprite Sheet" UI produces "<base>.png" + "<base>.json" by default,
// so when both files arrive in the same multi-file upload we can wire
// the sidecar through automatically — designers don't need to flip
// the parser-override dropdown.
//
// Returns nil and no error when no sidecar exists. Read errors are
// non-fatal: missing/unreadable sidecar simply means we fall back to
// auto-slicing.
func readSidecar(header *multipart.FileHeader, siblings []*multipart.FileHeader) []byte {
	if header == nil {
		return nil
	}
	target := strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename)) + ".json"
	for _, sib := range siblings {
		if sib == nil || sib == header {
			continue
		}
		if !strings.EqualFold(filepath.Base(sib.Filename), target) {
			continue
		}
		f, err := sib.Open()
		if err != nil {
			return nil
		}
		body, err := io.ReadAll(io.LimitReader(f, MaxUploadBytes+1))
		_ = f.Close()
		if err != nil || len(body) == 0 || len(body) > MaxUploadBytes {
			return nil
		}
		return body
	}
	return nil
}

// normalizeContentType strips charset/parameters off DetectContentType output
// (e.g. "image/png; charset=utf-8") so the lookup table stays simple.
func normalizeContentType(ct string) string {
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// defaultDisplayName produces a designer-friendly default from a filename.
// "BossSheet_v3.png" -> "BossSheet_v3".
func defaultDisplayName(filename string) string {
	base := filepath.Base(filename)
	if base == "" || base == "." {
		return "untitled"
	}
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return "untitled"
	}
	return base
}

// uniquify appends a short suffix from the content key when (kind, name)
// collides on insert. Eight chars of hex is plenty to disambiguate without
// being unwieldy.
func uniquify(base, contentKey string) string {
	const tailLen = 8
	tail := contentKey
	if len(tail) > tailLen {
		tail = tail[len(tail)-tailLen:]
	}
	return base + " (" + tail + ")"
}
