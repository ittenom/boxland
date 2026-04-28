// Boxland — characters: artifact.Handler implementations.
//
// Five handlers, one per designer-managed artifact kind:
//
//   KindCharacterSlot       — SlotHandler
//   KindCharacterPart       — PartHandler
//   KindCharacterStatSet    — StatSetHandler
//   KindCharacterTalentTree — TalentTreeHandler
//   KindNpcTemplate         — NpcTemplateHandler
//
// Each handler follows the entities/artifact.go pattern: a Draft struct
// (editable surface, distinct from the row type), a Validate method, a
// Publish method that updates the existing live row inside the supplied
// pgx.Tx and returns a configurable.DiffJSON delta.
//
// Phase 1 scope: NpcTemplateHandler.Publish only writes the row's
// metadata (name, tags, recipe_id, active_bake_id, entity_type_id) —
// the bake-on-publish path lands in Phase 2.

package characters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/configurable"
	"boxland/server/internal/publishing/artifact"
)

// ---------------------------------------------------------------------------
// Slot
// ---------------------------------------------------------------------------

// SlotDraft is the editable surface of a Slot.
type SlotDraft struct {
	Key               string `json:"key"`
	Label             string `json:"label"`
	Required          bool   `json:"required"`
	OrderIndex        int32  `json:"order_index"`
	DefaultLayerOrder int32  `json:"default_layer_order"`
	AllowsPalette     bool   `json:"allows_palette"`
}

// Validate enforces structural invariants. The same key/label rules as
// the row type so designer drafts get the same treatment.
func (d SlotDraft) Validate() error {
	probe := Slot{
		Key: d.Key, Label: d.Label, Required: d.Required,
		OrderIndex: d.OrderIndex, DefaultLayerOrder: d.DefaultLayerOrder,
		AllowsPalette: d.AllowsPalette,
	}
	return probe.Validate()
}

// Descriptor drives the generic form renderer.
func (SlotDraft) Descriptor() []configurable.FieldDescriptor {
	zero := 0.0
	return []configurable.FieldDescriptor{
		{Key: "key", Label: "Key", Kind: configurable.KindString, Required: true, MaxLen: 32,
			Help: "Stable id used in recipe JSON. Lowercase a-z, 0-9, _."},
		{Key: "label", Label: "Label", Kind: configurable.KindString, Required: true, MaxLen: MaxNameLen},
		{Key: "required", Label: "Required", Kind: configurable.KindBool,
			Help: "Recipes without this slot fail validation."},
		{Key: "order_index", Label: "Order index", Kind: configurable.KindInt, Min: &zero},
		{Key: "default_layer_order", Label: "Default layer order", Kind: configurable.KindInt, Min: &zero,
			Help: "Lower draws first. Parts may override per-part."},
		{Key: "allows_palette", Label: "Allows palette", Kind: configurable.KindBool},
	}
}

// SlotHandler implements artifact.Handler for character slots.
type SlotHandler struct{ Svc *Service }

// NewSlotHandler constructs a SlotHandler.
func NewSlotHandler(svc *Service) *SlotHandler { return &SlotHandler{Svc: svc} }

func (*SlotHandler) Kind() artifact.Kind { return artifact.KindCharacterSlot }

func (h *SlotHandler) Validate(_ context.Context, d artifact.DraftRow) error {
	var sd SlotDraft
	if err := json.Unmarshal(d.DraftJSON, &sd); err != nil {
		return fmt.Errorf("character_slot draft: bad json: %w", err)
	}
	return sd.Validate()
}

