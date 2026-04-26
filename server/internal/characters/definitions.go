// Boxland — characters: row types, owner kinds, and shared validation
// constants. All rows here are repo-managed via persistence/repo.Repo[T];
// follow the tag conventions documented in that package.

package characters

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Constants and small enums
// ---------------------------------------------------------------------------

// OwnerKind identifies the polymorphic owner of a character_recipes row.
type OwnerKind string

const (
	OwnerKindDesigner OwnerKind = "designer"
	OwnerKindPlayer   OwnerKind = "player"
)

// Validate reports whether OwnerKind is one of the allowed values.
func (o OwnerKind) Validate() error {
	switch o {
	case OwnerKindDesigner, OwnerKindPlayer:
		return nil
	default:
		return fmt.Errorf("characters: owner_kind %q is not one of designer|player", o)
	}
}

// BakeStatus is the lifecycle of a character_bakes row.
type BakeStatus string

const (
	BakeStatusPending BakeStatus = "pending"
	BakeStatusBaked   BakeStatus = "baked"
	BakeStatusFailed  BakeStatus = "failed"
)

// Validate reports whether BakeStatus is one of the allowed values.
func (s BakeStatus) Validate() error {
	switch s {
	case BakeStatusPending, BakeStatusBaked, BakeStatusFailed:
		return nil
	default:
		return fmt.Errorf("characters: bake status %q is not one of pending|baked|failed", s)
	}
}

// TalentLayoutMode mirrors the CHECK on character_talent_trees.layout_mode.
type TalentLayoutMode string

const (
	LayoutTree     TalentLayoutMode = "tree"
	LayoutTiered   TalentLayoutMode = "tiered"
	LayoutFreeList TalentLayoutMode = "free_list"
	LayoutWeb      TalentLayoutMode = "web"
)

// Validate reports whether the layout mode is one of the allowed values.
func (m TalentLayoutMode) Validate() error {
	switch m {
	case LayoutTree, LayoutTiered, LayoutFreeList, LayoutWeb:
		return nil
	default:
		return fmt.Errorf("characters: talent layout_mode %q is not one of tree|tiered|free_list|web", m)
	}
}

// StatKind classifies a stat definition (NOT a column on the row directly;
// stored inside stat_sets.stats_json[].kind).
type StatKind string

const (
	StatCore     StatKind = "core"
	StatDerived  StatKind = "derived"
	StatResource StatKind = "resource"
	StatHidden   StatKind = "hidden"
)

// Validate reports whether the stat kind is one of the allowed values.
func (k StatKind) Validate() error {
	switch k {
	case StatCore, StatDerived, StatResource, StatHidden:
		return nil
	default:
		return fmt.Errorf("characters: stat kind %q is not one of core|derived|resource|hidden", k)
	}
}

// MaxNameLen caps human-typed names. Aligned with assets/entities.
const MaxNameLen = 128

// MaxBioLen caps player_characters.public_bio. Generous but bounded so
// a single recipe can't blow up the row size.
const MaxBioLen = 4096

// MaxRecipeJSONBytes caps the canonicalized recipe size after Normalize.
// Designed to comfortably hold ~24 slots × selections + a few hundred
// stat allocations + a few hundred talent picks. Past this the recipe
// is almost certainly malformed or hostile.
const MaxRecipeJSONBytes = 32 * 1024

// ---------------------------------------------------------------------------
// Row types — one struct per table. All use repo.Repo[T] tag conventions.
// ---------------------------------------------------------------------------

// Slot is one row in character_slots.
type Slot struct {
	ID                int64     `db:"id"                  pk:"auto" json:"id"`
	Key               string    `db:"key"                            json:"key"`
	Label             string    `db:"label"                          json:"label"`
	Required          bool      `db:"required"                       json:"required"`
	OrderIndex        int32     `db:"order_index"                    json:"order_index"`
	DefaultLayerOrder int32     `db:"default_layer_order"            json:"default_layer_order"`
	AllowsPalette     bool      `db:"allows_palette"                 json:"allows_palette"`
	CreatedBy         *int64    `db:"created_by"                     json:"created_by,omitempty"`
	CreatedAt         time.Time `db:"created_at"  repo:"readonly"    json:"created_at"`
	UpdatedAt         time.Time `db:"updated_at"  repo:"readonly"    json:"updated_at"`
}

// Validate enforces structural invariants on a Slot. Doesn't touch the DB.
func (s Slot) Validate() error {
	if err := validateSlotKey(s.Key); err != nil {
		return err
	}
	if strings.TrimSpace(s.Label) == "" {
		return errors.New("characters: slot label is required")
	}
	if len(s.Label) > MaxNameLen {
		return fmt.Errorf("characters: slot label exceeds %d chars", MaxNameLen)
	}
	return nil
}

