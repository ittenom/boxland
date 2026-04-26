// Boxland — characters: recipe normalization and hashing.
//
// A Recipe row stores three opaque JSON blobs (appearance/stats/talents).
// To dedup bakes by content we need a *canonical* byte representation
// that's identical for every recipe with the same meaningful content,
// regardless of map-iteration order, integer formatting, or whitespace.
//
// The canonicalizer here:
//
//   - parses each blob as generic JSON (json.Unmarshal into any),
//   - walks the tree and serializes objects with sorted keys,
//   - serializes arrays in input order (arrays carry meaning — e.g. layer
//     order), but rejects arrays of objects keyed by `slot_key` (we sort
//     those by `slot_key` so paint-order metadata wins, not insertion
//     order),
//   - emits compact JSON with no whitespace.
//
// Hash = sha256 of `{"appearance":<canon>,"stats":<canon>,"talents":<canon>,"name":<recipe.Name>}`.
// Name is included so two recipes with identical art/stats/talents but
// different display names produce different hashes; downstream the bake
// dedup is keyed on this hash exactly.
//
// This file is pure logic — no DB, no I/O. Tested via recipes_test.go.

package characters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

// AppearanceSlot is one entry in the appearance.slots array. Defined
// here (not in definitions.go) because it's the canonical wire shape
// the generator UI POSTs and the bake reads — it's recipe-private.
type AppearanceSlot struct {
	SlotKey    string            `json:"slot_key"`
	PartID     int64             `json:"part_id"`
	LayerOrder *int32            `json:"layer_order,omitempty"` // override; nil = inherit
	Palette    map[string]string `json:"palette,omitempty"`     // region key -> color hex
}

// AppearanceSelection is the typed shape we expect for a recipe's
// appearance JSON. Stored as opaque JSON in the row so future fields
// don't need a migration; round-tripping it through this struct is the
// canonical way to read it.
type AppearanceSelection struct {
	Slots []AppearanceSlot `json:"slots"`
}

// StatSelection is the typed shape we expect for stats JSON. SetID is
// the stat_set the player allocated against; Allocations are the
// designer-pickable points-spent values keyed by stat key.
type StatSelection struct {
	SetID       int64          `json:"set_id"`
	Allocations map[string]int `json:"allocations"`
}

// TalentSelection is the typed shape we expect for talents JSON. Each
// entry under Picks is "tree_key.node_key" -> rank.
type TalentSelection struct {
	Picks map[string]int `json:"picks"`
}

// NormalizeRecipe produces canonical bytes for a recipe payload. The
// returned slice is the input to ComputeRecipeHash. This function never
// touches the DB and is safe to call from any goroutine.
func NormalizeRecipe(name string, appearance, stats, talents []byte) ([]byte, error) {
	canonAppearance, err := canonicalizeAppearance(appearance)
	if err != nil {
		return nil, fmt.Errorf("normalize appearance: %w", err)
	}
	canonStats, err := canonicalizeStats(stats)
	if err != nil {
		return nil, fmt.Errorf("normalize stats: %w", err)
	}
	canonTalents, err := canonicalizeTalents(talents)
	if err != nil {
		return nil, fmt.Errorf("normalize talents: %w", err)
	}

	// Assemble a single canonical envelope. Use a small ordered struct
	// rather than a map so we can guarantee key order without sorting.
	type envelope struct {
		Name       string          `json:"name"`
		Appearance json.RawMessage `json:"appearance"`
		Stats      json.RawMessage `json:"stats"`
		Talents    json.RawMessage `json:"talents"`
	}
	body, err := json.Marshal(envelope{
		Name:       name,
		Appearance: canonAppearance,
		Stats:      canonStats,
		Talents:    canonTalents,
	})
	if err != nil {
		return nil, fmt.Errorf("envelope marshal: %w", err)
	}
	return body, nil
}