func (h *SlotHandler) Publish(ctx context.Context, tx pgx.Tx, d artifact.DraftRow) (artifact.PublishResult, error) {
	var sd SlotDraft
	if err := json.Unmarshal(d.DraftJSON, &sd); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("character_slot draft: bad json: %w", err)
	}
	prev, err := h.loadPrev(ctx, tx, d.ArtifactID)
	if err != nil {
		return artifact.PublishResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE character_slots SET
			key = $2, label = $3, required = $4, order_index = $5,
			default_layer_order = $6, allows_palette = $7, updated_at = now()
		WHERE id = $1
	`, d.ArtifactID, sd.Key, sd.Label, sd.Required, sd.OrderIndex,
		sd.DefaultLayerOrder, sd.AllowsPalette); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply character_slot update: %w", err)
	}
	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: configurable.DiffJSON(prev, sd)}, nil
}

func (h *SlotHandler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (SlotDraft, error) {
	var p SlotDraft
	err := tx.QueryRow(ctx, `
		SELECT key, label, required, order_index, default_layer_order, allows_palette
		FROM character_slots WHERE id = $1
	`, id).Scan(&p.Key, &p.Label, &p.Required, &p.OrderIndex, &p.DefaultLayerOrder, &p.AllowsPalette)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, fmt.Errorf("character_slot %d: %w", id, ErrSlotNotFound)
		}
		return p, err
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Part
// ---------------------------------------------------------------------------

// PartDraft is the editable surface of a Part.
type PartDraft struct {
	SlotID             int64           `json:"slot_id"`
	AssetID            int64           `json:"asset_id"`
	Name               string          `json:"name"`
	Tags               []string        `json:"tags"`
	CompatibleTags     []string        `json:"compatible_tags"`
	LayerOrder         *int32          `json:"layer_order,omitempty"`
	FrameMapJSON       json.RawMessage `json:"frame_map"`
	PaletteRegionsJSON json.RawMessage `json:"palette_regions,omitempty"`
}

// Validate enforces structural invariants on the draft surface.
func (d PartDraft) Validate() error {
	probe := Part{
		SlotID: d.SlotID, AssetID: d.AssetID, Name: d.Name,
		Tags: d.Tags, CompatibleTags: d.CompatibleTags,
		LayerOrder: d.LayerOrder, FrameMapJSON: d.FrameMapJSON,
		PaletteRegionsJSON: d.PaletteRegionsJSON,
	}
	return probe.Validate()
}

// Descriptor drives the generic form renderer.
func (PartDraft) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "slot_id", Label: "Slot", Kind: configurable.KindInt, Required: true,
			Help: "Slot id this part belongs to."},
		{Key: "asset_id", Label: "Sprite asset", Kind: configurable.KindAssetRef, Required: true,
			RefTags: []string{"sprite"}},
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: MaxNameLen},
		{Key: "tags", Label: "Tags", Kind: configurable.KindList, Children: []configurable.FieldDescriptor{
			{Key: "tag", Label: "Tag", Kind: configurable.KindString, MaxLen: 32},
		}},
		{Key: "compatible_tags", Label: "Compatible tags", Kind: configurable.KindList,
			Help: "Recipes only allow this part beside parts whose tags include any of these.",
			Children: []configurable.FieldDescriptor{
				{Key: "tag", Label: "Tag", Kind: configurable.KindString, MaxLen: 32},
			}},
		{Key: "layer_order", Label: "Layer order (override)", Kind: configurable.KindInt,
			Help: "Leave blank to inherit slot's default layer order."},
	}
}

// PartHandler implements artifact.Handler for character parts.
type PartHandler struct{ Svc *Service }

// NewPartHandler constructs a PartHandler.
func NewPartHandler(svc *Service) *PartHandler { return &PartHandler{Svc: svc} }

func (*PartHandler) Kind() artifact.Kind { return artifact.KindCharacterPart }

func (h *PartHandler) Validate(_ context.Context, d artifact.DraftRow) error {
	var pd PartDraft
	if err := json.Unmarshal(d.DraftJSON, &pd); err != nil {
		return fmt.Errorf("character_part draft: bad json: %w", err)
	}
	return pd.Validate()
}

func (h *PartHandler) Publish(ctx context.Context, tx pgx.Tx, d artifact.DraftRow) (artifact.PublishResult, error) {
	var pd PartDraft
	if err := json.Unmarshal(d.DraftJSON, &pd); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("character_part draft: bad json: %w", err)
	}
	prev, err := h.loadPrev(ctx, tx, d.ArtifactID)
	if err != nil {
		return artifact.PublishResult{}, err
	}
	tags := valOrEmpty(pd.Tags)
	compatTags := valOrEmpty(pd.CompatibleTags)
	frameMap := pd.FrameMapJSON
	if len(frameMap) == 0 {
		frameMap = json.RawMessage(`{}`)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE character_parts SET
			slot_id = $2, asset_id = $3, name = $4, tags = $5, compatible_tags = $6,
			layer_order = $7, frame_map_json = $8, palette_regions_json = $9, updated_at = now()
		WHERE id = $1
	`, d.ArtifactID, pd.SlotID, pd.AssetID, pd.Name, tags, compatTags,
		pd.LayerOrder, frameMap, pd.PaletteRegionsJSON); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply character_part update: %w", err)
	}
	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: configurable.DiffJSON(prev, pd)}, nil
}