// validateSlotKey enforces the same character set we accept for HUD
// binding keys: lowercase alnum + underscore, non-empty, ≤ 32 chars.
// Slot keys are baked into recipe JSON so they need to be stable and
// URL-safe.
func validateSlotKey(key string) error {
	if key == "" {
		return errors.New("characters: slot key is required")
	}
	if len(key) > 32 {
		return errors.New("characters: slot key exceeds 32 chars")
	}
	for _, c := range key {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return fmt.Errorf("characters: slot key %q must be lowercase a-z, 0-9, or _", key)
		}
	}
	return nil
}

// Part is one row in character_parts.
type Part struct {
	ID                 int64           `db:"id"                    pk:"auto" json:"id"`
	SlotID             int64           `db:"slot_id"                         json:"slot_id"`
	AssetID            int64           `db:"asset_id"                        json:"asset_id"`
	Name               string          `db:"name"                            json:"name"`
	Tags               []string        `db:"tags"                            json:"tags"`
	CompatibleTags     []string        `db:"compatible_tags"                 json:"compatible_tags"`
	LayerOrder         *int32          `db:"layer_order"                     json:"layer_order,omitempty"`
	FrameMapJSON       json.RawMessage `db:"frame_map_json"                  json:"frame_map"`
	PaletteRegionsJSON json.RawMessage `db:"palette_regions_json"            json:"palette_regions,omitempty"`
	CreatedBy          int64           `db:"created_by"                      json:"created_by"`
	CreatedAt          time.Time       `db:"created_at"  repo:"readonly"     json:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"  repo:"readonly"     json:"updated_at"`
}

// Validate enforces structural invariants on a Part.
func (p Part) Validate() error {
	if p.SlotID <= 0 {
		return errors.New("characters: part slot_id is required")
	}
	if p.AssetID <= 0 {
		return errors.New("characters: part asset_id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("characters: part name is required")
	}
	if len(p.Name) > MaxNameLen {
		return fmt.Errorf("characters: part name exceeds %d chars", MaxNameLen)
	}
	for _, t := range p.Tags {
		if strings.TrimSpace(t) == "" {
			return errors.New("characters: part tags must not contain empty strings")
		}
	}
	for _, t := range p.CompatibleTags {
		if strings.TrimSpace(t) == "" {
			return errors.New("characters: part compatible_tags must not contain empty strings")
		}
	}
	// FrameMapJSON shape is validated in detail by parts.go (canonical
	// animation contract). Here we only require it parses as JSON.
	if len(p.FrameMapJSON) > 0 {
		var probe any
		if err := json.Unmarshal(p.FrameMapJSON, &probe); err != nil {
			return fmt.Errorf("characters: part frame_map_json is not valid JSON: %w", err)
		}
	}
	return nil
}

// Recipe is one row in character_recipes.
type Recipe struct {
	ID             int64           `db:"id"                    pk:"auto" json:"id"`
	OwnerKind      OwnerKind       `db:"owner_kind"                      json:"owner_kind"`
	OwnerID        int64           `db:"owner_id"                        json:"owner_id"`
	Name           string          `db:"name"                            json:"name"`
	AppearanceJSON json.RawMessage `db:"appearance_json"                 json:"appearance"`
	StatsJSON      json.RawMessage `db:"stats_json"                      json:"stats"`
	TalentsJSON    json.RawMessage `db:"talents_json"                    json:"talents"`
	RecipeHash     []byte          `db:"recipe_hash"                     json:"recipe_hash"`
	CreatedBy      int64           `db:"created_by"                      json:"created_by"`
	CreatedAt      time.Time       `db:"created_at"  repo:"readonly"     json:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"  repo:"readonly"     json:"updated_at"`
}

// Validate enforces shape on a Recipe row. The richer canonical-form
// validation lives in recipes.go (Phase 2).
func (r Recipe) Validate() error {
	if err := r.OwnerKind.Validate(); err != nil {
		return err
	}
	if r.OwnerID <= 0 {
		return errors.New("characters: recipe owner_id is required")
	}
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("characters: recipe name is required")
	}
	if len(r.Name) > MaxNameLen {
		return fmt.Errorf("characters: recipe name exceeds %d chars", MaxNameLen)
	}
	if len(r.RecipeHash) == 0 {
		return errors.New("characters: recipe_hash is required")
	}
	if len(r.AppearanceJSON)+len(r.StatsJSON)+len(r.TalentsJSON) > MaxRecipeJSONBytes {
		return fmt.Errorf("characters: recipe payload exceeds %d bytes", MaxRecipeJSONBytes)
	}
	return nil
}