// ComputeRecipeHash returns sha256(NormalizeRecipe(...)). Returns 32 bytes.
func ComputeRecipeHash(name string, appearance, stats, talents []byte) ([]byte, error) {
	canon, err := NormalizeRecipe(name, appearance, stats, talents)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canon)
	return sum[:], nil
}

// canonicalizeAppearance parses the input as AppearanceSelection,
// sorts slots by slot_key, sorts each palette map by key, and emits
// compact JSON. Empty/null input collapses to {"slots":[]}.
func canonicalizeAppearance(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return []byte(`{"slots":[]}`), nil
	}
	var sel AppearanceSelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return nil, fmt.Errorf("appearance: %w", err)
	}
	// Sort slots by slot_key so designer reorderings of the same
	// selections don't change the hash.
	sort.Slice(sel.Slots, func(i, j int) bool {
		return sel.Slots[i].SlotKey < sel.Slots[j].SlotKey
	})
	// Reject duplicate slot_keys: each slot can be filled at most once
	// per recipe. Cleaner to fail here than to let the bake see two
	// parts on `body`.
	seen := make(map[string]struct{}, len(sel.Slots))
	for _, s := range sel.Slots {
		if s.SlotKey == "" {
			return nil, fmt.Errorf("appearance: slot_key is required")
		}
		if _, dup := seen[s.SlotKey]; dup {
			return nil, fmt.Errorf("appearance: slot_key %q appears more than once", s.SlotKey)
		}
		seen[s.SlotKey] = struct{}{}
	}
	// json.Marshal already sorts map keys alphabetically (Go std lib
	// guarantee since 1.12), so palette maps inside each slot
	// canonicalize automatically.
	return json.Marshal(sel)
}

// canonicalizeStats parses StatSelection, sorts the allocations map
// (json.Marshal does this for free), and emits compact JSON. Empty
// input collapses to {"set_id":0,"allocations":{}}.
func canonicalizeStats(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return []byte(`{"allocations":{},"set_id":0}`), nil
	}
	var sel StatSelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}
	if sel.Allocations == nil {
		sel.Allocations = map[string]int{}
	}
	return json.Marshal(sel)
}

// canonicalizeTalents parses TalentSelection. Same shape rules.
func canonicalizeTalents(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return []byte(`{"picks":{}}`), nil
	}
	var sel TalentSelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return nil, fmt.Errorf("talents: %w", err)
	}
	if sel.Picks == nil {
		sel.Picks = map[string]int{}
	}
	return json.Marshal(sel)
}

// ---------------------------------------------------------------------------
// Recipe -> BakeRecipe materialization
// ---------------------------------------------------------------------------

