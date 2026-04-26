// Boxland — characters: bake pipeline.
//
// A bake takes a Recipe + the live Slot/Part/Asset rows it references
// and produces a single composed sprite sheet PNG. The output sheet
// has one row per canonical animation and one column per frame in the
// longest animation; each frame is 32x32 pixels.
//
// The bake is content-addressed by the recipe hash: identical recipe
// content (per characters.NormalizeRecipe) always produces an identical
// PNG and lands at the same object-store key, so re-baking the same
// recipe is a no-op. The (recipe_hash) WHERE status='baked' partial
// unique index on character_bakes enforces the dedup at the DB level.
//
// Bake runs INSIDE the publish transaction (see NpcTemplateHandler.Publish
// in artifact.go). Every read uses the supplied tx so the bake sees the
// transaction's view of the world; image fetches go through ObjectStore
// directly (no tx — object storage doesn't support transactions, which
// is fine because PNG bytes at a given key are immutable).
//
// Phase 2 scope: layered composition with no palette tinting. Palette
// regions (per-part recolor) are validated through but ignored at bake
// time; they land in a follow-up.
//
// Frame contract: each part's frame_map_json is `{"<anim>": [from, to]}`,
// one entry per canonical animation the part covers. The bake takes the
// INTERSECTION of canonical anim names across all selected parts; if a
// recipe has parts that don't all cover the same anims, only the common
// ones make it into the bake. A recipe whose intersection is empty is a
// validation error.

package characters

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/assets"
	"boxland/server/internal/persistence"
)

// FrameSize is the canonical character-sprite frame edge. Pinned at 32
// for the first slice; changing this is a breaking change to every
// recipe and every existing bake (consider a new constant + migration
// in a follow-up if larger frames are needed).
const FrameSize = 32

// BakeOutputPrefix is the object-store path prefix for character bake
// outputs. Distinct from `assets/` (uploads) and `asset_variants/`
// (palette bakes) so it's easy to count, audit, or GC.
const BakeOutputPrefix = "character_bakes"

// BakeDeps bundles the small set of services bake.Run needs. Callers
// (currently NpcTemplateHandler.Publish) pass these in so this package
// doesn't have to know about the global service graph.
type BakeDeps struct {
	Store  *persistence.ObjectStore
	Assets *assets.Service
}

// BakeOutcome reports what bake.Run did. Callers update the NPC template
// (or player_character) to point at AssetID + persist a character_bakes
// row referencing it.
type BakeOutcome struct {
	BakeID    int64  // character_bakes.id of the persisted bake row
	AssetID   int64  // assets.id of the composed sprite asset
	OutputKey string // object-store key (= the asset's content_addressed_path)
	Reused    bool   // true if a baked row for this hash already existed
	Anims     []bakedAnim
}

// bakedAnim names one animation in the output sheet.
type bakedAnim struct {
	Name       string `json:"name"`
	FrameFrom  int32  `json:"frame_from"`
	FrameTo    int32  `json:"frame_to"`
	FrameCount int32  `json:"frame_count"`
}

// BakeRecipe is the canonical input to Bake. Built by the caller from a
// stored Recipe row plus the resolved live data (parts + their assets).
type BakeRecipe struct {
	Name           string              // recipe.Name (folded into hash)
	OwnerKind      OwnerKind
	OwnerID        int64
	Selections     []BakedSelection    // one per appearance.slots entry
	AppearanceJSON json.RawMessage     // raw appearance bytes (used for hashing)
	StatsJSON      json.RawMessage
	TalentsJSON    json.RawMessage
}

// BakedSelection is one resolved layered part: the part itself plus the
// slot's default layer order so we can sort without a second lookup.
type BakedSelection struct {
	Slot       Slot
	Part       Part
	LayerOrder int32 // effective: part.LayerOrder if set, else slot.DefaultLayerOrder
}