func (h *PartHandler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (PartDraft, error) {
	var p PartDraft
	err := tx.QueryRow(ctx, `
		SELECT slot_id, asset_id, name, tags, compatible_tags,
		       layer_order, frame_map_json, palette_regions_json
		FROM character_parts WHERE id = $1
	`, id).Scan(&p.SlotID, &p.AssetID, &p.Name, &p.Tags, &p.CompatibleTags,
		&p.LayerOrder, &p.FrameMapJSON, &p.PaletteRegionsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, fmt.Errorf("character_part %d: %w", id, ErrPartNotFound)
		}
		return p, err
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	if p.CompatibleTags == nil {
		p.CompatibleTags = []string{}
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Stat set
// ---------------------------------------------------------------------------

// StatSetDraft is the editable surface of a StatSet.
type StatSetDraft struct {
	Key               string          `json:"key"`
	Name              string          `json:"name"`
	StatsJSON         json.RawMessage `json:"stats"`
	CreationRulesJSON json.RawMessage `json:"creation_rules"`
}

// Validate enforces structural invariants.
func (d StatSetDraft) Validate() error {
	probe := StatSet{Key: d.Key, Name: d.Name}
	return probe.Validate()
	// stats_json / creation_rules_json shape validation lives in
	// stats.go (Phase 3); here we only enforce the row-level shape.
}

// Descriptor drives the generic form renderer.
func (StatSetDraft) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "key", Label: "Key", Kind: configurable.KindString, Required: true, MaxLen: 32},
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: MaxNameLen},
	}
}

// StatSetHandler implements artifact.Handler for stat sets.
type StatSetHandler struct{ Svc *Service }

// NewStatSetHandler constructs a StatSetHandler.
func NewStatSetHandler(svc *Service) *StatSetHandler { return &StatSetHandler{Svc: svc} }

func (*StatSetHandler) Kind() artifact.Kind { return artifact.KindCharacterStatSet }

func (h *StatSetHandler) Validate(_ context.Context, d artifact.DraftRow) error {
	var sd StatSetDraft
	if err := json.Unmarshal(d.DraftJSON, &sd); err != nil {
		return fmt.Errorf("character_stat_set draft: bad json: %w", err)
	}
	return sd.Validate()
}

func (h *StatSetHandler) Publish(ctx context.Context, tx pgx.Tx, d artifact.DraftRow) (artifact.PublishResult, error) {
	var sd StatSetDraft
	if err := json.Unmarshal(d.DraftJSON, &sd); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("character_stat_set draft: bad json: %w", err)
	}
	prev, err := h.loadPrev(ctx, tx, d.ArtifactID)
	if err != nil {
		return artifact.PublishResult{}, err
	}
	stats := sd.StatsJSON
	if len(stats) == 0 {
		stats = json.RawMessage(`[]`)
	}
	rules := sd.CreationRulesJSON
	if len(rules) == 0 {
		rules = json.RawMessage(`{}`)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE character_stat_sets SET
			key = $2, name = $3, stats_json = $4, creation_rules_json = $5, updated_at = now()
		WHERE id = $1
	`, d.ArtifactID, sd.Key, sd.Name, stats, rules); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply character_stat_set update: %w", err)
	}
	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: configurable.DiffJSON(prev, sd)}, nil
}

func (h *StatSetHandler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (StatSetDraft, error) {
	var p StatSetDraft
	err := tx.QueryRow(ctx, `
		SELECT key, name, stats_json, creation_rules_json
		FROM character_stat_sets WHERE id = $1
	`, id).Scan(&p.Key, &p.Name, &p.StatsJSON, &p.CreationRulesJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, fmt.Errorf("character_stat_set %d: %w", id, ErrStatSetNotFound)
		}
		return p, err
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Talent tree (header only — nodes ride along inside Draft.Nodes)
// ---------------------------------------------------------------------------

// TalentTreeDraft is the editable surface of a TalentTree, including its
// nodes inline so the publish handler can replace the node set in one
// transaction. (One artifact = one tree + its nodes, conceptually.)
type TalentTreeDraft struct {
	Key         string             `json:"key"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	CurrencyKey string             `json:"currency_key"`
	LayoutMode  TalentLayoutMode   `json:"layout_mode"`
	Nodes       []TalentNodeDraft  `json:"nodes"`
}