// LoadBakeRecipe reads a character_recipes row plus all its referenced
// parts and slots inside the supplied tx, and assembles the BakeRecipe
// shape RunBake expects. Used by NpcTemplateHandler.Publish (and later
// by the player-side save-and-bake path).
//
// Returns ErrRecipeNotFound when the recipe id doesn't exist in this tx.
func LoadBakeRecipe(ctx context.Context, tx pgx.Tx, recipeID int64) (BakeRecipe, error) {
	var r struct {
		Name       string
		OwnerKind  string
		OwnerID    int64
		Appearance []byte
		Stats      []byte
		Talents    []byte
	}
	err := tx.QueryRow(ctx, `
		SELECT name, owner_kind, owner_id, appearance_json, stats_json, talents_json
		FROM character_recipes WHERE id = $1
	`, recipeID).Scan(&r.Name, &r.OwnerKind, &r.OwnerID, &r.Appearance, &r.Stats, &r.Talents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BakeRecipe{}, ErrRecipeNotFound
		}
		return BakeRecipe{}, fmt.Errorf("load recipe: %w", err)
	}

	var sel AppearanceSelection
	if len(r.Appearance) > 0 {
		if err := json.Unmarshal(r.Appearance, &sel); err != nil {
			return BakeRecipe{}, fmt.Errorf("recipe appearance: %w", err)
		}
	}
	if len(sel.Slots) == 0 {
		return BakeRecipe{}, errors.New("recipe has no appearance.slots entries")
	}

	// Resolve slot keys -> Slot rows (one batched query keyed by key).
	slotKeys := make([]string, 0, len(sel.Slots))
	partIDs := make([]int64, 0, len(sel.Slots))
	for _, s := range sel.Slots {
		slotKeys = append(slotKeys, s.SlotKey)
		partIDs = append(partIDs, s.PartID)
	}
	slotByKey, err := loadSlotsByKey(ctx, tx, slotKeys)
	if err != nil {
		return BakeRecipe{}, err
	}
	partByID, err := loadPartsByID(ctx, tx, partIDs)
	if err != nil {
		return BakeRecipe{}, err
	}

	// Build BakedSelection in appearance order. layer_order = explicit
	// override or slot.default_layer_order.
	out := make([]BakedSelection, 0, len(sel.Slots))
	for _, s := range sel.Slots {
		slot, ok := slotByKey[s.SlotKey]
		if !ok {
			return BakeRecipe{}, fmt.Errorf("recipe references unknown slot key %q", s.SlotKey)
		}
		part, ok := partByID[s.PartID]
		if !ok {
			return BakeRecipe{}, fmt.Errorf("recipe references unknown part id %d", s.PartID)
		}
		if part.SlotID != slot.ID {
			return BakeRecipe{}, fmt.Errorf("part %d does not belong to slot %q", part.ID, slot.Key)
		}
		layer := slot.DefaultLayerOrder
		if s.LayerOrder != nil {
			layer = *s.LayerOrder
		} else if part.LayerOrder != nil {
			layer = *part.LayerOrder
		}
		out = append(out, BakedSelection{Slot: slot, Part: part, LayerOrder: layer})
	}

	return BakeRecipe{
		Name:           r.Name,
		OwnerKind:      OwnerKind(r.OwnerKind),
		OwnerID:        r.OwnerID,
		Selections:     out,
		AppearanceJSON: r.Appearance,
		StatsJSON:      r.Stats,
		TalentsJSON:    r.Talents,
	}, nil
}

// loadSlotsByKey fetches every character_slots row whose key is in keys.
func loadSlotsByKey(ctx context.Context, tx pgx.Tx, keys []string) (map[string]Slot, error) {
	if len(keys) == 0 {
		return map[string]Slot{}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, key, label, required, order_index, default_layer_order, allows_palette,
		       created_by, created_at, updated_at
		FROM character_slots WHERE key = ANY($1::text[])
	`, keys)
	if err != nil {
		return nil, fmt.Errorf("load slots: %w", err)
	}
	defer rows.Close()
	out := make(map[string]Slot, len(keys))
	for rows.Next() {
		var s Slot
		if err := rows.Scan(&s.ID, &s.Key, &s.Label, &s.Required, &s.OrderIndex,
			&s.DefaultLayerOrder, &s.AllowsPalette, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan slot: %w", err)
		}
		out[s.Key] = s
	}
	return out, rows.Err()
}

// loadPartsByID fetches every character_parts row whose id is in ids.
func loadPartsByID(ctx context.Context, tx pgx.Tx, ids []int64) (map[int64]Part, error) {
	if len(ids) == 0 {
		return map[int64]Part{}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, slot_id, asset_id, name, tags, compatible_tags,
		       layer_order, frame_map_json, palette_regions_json,
		       created_by, created_at, updated_at
		FROM character_parts WHERE id = ANY($1::bigint[])
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("load parts: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]Part, len(ids))
	for rows.Next() {
		var p Part
		if err := rows.Scan(&p.ID, &p.SlotID, &p.AssetID, &p.Name, &p.Tags, &p.CompatibleTags,
			&p.LayerOrder, &p.FrameMapJSON, &p.PaletteRegionsJSON,
			&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan part: %w", err)
		}
		if p.Tags == nil {
			p.Tags = []string{}
		}
		if p.CompatibleTags == nil {
			p.CompatibleTags = []string{}
		}
		out[p.ID] = p
	}
	return out, rows.Err()
}