// RunBake composes the sprite sheet, uploads it, inserts/finds the asset
// row, and persists the character_bakes row. Always called inside a
// publish transaction (the supplied tx).
//
// On any failure before the asset row is created the function returns
// an error and writes nothing. After the asset row is created (i.e. the
// PNG is in object storage), errors persist a `failed` bake row so the
// designer sees the diagnostic on the next publish.
func RunBake(
	ctx context.Context,
	tx pgx.Tx,
	deps BakeDeps,
	recipe BakeRecipe,
	recipeID int64,
) (BakeOutcome, error) {
	if deps.Store == nil || deps.Assets == nil {
		return BakeOutcome{}, errors.New("bake: BakeDeps missing Store or Assets")
	}
	if len(recipe.Selections) == 0 {
		return BakeOutcome{}, errors.New("bake: recipe has no selections")
	}

	// 1. Compute the recipe hash. This is the dedup key.
	recipeHash, err := ComputeRecipeHash(recipe.Name, recipe.AppearanceJSON, recipe.StatsJSON, recipe.TalentsJSON)
	if err != nil {
		return BakeOutcome{}, fmt.Errorf("bake hash: %w", err)
	}

	// 2. Look for an existing successful bake by hash (inside the tx so
	// concurrent publishes see each other's commits).
	if existing, ok, err := findBakedByHash(ctx, tx, recipeHash); err != nil {
		return BakeOutcome{}, fmt.Errorf("bake lookup: %w", err)
	} else if ok {
		return BakeOutcome{
			BakeID:    existing.id,
			AssetID:   existing.assetID,
			OutputKey: existing.outputKey,
			Reused:    true,
		}, nil
	}

	// 3. Decide canonical animations = intersection of all parts'
	// frame_map_json keys. Only anims every part covers make it in.
	anims, framePlans, err := planAnimations(recipe.Selections)
	if err != nil {
		return BakeOutcome{}, fmt.Errorf("bake plan: %w", err)
	}

	// 4. Sort selections by effective layer order (low draws first; ties
	// broken by part_id for determinism).
	layered := append([]BakedSelection(nil), recipe.Selections...)
	sort.SliceStable(layered, func(i, j int) bool {
		if layered[i].LayerOrder != layered[j].LayerOrder {
			return layered[i].LayerOrder < layered[j].LayerOrder
		}
		return layered[i].Part.ID < layered[j].Part.ID
	})

	// 5. Fetch every source PNG once. Batched read: one Get per unique
	// asset id (not per (part, frame)).
	srcImages, err := loadSourceImages(ctx, tx, deps, layered)
	if err != nil {
		return BakeOutcome{}, fmt.Errorf("bake load sources: %w", err)
	}

	// 6. Compose the output PNG. Each canonical anim gets its own row.
	pngBytes, err := composeSheet(anims, framePlans, layered, srcImages)
	if err != nil {
		return BakeOutcome{}, fmt.Errorf("bake compose: %w", err)
	}

	// 7. Content-addressed object key + upload. The key is derived from
	// the PNG bytes themselves so identical compositions land at the
	// same key regardless of how they got there.
	outKey := persistence.ContentAddressedKey(BakeOutputPrefix, pngBytes)
	if err := deps.Store.Put(ctx, outKey, "image/png", bytes.NewReader(pngBytes), int64(len(pngBytes))); err != nil {
		// Object-store upload is the first persistent side effect; if
		// it fails we record a `failed` bake row so the designer has
		// something to look at, then return the error.
		_ = persistFailedBake(ctx, tx, recipeID, recipeHash, "upload: "+err.Error())
		return BakeOutcome{}, fmt.Errorf("bake upload: %w", err)
	}

	// 8. Insert the assets row inside the tx. Use FindByContentPath
	// first so re-running with identical bytes doesn't violate the
	// (kind,name) unique index.
	assetID, err := upsertBakeAsset(ctx, tx, recipe, outKey, recipeHash, anims)
	if err != nil {
		_ = persistFailedBake(ctx, tx, recipeID, recipeHash, "asset upsert: "+err.Error())
		return BakeOutcome{}, fmt.Errorf("bake asset upsert: %w", err)
	}

	// 9. Insert asset_animations rows describing each canonical anim's
	// frame range in the composed sheet.
	if err := insertBakeAnimations(ctx, tx, assetID, anims); err != nil {
		_ = persistFailedBake(ctx, tx, recipeID, recipeHash, "anims: "+err.Error())
		return BakeOutcome{}, fmt.Errorf("bake anims: %w", err)
	}

	// 10. Persist the character_bakes row as `baked`. ON CONFLICT on the
	// partial unique index makes a concurrent successful bake a no-op.
	bakeID, err := persistBakedRow(ctx, tx, recipeID, recipeHash, assetID)
	if err != nil {
		return BakeOutcome{}, fmt.Errorf("bake row persist: %w", err)
	}

	return BakeOutcome{
		BakeID:    bakeID,
		AssetID:   assetID,
		OutputKey: outKey,
		Reused:    false,
		Anims:     anims,
	}, nil
}