// TalentNodeDraft is the editable surface of a TalentNode.
type TalentNodeDraft struct {
	Key               string          `json:"key"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	IconAssetID       *int64          `json:"icon_asset_id,omitempty"`
	MaxRank           int32           `json:"max_rank"`
	CostJSON          json.RawMessage `json:"cost"`
	PrerequisitesJSON json.RawMessage `json:"prerequisites"`
	EffectJSON        json.RawMessage `json:"effect"`
	LayoutJSON        json.RawMessage `json:"layout,omitempty"`
	MutexGroup        string          `json:"mutex_group"`
}

// Validate runs row-level validation on the tree and every node.
func (d TalentTreeDraft) Validate() error {
	probe := TalentTree{
		Key: d.Key, Name: d.Name, Description: d.Description,
		CurrencyKey: d.CurrencyKey, LayoutMode: d.LayoutMode,
	}
	if err := probe.Validate(); err != nil {
		return err
	}
	keys := make(map[string]struct{}, len(d.Nodes))
	for i, n := range d.Nodes {
		// TreeID = -1 sentinel: we don't yet know the tree id at draft
		// time, so the row-level Validate would reject it. Use a
		// stand-in positive value to satisfy the >0 check.
		probeNode := TalentNode{
			TreeID: 1, Key: n.Key, Name: n.Name, Description: n.Description,
			IconAssetID: n.IconAssetID, MaxRank: n.MaxRank,
			CostJSON: n.CostJSON, PrerequisitesJSON: n.PrerequisitesJSON,
			EffectJSON: n.EffectJSON, LayoutJSON: n.LayoutJSON,
			MutexGroup: n.MutexGroup,
		}
		if err := probeNode.Validate(); err != nil {
			return fmt.Errorf("nodes[%d]: %w", i, err)
		}
		if _, dup := keys[n.Key]; dup {
			return fmt.Errorf("characters: talent_tree node key %q appears twice", n.Key)
		}
		keys[n.Key] = struct{}{}
	}
	return nil
}

// Descriptor drives the generic form renderer (header only; nodes are
// edited via a per-node sub-form, matching how entity_components works).
func (TalentTreeDraft) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "key", Label: "Key", Kind: configurable.KindString, Required: true, MaxLen: 32},
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: MaxNameLen},
		{Key: "description", Label: "Description", Kind: configurable.KindMultilineText, MaxLen: 2048},
		{Key: "currency_key", Label: "Currency stat key", Kind: configurable.KindString, Required: true, MaxLen: 32,
			Help: "References a stat in the linked stat set with kind=resource."},
		{Key: "layout_mode", Label: "Layout", Kind: configurable.KindEnum,
			Options: []configurable.EnumOption{
				{Value: "tree", Label: "Tree"},
				{Value: "tiered", Label: "Tiered list"},
				{Value: "free_list", Label: "Free pick list"},
				{Value: "web", Label: "Web"},
			}},
	}
}

// TalentTreeHandler implements artifact.Handler for talent trees.
type TalentTreeHandler struct{ Svc *Service }

// NewTalentTreeHandler constructs a TalentTreeHandler.
func NewTalentTreeHandler(svc *Service) *TalentTreeHandler { return &TalentTreeHandler{Svc: svc} }

func (*TalentTreeHandler) Kind() artifact.Kind { return artifact.KindCharacterTalentTree }

func (h *TalentTreeHandler) Validate(_ context.Context, d artifact.DraftRow) error {
	var td TalentTreeDraft
	if err := json.Unmarshal(d.DraftJSON, &td); err != nil {
		return fmt.Errorf("character_talent_tree draft: bad json: %w", err)
	}
	return td.Validate()
}

func (h *TalentTreeHandler) Publish(ctx context.Context, tx pgx.Tx, d artifact.DraftRow) (artifact.PublishResult, error) {
	var td TalentTreeDraft
	if err := json.Unmarshal(d.DraftJSON, &td); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("character_talent_tree draft: bad json: %w", err)
	}
	prev, err := h.loadPrev(ctx, tx, d.ArtifactID)
	if err != nil {
		return artifact.PublishResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE character_talent_trees SET
			key = $2, name = $3, description = $4, currency_key = $5,
			layout_mode = $6, updated_at = now()
		WHERE id = $1
	`, d.ArtifactID, td.Key, td.Name, td.Description, td.CurrencyKey, string(td.LayoutMode)); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply character_talent_tree update: %w", err)
	}
	if err := h.replaceNodes(ctx, tx, d.ArtifactID, td.Nodes); err != nil {
		return artifact.PublishResult{}, err
	}
	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: configurable.DiffJSON(prev, td)}, nil
}

