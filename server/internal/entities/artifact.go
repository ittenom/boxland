// Boxland — EntityType as Artifact[T] (PLAN.md §4o).
package entities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/configurable"
	"boxland/server/internal/entities/components"
	"boxland/server/internal/publishing/artifact"
)

// EntityTypeDraft is the editable surface of an entity type. Wrapped here
// (vs. exposing the EntityType row directly) so the descriptor is a clean
// list of human-meaningful fields and the publish handler can compute a
// useful structured diff.
type EntityTypeDraft struct {
	Name                 string                                  `json:"name"`
	SpriteAssetID        *int64                                  `json:"sprite_asset_id,omitempty"`
	DefaultAnimationID   *int64                                  `json:"default_animation_id,omitempty"`
	ColliderW            int32                                   `json:"collider_w"`
	ColliderH            int32                                   `json:"collider_h"`
	ColliderAnchorX      int32                                   `json:"collider_anchor_x"`
	ColliderAnchorY      int32                                   `json:"collider_anchor_y"`
	DefaultCollisionMask int64                                   `json:"default_collision_mask"`
	Tags                 []string                                `json:"tags"`
	Components           map[components.Kind]json.RawMessage     `json:"components,omitempty"`
}

// Validate enforces basic invariants. Component-config validation is
// performed against the live registry inside the publish path so the
// draft doesn't need to know which kinds are registered.
func (d EntityTypeDraft) Validate() error {
	if d.Name == "" {
		return errors.New("entity-type draft: name is required")
	}
	if d.ColliderW < 0 || d.ColliderH < 0 {
		return errors.New("entity-type draft: collider dimensions must be non-negative")
	}
	if d.ColliderAnchorX > d.ColliderW || d.ColliderAnchorY > d.ColliderH {
		return errors.New("entity-type draft: collider anchor must lie within W x H")
	}
	for _, t := range d.Tags {
		if t == "" {
			return errors.New("entity-type draft: tags must not contain empty strings")
		}
	}
	return nil
}

// Descriptor drives the generic form renderer (excludes `components` --
// that's edited via a per-component sub-form, not the top-level field
// list). The Entity Manager UI composes this descriptor with one
// per-component sub-form per entry in the registry.
func (EntityTypeDraft) Descriptor() []configurable.FieldDescriptor {
	zero := 0.0
	return []configurable.FieldDescriptor{
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: 128},
		{Key: "sprite_asset_id", Label: "Sprite asset", Kind: configurable.KindAssetRef,
			RefTags: []string{"sprite", "sprite_animated"}},
		{Key: "default_animation_id", Label: "Default animation id", Kind: configurable.KindInt, Min: &zero,
			Help: "0 (or empty) defers to the first animation on the asset."},
		{Key: "collider_w", Label: "Collider W (px)", Kind: configurable.KindInt, Min: &zero},
		{Key: "collider_h", Label: "Collider H (px)", Kind: configurable.KindInt, Min: &zero},
		{Key: "collider_anchor_x", Label: "Anchor X (px)", Kind: configurable.KindInt, Min: &zero},
		{Key: "collider_anchor_y", Label: "Anchor Y (px)", Kind: configurable.KindInt, Min: &zero},
		{Key: "default_collision_mask", Label: "Default collision mask", Kind: configurable.KindInt, Min: &zero,
			Help: "uint32 bitmask. 1 = land (default)."},
		{Key: "tags", Label: "Tags", Kind: configurable.KindList,
			Children: []configurable.FieldDescriptor{
				{Key: "tag", Label: "Tag", Kind: configurable.KindString, MaxLen: 32},
			}},
	}
}

// Handler implements artifact.Handler for the entity_type kind.
type Handler struct {
	Svc *Service
}

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{Svc: svc}
}

func (*Handler) Kind() artifact.Kind { return artifact.KindEntityType }

func (h *Handler) Validate(ctx context.Context, draft artifact.DraftRow) error {
	var d EntityTypeDraft
	if err := json.Unmarshal(draft.DraftJSON, &d); err != nil {
		return fmt.Errorf("entity-type draft: bad json: %w", err)
	}
	if err := d.Validate(); err != nil {
		return err
	}
	if len(d.Components) > 0 {
		if err := h.Svc.Compos.ValidateAll(d.Components); err != nil {
			return err
		}
	}
	return nil
}

// Publish applies the draft within the given tx and emits a structured diff.
func (h *Handler) Publish(ctx context.Context, tx pgx.Tx, draft artifact.DraftRow) (artifact.PublishResult, error) {
	var d EntityTypeDraft
	if err := json.Unmarshal(draft.DraftJSON, &d); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("entity-type draft: bad json: %w", err)
	}

	prev, err := h.loadPrev(ctx, tx, draft.ArtifactID)
	if err != nil {
		return artifact.PublishResult{}, err
	}

	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE entity_types SET
			name = $2,
			sprite_asset_id = $3,
			default_animation_id = $4,
			collider_w = $5,
			collider_h = $6,
			collider_anchor_x = $7,
			collider_anchor_y = $8,
			default_collision_mask = $9,
			tags = $10,
			updated_at = now()
		WHERE id = $1
	`, draft.ArtifactID, d.Name, d.SpriteAssetID, d.DefaultAnimationID,
		d.ColliderW, d.ColliderH, d.ColliderAnchorX, d.ColliderAnchorY,
		d.DefaultCollisionMask, tags); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply entity-type update: %w", err)
	}

	if d.Components != nil {
		if err := h.Svc.SetComponents(ctx, tx, draft.ArtifactID, d.Components); err != nil {
			return artifact.PublishResult{}, fmt.Errorf("apply components: %w", err)
		}
	}

	diff := configurable.DiffJSON(prev, d)
	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: diff}, nil
}

// loadPrev pulls the live row inside the tx so the diff sees the
// transaction-consistent before-state.
func (h *Handler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (EntityTypeDraft, error) {
	var p EntityTypeDraft
	var spriteAsset, defaultAnim *int64
	err := tx.QueryRow(ctx, `
		SELECT name, sprite_asset_id, default_animation_id,
		       collider_w, collider_h, collider_anchor_x, collider_anchor_y,
		       default_collision_mask, tags
		FROM entity_types WHERE id = $1
	`, id).Scan(
		&p.Name, &spriteAsset, &defaultAnim,
		&p.ColliderW, &p.ColliderH, &p.ColliderAnchorX, &p.ColliderAnchorY,
		&p.DefaultCollisionMask, &p.Tags,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, fmt.Errorf("entity_type %d: %w", id, ErrEntityTypeNotFound)
		}
		return p, err
	}
	p.SpriteAssetID = spriteAsset
	p.DefaultAnimationID = defaultAnim
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return p, nil
}