// ---------------------------------------------------------------------------
// Animation planning
// ---------------------------------------------------------------------------

// framePlan describes one canonical animation: the layout in the output
// sheet plus each selection's source frame range for that anim.
type framePlan struct {
	Name       string
	FrameCount int
	OutputRow  int             // 0-based row in the output sheet
	Sources    []sourceMapping // one per BakedSelection (in same order)
}

type sourceMapping struct {
	From int // frame index in the part's source asset
	To   int // inclusive
}

// planAnimations computes the intersection of canonical anim names
// across every selection's frame_map_json, then for each anim picks
// the COMMON frame count (= min across selections). Returns the
// description list (one per anim, in alphabetical order for
// determinism) and the per-anim frame plan.
func planAnimations(selections []BakedSelection) ([]bakedAnim, []framePlan, error) {
	if len(selections) == 0 {
		return nil, nil, errors.New("plan: no selections")
	}

	// Parse every selection's frame_map_json once.
	maps := make([]map[string][2]int, len(selections))
	for i, s := range selections {
		fm, err := parseFrameMap(s.Part.FrameMapJSON)
		if err != nil {
			return nil, nil, fmt.Errorf("part %d (%s): %w", s.Part.ID, s.Part.Name, err)
		}
		if len(fm) == 0 {
			return nil, nil, fmt.Errorf("part %d (%s) has empty frame_map", s.Part.ID, s.Part.Name)
		}
		maps[i] = fm
	}

	// Intersect anim names.
	intersection := make(map[string]struct{})
	for name := range maps[0] {
		intersection[name] = struct{}{}
	}
	for _, m := range maps[1:] {
		for name := range intersection {
			if _, ok := m[name]; !ok {
				delete(intersection, name)
			}
		}
	}
	if len(intersection) == 0 {
		return nil, nil, errors.New("plan: no canonical animations are covered by every selected part")
	}

	// Sorted anim names for deterministic output ordering.
	names := make([]string, 0, len(intersection))
	for name := range intersection {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build the per-anim plan. The common frame count is the min across
	// selections — short anims clip long ones to keep frame indices in
	// sync.
	plans := make([]framePlan, 0, len(names))
	descs := make([]bakedAnim, 0, len(names))
	cumulativeFrames := 0
	for row, name := range names {
		minCount := -1
		for _, m := range maps {
			fr := m[name]
			cnt := fr[1] - fr[0] + 1
			if cnt < 1 {
				return nil, nil, fmt.Errorf("plan: anim %q has invalid frame range [%d,%d]", name, fr[0], fr[1])
			}
			if minCount < 0 || cnt < minCount {
				minCount = cnt
			}
		}
		sources := make([]sourceMapping, len(selections))
		for i, m := range maps {
			fr := m[name]
			sources[i] = sourceMapping{From: fr[0], To: fr[0] + minCount - 1}
		}
		plans = append(plans, framePlan{
			Name:       name,
			FrameCount: minCount,
			OutputRow:  row,
			Sources:    sources,
		})
		descs = append(descs, bakedAnim{
			Name:       name,
			FrameFrom:  int32(cumulativeFrames),
			FrameTo:    int32(cumulativeFrames + minCount - 1),
			FrameCount: int32(minCount),
		})
		cumulativeFrames += minCount
	}
	return descs, plans, nil
}

// parseFrameMap parses {"idle":[0,3], ...} into a map. Tolerates a
// single-int "frame index" shorthand by treating it as a one-frame range.
func parseFrameMap(raw json.RawMessage) (map[string][2]int, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string][2]int{}, nil
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("frame_map: %w", err)
	}
	out := make(map[string][2]int, len(generic))
	for k, v := range generic {
		if k == "" {
			return nil, errors.New("frame_map: empty animation name")
		}
		// Try [from, to] first.
		var pair [2]int
		if err := json.Unmarshal(v, &pair); err == nil {
			if pair[0] < 0 || pair[1] < pair[0] {
				return nil, fmt.Errorf("frame_map[%s]: invalid range [%d,%d]", k, pair[0], pair[1])
			}
			out[k] = pair
			continue
		}
		// Fall back to a bare int.
		var single int
		if err := json.Unmarshal(v, &single); err != nil {
			return nil, fmt.Errorf("frame_map[%s]: must be [from,to] or int, got %s", k, string(v))
		}
		if single < 0 {
			return nil, fmt.Errorf("frame_map[%s]: frame index must be >= 0", k)
		}
		out[k] = [2]int{single, single}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Source image loading
// ---------------------------------------------------------------------------

// loadSourceImages fetches each unique source asset's PNG and decodes
// it. Returns a map keyed by asset_id so composeSheet can look them up
// by selection.
func loadSourceImages(
	ctx context.Context,
	tx pgx.Tx,
	deps BakeDeps,
	selections []BakedSelection,
) (map[int64]image.Image, error) {
	// Resolve unique asset ids and their content paths via one batched
	// query (avoids N+1 against a tx).
	idSet := make(map[int64]struct{}, len(selections))
	idList := make([]int64, 0, len(selections))
	for _, s := range selections {
		if _, dup := idSet[s.Part.AssetID]; dup {
			continue
		}
		idSet[s.Part.AssetID] = struct{}{}
		idList = append(idList, s.Part.AssetID)
	}
	pathByID := make(map[int64]string, len(idList))
	rows, err := tx.Query(ctx, `
		SELECT id, content_addressed_path FROM assets WHERE id = ANY($1::bigint[])
	`, idList)
	if err != nil {
		return nil, fmt.Errorf("load asset paths: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			return nil, fmt.Errorf("scan asset path: %w", err)
		}
		pathByID[id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range idList {
		if _, ok := pathByID[id]; !ok {
			return nil, fmt.Errorf("source asset %d not found (deleted?)", id)
		}
	}

	// Fetch + decode each PNG.
	imgs := make(map[int64]image.Image, len(idList))
	for id, key := range pathByID {
		body, err := readAll(ctx, deps.Store, key)
		if err != nil {
			return nil, fmt.Errorf("read source asset %d (%s): %w", id, key, err)
		}
		img, err := png.Decode(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("decode source asset %d: %w", id, err)
		}
		imgs[id] = img
	}
	return imgs, nil
}

// readAll is a tiny wrapper around ObjectStore.Get + io.ReadAll.
func readAll(ctx context.Context, store *persistence.ObjectStore, key string) ([]byte, error) {
	r, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// ---------------------------------------------------------------------------
// Composition
// ---------------------------------------------------------------------------

// composeSheet builds the output sprite sheet from the planned anims.
// Layout: row r = anims[r], col c = frame c of that anim. Output is
// always (cols × FrameSize) wide × (len(anims) × FrameSize) tall.
//
// Source frames are addressed by treating each source asset as a
// row-major grid of FrameSize-square cells. Sources whose width is not
// a multiple of FrameSize are still tolerated — we treat the floor of
// (width / FrameSize) as the number of columns. Frames beyond a
// source's available cell count are skipped (transparent).
func composeSheet(
	anims []bakedAnim,
	plans []framePlan,
	layered []BakedSelection,
	srcImages map[int64]image.Image,
) ([]byte, error) {
	if len(plans) != len(anims) {
		return nil, errors.New("compose: anim/plan length mismatch")
	}
	maxFrames := 0
	for _, a := range anims {
		if int(a.FrameCount) > maxFrames {
			maxFrames = int(a.FrameCount)
		}
	}
	if maxFrames == 0 {
		return nil, errors.New("compose: no frames to bake")
	}

	out := image.NewNRGBA(image.Rect(0, 0, maxFrames*FrameSize, len(anims)*FrameSize))

	for _, plan := range plans {
		dstY := plan.OutputRow * FrameSize
		// For each frame in this anim, blit every layered selection in
		// order. Earlier selections (lower layer_order) draw first.
		for f := 0; f < plan.FrameCount; f++ {
			dstX := f * FrameSize
			dstRect := image.Rect(dstX, dstY, dstX+FrameSize, dstY+FrameSize)
			for selIdx, sel := range layered {
				src := srcImages[sel.Part.AssetID]
				if src == nil {
					continue
				}
				srcCols := src.Bounds().Dx() / FrameSize
				if srcCols < 1 {
					continue
				}
				srcFrame := plan.Sources[selIdx].From + f
				sx := (srcFrame % srcCols) * FrameSize
				sy := (srcFrame / srcCols) * FrameSize
				srcRect := image.Rect(sx, sy, sx+FrameSize, sy+FrameSize)
				if !srcRect.In(src.Bounds()) {
					continue
				}
				// draw.Over composites alpha; nearest-neighbor by
				// virtue of Rect-aligned blits at integer offsets.
				draw.Draw(out, dstRect, src, srcRect.Min, draw.Over)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// ---------------------------------------------------------------------------
// Asset + bake row persistence
// ---------------------------------------------------------------------------

// upsertBakeAsset returns an existing assets row at outKey if one
// exists; otherwise inserts a new sprite asset whose
// content_addressed_path = outKey. The sprite's name embeds the bake
// hash so it's stable + recognizable in the asset list.
func upsertBakeAsset(
	ctx context.Context,
	tx pgx.Tx,
	recipe BakeRecipe,
	outKey string,
	recipeHash []byte,
	anims []bakedAnim,
) (int64, error) {
	// Look up an existing sprite row for this exact path. PNG bytes at
	// a content-addressed key are immutable, so reuse is always safe.
	var existing int64
	err := tx.QueryRow(ctx, `
		SELECT id FROM assets
		WHERE kind = 'sprite' AND content_addressed_path = $1
	`, outKey).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	// Compose the sheet metadata. Cols = max frames-per-anim, Rows =
	// number of anims, FrameCount = total frames across all anims.
	maxFrames, total := 0, 0
	for _, a := range anims {
		if int(a.FrameCount) > maxFrames {
			maxFrames = int(a.FrameCount)
		}
		total += int(a.FrameCount)
	}
	meta := assets.SheetMetadata{
		GridW: FrameSize, GridH: FrameSize,
		Cols: maxFrames, Rows: len(anims),
		FrameCount: total, Source: "character_bake",
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return 0, fmt.Errorf("marshal sheet metadata: %w", err)
	}

	// Generate a stable, unique-per-recipe-hash name.
	hashHex := hex.EncodeToString(recipeHash)
	name := fmt.Sprintf("character/%s/%s", sanitizeName(recipe.Name), hashHex[:12])

	// created_by: designer-owned recipes carry the designer id; player
	// recipes share a synthetic owner id (the player_id) but the
	// assets.created_by FK is to designers(id). For Phase 2 we limit
	// bake-on-publish to designer recipes; player flows ship in Phase 4
	// and will need a separate "system" designer id for player-derived
	// bakes. Keep the constraint visible:
	if recipe.OwnerKind != OwnerKindDesigner {
		return 0, fmt.Errorf("bake: only designer recipes can be baked in Phase 2 (got %s)", recipe.OwnerKind)
	}
	createdBy := recipe.OwnerID

	var newID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO assets
			(kind, name, content_addressed_path, original_format, metadata_json, tags, created_by)
		VALUES
			('sprite', $1, $2, 'png', $3, $4::text[], $5)
		RETURNING id
	`, name, outKey, metaJSON, []string{"character_bake"}, createdBy).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("insert asset row: %w", err)
	}
	return newID, nil
}

// insertBakeAnimations writes one asset_animations row per canonical
// animation in the composed sheet.
func insertBakeAnimations(ctx context.Context, tx pgx.Tx, assetID int64, anims []bakedAnim) error {
	for _, a := range anims {
		if _, err := tx.Exec(ctx, `
			INSERT INTO asset_animations (asset_id, name, frame_from, frame_to, direction, fps)
			VALUES ($1, $2, $3, $4, 'forward', 8)
			ON CONFLICT (asset_id, name) DO NOTHING
		`, assetID, a.Name, int(a.FrameFrom), int(a.FrameTo)); err != nil {
			return fmt.Errorf("insert anim %q: %w", a.Name, err)
		}
	}
	return nil
}

// existingBaked carries the small struct findBakedByHash returns.
type existingBaked struct {
	id        int64
	assetID   int64
	outputKey string
}

// findBakedByHash returns the most recent successful bake (if any) for
// the given recipe hash. The partial unique index character_bakes_hash_baked_uniq
// guarantees at most one row matches.
func findBakedByHash(ctx context.Context, tx pgx.Tx, hash []byte) (existingBaked, bool, error) {
	var out existingBaked
	var assetID *int64
	var contentPath *string
	err := tx.QueryRow(ctx, `
		SELECT b.id, b.asset_id, a.content_addressed_path
		FROM character_bakes b
		LEFT JOIN assets a ON a.id = b.asset_id
		WHERE b.recipe_hash = $1 AND b.status = 'baked'
	`, hash).Scan(&out.id, &assetID, &contentPath)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return existingBaked{}, false, nil
		}
		return existingBaked{}, false, err
	}
	if assetID != nil {
		out.assetID = *assetID
	}
	if contentPath != nil {
		out.outputKey = *contentPath
	}
	// If the linked asset was deleted (asset_id IS NULL), treat the
	// bake as a miss so the caller re-bakes.
	if assetID == nil {
		return existingBaked{}, false, nil
	}
	return out, true, nil
}

// persistBakedRow inserts a character_bakes row in `baked` status. The
// partial unique index (recipe_hash) WHERE status='baked' prevents two
// successful rows with the same hash; ON CONFLICT DO NOTHING + a
// follow-up SELECT covers the concurrent-bake race.
func persistBakedRow(ctx context.Context, tx pgx.Tx, recipeID int64, hash []byte, assetID int64) (int64, error) {
	now := time.Now().UTC()
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO character_bakes
			(recipe_id, recipe_hash, asset_id, status, baked_at)
		VALUES
			($1, $2, $3, 'baked', $4)
		ON CONFLICT (recipe_hash) WHERE status = 'baked' DO NOTHING
		RETURNING id
	`, recipeID, hash, assetID, now).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	// Lost the race: another publish committed first. Read the winner.
	if err := tx.QueryRow(ctx, `
		SELECT id FROM character_bakes WHERE recipe_hash = $1 AND status = 'baked'
	`, hash).Scan(&id); err != nil {
		return 0, fmt.Errorf("re-read baked row: %w", err)
	}
	return id, nil
}

// persistFailedBake records a failed bake so the designer can see why.
// Uses ON CONFLICT DO UPDATE on (recipe_id, status) — but since there's
// no such index, just insert; multiple failed rows are acceptable
// (history of failures is useful diagnostic data).
func persistFailedBake(ctx context.Context, tx pgx.Tx, recipeID int64, hash []byte, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO character_bakes (recipe_id, recipe_hash, status, failure_reason)
		VALUES ($1, $2, 'failed', $3)
	`, recipeID, hash, reason)
	return err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// sanitizeName normalizes a recipe display name into a path-safe
// fragment for embedding in the baked asset name.
func sanitizeName(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == ' ', c == '-', c == '_':
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "unnamed"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return string(out)
}