// replaceNodes wipes existing nodes and re-inserts the draft set. Nodes
// belong wholly to their tree (FK has ON DELETE CASCADE), so wholesale
// replacement is the cleanest semantics for a tree publish.
func (h *TalentTreeHandler) replaceNodes(ctx context.Context, tx pgx.Tx, treeID int64, nodes []TalentNodeDraft) error {
	if _, err := tx.Exec(ctx, `DELETE FROM character_talent_nodes WHERE tree_id = $1`, treeID); err != nil {
		return fmt.Errorf("delete old nodes: %w", err)
	}
	for i, n := range nodes {
		cost := n.CostJSON
		if len(cost) == 0 {
			cost = json.RawMessage(`{}`)
		}
		prereqs := n.PrerequisitesJSON
		if len(prereqs) == 0 {
			prereqs = json.RawMessage(`[]`)
		}
		effect := n.EffectJSON
		if len(effect) == 0 {
			effect = json.RawMessage(`[]`)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO character_talent_nodes
				(tree_id, key, name, description, icon_asset_id, max_rank,
				 cost_json, prerequisites_json, effect_json, layout_json, mutex_group)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, treeID, n.Key, n.Name, n.Description, n.IconAssetID, n.MaxRank,
			cost, prereqs, effect, n.LayoutJSON, n.MutexGroup); err != nil {
			return fmt.Errorf("insert node[%d] %q: %w", i, n.Key, err)
		}
	}
	return nil
}

func (h *TalentTreeHandler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (TalentTreeDraft, error) {
	var p TalentTreeDraft
	var layout string
	err := tx.QueryRow(ctx, `
		SELECT key, name, description, currency_key, layout_mode
		FROM character_talent_trees WHERE id = $1
	`, id).Scan(&p.Key, &p.Name, &p.Description, &p.CurrencyKey, &layout)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, fmt.Errorf("character_talent_tree %d: %w", id, ErrTalentTreeNotFound)
		}
		return p, err
	}
	p.LayoutMode = TalentLayoutMode(layout)

	rows, err := tx.Query(ctx, `
		SELECT key, name, description, icon_asset_id, max_rank,
		       cost_json, prerequisites_json, effect_json, layout_json, mutex_group
		FROM character_talent_nodes WHERE tree_id = $1
		ORDER BY key ASC
	`, id)
	if err != nil {
		return p, fmt.Errorf("load nodes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n TalentNodeDraft
		if err := rows.Scan(&n.Key, &n.Name, &n.Description, &n.IconAssetID, &n.MaxRank,
			&n.CostJSON, &n.PrerequisitesJSON, &n.EffectJSON, &n.LayoutJSON, &n.MutexGroup); err != nil {
			return p, fmt.Errorf("scan node: %w", err)
		}
		p.Nodes = append(p.Nodes, n)
	}
	return p, rows.Err()
}

// ---------------------------------------------------------------------------
// NPC template
// ---------------------------------------------------------------------------

// NpcTemplateDraft is the editable surface of an NpcTemplate.
//
// Phase 1: this draft pins identity (name, tags, recipe_id, bake link,
// entity_type link) but does not run a bake. The bake-on-publish path
// (Phase 2) lives in characters/bake.go and is invoked from this handler.
type NpcTemplateDraft struct {
	Name         string   `json:"name"`
	Tags         []string `json:"tags"`
	RecipeID     *int64   `json:"recipe_id,omitempty"`
	ActiveBakeID *int64   `json:"active_bake_id,omitempty"`
	EntityTypeID *int64   `json:"entity_type_id,omitempty"`
}

// Validate enforces structural invariants.
func (d NpcTemplateDraft) Validate() error {
	probe := NpcTemplate{
		Name: d.Name, Tags: d.Tags,
		RecipeID: d.RecipeID, ActiveBakeID: d.ActiveBakeID, EntityTypeID: d.EntityTypeID,
	}
	return probe.Validate()
}

// Descriptor drives the generic form renderer.
func (NpcTemplateDraft) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: MaxNameLen},
		{Key: "tags", Label: "Tags", Kind: configurable.KindList, Children: []configurable.FieldDescriptor{
			{Key: "tag", Label: "Tag", Kind: configurable.KindString, MaxLen: 32},
		}},
		{Key: "recipe_id", Label: "Recipe", Kind: configurable.KindInt,
			Help: "Pinned by the Character Generator after save."},
		{Key: "entity_type_id", Label: "Entity type", Kind: configurable.KindEntityTypeRef,
			Help: "Auto-minted on first publish if blank."},
	}
}