// Bake is one row in character_bakes.
type Bake struct {
	ID            int64      `db:"id"                  pk:"auto" json:"id"`
	RecipeID      int64      `db:"recipe_id"                     json:"recipe_id"`
	RecipeHash    []byte     `db:"recipe_hash"                   json:"recipe_hash"`
	AssetID       *int64     `db:"asset_id"                      json:"asset_id,omitempty"`
	Status        BakeStatus `db:"status"                        json:"status"`
	FailureReason string     `db:"failure_reason"                json:"failure_reason"`
	BakedAt       *time.Time `db:"baked_at"                      json:"baked_at,omitempty"`
	CreatedAt     time.Time  `db:"created_at"  repo:"readonly"   json:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"  repo:"readonly"   json:"updated_at"`
}

// Validate enforces structural invariants on a Bake row.
func (b Bake) Validate() error {
	if b.RecipeID <= 0 {
		return errors.New("characters: bake recipe_id is required")
	}
	if len(b.RecipeHash) == 0 {
		return errors.New("characters: bake recipe_hash is required")
	}
	if err := b.Status.Validate(); err != nil {
		return err
	}
	if b.Status == BakeStatusBaked && (b.AssetID == nil || *b.AssetID <= 0) {
		return errors.New("characters: baked bakes must reference an asset_id")
	}
	return nil
}

// StatSet is one row in character_stat_sets.
type StatSet struct {
	ID                int64           `db:"id"                       pk:"auto" json:"id"`
	Key               string          `db:"key"                                json:"key"`
	Name              string          `db:"name"                               json:"name"`
	StatsJSON         json.RawMessage `db:"stats_json"                         json:"stats"`
	CreationRulesJSON json.RawMessage `db:"creation_rules_json"                json:"creation_rules"`
	CreatedBy         int64           `db:"created_by"                         json:"created_by"`
	CreatedAt         time.Time       `db:"created_at"  repo:"readonly"        json:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"  repo:"readonly"        json:"updated_at"`
}

// Validate enforces shape on a StatSet row. Stat-formula validation is
// in stats.go (Phase 3).
func (s StatSet) Validate() error {
	if err := validateSlotKey(s.Key); err != nil {
		return fmt.Errorf("stat_set: %w", err)
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("characters: stat_set name is required")
	}
	if len(s.Name) > MaxNameLen {
		return fmt.Errorf("characters: stat_set name exceeds %d chars", MaxNameLen)
	}
	return nil
}

// TalentTree is one row in character_talent_trees.
type TalentTree struct {
	ID          int64            `db:"id"                  pk:"auto" json:"id"`
	Key         string           `db:"key"                           json:"key"`
	Name        string           `db:"name"                          json:"name"`
	Description string           `db:"description"                   json:"description"`
	CurrencyKey string           `db:"currency_key"                  json:"currency_key"`
	LayoutMode  TalentLayoutMode `db:"layout_mode"                   json:"layout_mode"`
	CreatedBy   int64            `db:"created_by"                    json:"created_by"`
	CreatedAt   time.Time        `db:"created_at"  repo:"readonly"   json:"created_at"`
	UpdatedAt   time.Time        `db:"updated_at"  repo:"readonly"   json:"updated_at"`
}

// Validate enforces shape on a TalentTree row.
func (t TalentTree) Validate() error {
	if err := validateSlotKey(t.Key); err != nil {
		return fmt.Errorf("talent_tree: %w", err)
	}
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("characters: talent_tree name is required")
	}
	if len(t.Name) > MaxNameLen {
		return fmt.Errorf("characters: talent_tree name exceeds %d chars", MaxNameLen)
	}
	if err := t.LayoutMode.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(t.CurrencyKey) == "" {
		return errors.New("characters: talent_tree currency_key is required")
	}
	if err := validateSlotKey(t.CurrencyKey); err != nil {
		return fmt.Errorf("talent_tree currency_key: %w", err)
	}
	return nil
}