// NpcTemplateHandler implements artifact.Handler for NPC templates.
type NpcTemplateHandler struct{ Svc *Service }

// NewNpcTemplateHandler constructs a NpcTemplateHandler.
func NewNpcTemplateHandler(svc *Service) *NpcTemplateHandler { return &NpcTemplateHandler{Svc: svc} }

func (*NpcTemplateHandler) Kind() artifact.Kind { return artifact.KindNpcTemplate }

func (h *NpcTemplateHandler) Validate(_ context.Context, d artifact.DraftRow) error {
	var nd NpcTemplateDraft
	if err := json.Unmarshal(d.DraftJSON, &nd); err != nil {
		return fmt.Errorf("npc_template draft: bad json: %w", err)
	}
	return nd.Validate()
}

func (h *NpcTemplateHandler) Publish(ctx context.Context, tx pgx.Tx, d artifact.DraftRow) (artifact.PublishResult, error) {
	var nd NpcTemplateDraft
	if err := json.Unmarshal(d.DraftJSON, &nd); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("npc draft: bad json: %w", err)
	}
	prev, err := h.loadPrev(ctx, tx, d.ArtifactID)
	if err != nil {
		return artifact.PublishResult{}, err
	}

	// Bake-on-publish. If the draft has a recipe attached and the bake
	// deps are wired, we materialize the recipe + run the composer +
	// upsert the asset row + persist the bake row, all inside this
	// publish tx. The bake's asset_id becomes the NPC's
	// sprite_asset_id; the bake's row id becomes active_bake_id.
	bakeID := nd.ActiveBakeID
	bakedAssetID := (*int64)(nil)
	if nd.RecipeID != nil && h.Svc != nil && h.Svc.Store != nil && h.Svc.Assets != nil {
		recipe, err := LoadBakeRecipe(ctx, tx, *nd.RecipeID)
		if err != nil {
			return artifact.PublishResult{}, fmt.Errorf("npc publish: load recipe: %w", err)
		}
		if err := validateRecipeStatsAndTalents(ctx, tx, recipe); err != nil {
			return artifact.PublishResult{}, fmt.Errorf("npc publish: recipe validation: %w", err)
		}
		out, err := RunBake(ctx, tx, BakeDeps{Store: h.Svc.Store, Assets: h.Svc.Assets}, recipe, *nd.RecipeID)
		if err != nil {
			return artifact.PublishResult{}, fmt.Errorf("npc publish: bake: %w", err)
		}
		id := out.BakeID
		bakeID = &id
		assetID := out.AssetID
		bakedAssetID = &assetID
	}

	// Per the holistic redesign, the NPC template IS the entity_type.
	// d.ArtifactID is both the draft's artifact id and the
	// entity_types row id. No auto-mint dance, no separate template
	// table to keep in sync.
	tags := valOrEmpty(nd.Tags)
	if bakedAssetID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE entity_types SET
				name = $2, tags = $3, recipe_id = $4, active_bake_id = $5,
				sprite_asset_id = $6, updated_at = now()
			WHERE id = $1 AND entity_class = 'npc'
		`, d.ArtifactID, nd.Name, tags, nd.RecipeID, bakeID, *bakedAssetID); err != nil {
			return artifact.PublishResult{}, fmt.Errorf("apply npc update: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE entity_types SET
				name = $2, tags = $3, recipe_id = $4, active_bake_id = $5,
				updated_at = now()
			WHERE id = $1 AND entity_class = 'npc'
		`, d.ArtifactID, nd.Name, tags, nd.RecipeID, bakeID); err != nil {
			return artifact.PublishResult{}, fmt.Errorf("apply npc update: %w", err)
		}
	}

	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: configurable.DiffJSON(prev, nd)}, nil
}

func (h *NpcTemplateHandler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (NpcTemplateDraft, error) {
	var p NpcTemplateDraft
	err := tx.QueryRow(ctx, `
		SELECT name, tags, recipe_id, active_bake_id
		FROM entity_types WHERE id = $1 AND entity_class = 'npc'
	`, id).Scan(&p.Name, &p.Tags, &p.RecipeID, &p.ActiveBakeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, fmt.Errorf("npc %d: %w", id, ErrNpcTemplateNotFound)
		}
		return p, err
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	// EntityTypeID is the same as the row id in the new model.
	rid := id
	p.EntityTypeID = &rid
	return p, nil
}