// TalentNode is one row in character_talent_nodes.
type TalentNode struct {
	ID                int64           `db:"id"                  pk:"auto" json:"id"`
	TreeID            int64           `db:"tree_id"                       json:"tree_id"`
	Key               string          `db:"key"                           json:"key"`
	Name              string          `db:"name"                          json:"name"`
	Description       string          `db:"description"                   json:"description"`
	IconAssetID       *int64          `db:"icon_asset_id"                 json:"icon_asset_id,omitempty"`
	MaxRank           int32           `db:"max_rank"                      json:"max_rank"`
	CostJSON          json.RawMessage `db:"cost_json"                     json:"cost"`
	PrerequisitesJSON json.RawMessage `db:"prerequisites_json"            json:"prerequisites"`
	EffectJSON        json.RawMessage `db:"effect_json"                   json:"effect"`
	LayoutJSON        json.RawMessage `db:"layout_json"                   json:"layout,omitempty"`
	MutexGroup        string          `db:"mutex_group"                   json:"mutex_group"`
	CreatedAt         time.Time       `db:"created_at"  repo:"readonly"   json:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"  repo:"readonly"   json:"updated_at"`
}

// Validate enforces shape on a TalentNode row.
func (n TalentNode) Validate() error {
	if n.TreeID <= 0 {
		return errors.New("characters: talent_node tree_id is required")
	}
	if err := validateSlotKey(n.Key); err != nil {
		return fmt.Errorf("talent_node: %w", err)
	}
	if strings.TrimSpace(n.Name) == "" {
		return errors.New("characters: talent_node name is required")
	}
	if len(n.Name) > MaxNameLen {
		return fmt.Errorf("characters: talent_node name exceeds %d chars", MaxNameLen)
	}
	if n.MaxRank < 1 {
		return errors.New("characters: talent_node max_rank must be >= 1")
	}
	// MutexGroup may be empty (no group); when set, follow key rules.
	if n.MutexGroup != "" {
		if err := validateSlotKey(n.MutexGroup); err != nil {
			return fmt.Errorf("talent_node mutex_group: %w", err)
		}
	}
	return nil
}

// NpcTemplate is one row in npc_templates.
type NpcTemplate struct {
	ID           int64     `db:"id"                  pk:"auto" json:"id"`
	Name         string    `db:"name"                          json:"name"`
	RecipeID     *int64    `db:"recipe_id"                     json:"recipe_id,omitempty"`
	ActiveBakeID *int64    `db:"active_bake_id"                json:"active_bake_id,omitempty"`
	EntityTypeID *int64    `db:"entity_type_id"                json:"entity_type_id,omitempty"`
	Tags         []string  `db:"tags"                          json:"tags"`
	CreatedBy    int64     `db:"created_by"                    json:"created_by"`
	CreatedAt    time.Time `db:"created_at"  repo:"readonly"   json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"  repo:"readonly"   json:"updated_at"`
}

// Validate enforces shape on a NpcTemplate row.
func (n NpcTemplate) Validate() error {
	if strings.TrimSpace(n.Name) == "" {
		return errors.New("characters: npc_template name is required")
	}
	if len(n.Name) > MaxNameLen {
		return fmt.Errorf("characters: npc_template name exceeds %d chars", MaxNameLen)
	}
	for _, t := range n.Tags {
		if strings.TrimSpace(t) == "" {
			return errors.New("characters: npc_template tags must not contain empty strings")
		}
	}
	return nil
}

// PlayerCharacter is one row in player_characters.
type PlayerCharacter struct {
	ID            int64     `db:"id"                  pk:"auto" json:"id"`
	PlayerID      int64     `db:"player_id"                     json:"player_id"`
	RecipeID      *int64    `db:"recipe_id"                     json:"recipe_id,omitempty"`
	ActiveBakeID  *int64    `db:"active_bake_id"                json:"active_bake_id,omitempty"`
	Name          string    `db:"name"                          json:"name"`
	PublicBio     string    `db:"public_bio"                    json:"public_bio"`
	PrivateNotes  string    `db:"private_notes"                 json:"private_notes"`
	CreatedAt     time.Time `db:"created_at"  repo:"readonly"   json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"  repo:"readonly"   json:"updated_at"`
}

// Validate enforces shape on a PlayerCharacter row.
func (p PlayerCharacter) Validate() error {
	if p.PlayerID <= 0 {
		return errors.New("characters: player_character player_id is required")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("characters: player_character name is required")
	}
	if len(p.Name) > MaxNameLen {
		return fmt.Errorf("characters: player_character name exceeds %d chars", MaxNameLen)
	}
	if len(p.PublicBio) > MaxBioLen {
		return fmt.Errorf("characters: public_bio exceeds %d chars", MaxBioLen)
	}
	if len(p.PrivateNotes) > MaxBioLen {
		return fmt.Errorf("characters: private_notes exceeds %d chars", MaxBioLen)
	}
	return nil
}
