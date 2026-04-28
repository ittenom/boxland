package maps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/configurable"
	"boxland/server/internal/publishing/artifact"
)

// MapDraft is the editable surface of a map artifact: name, mode, and
// procedural seed. Tile placements + lighting cells flow through their
// own per-stroke endpoints (PlaceTiles etc.) rather than the draft path
// because they're high-frequency edits.
//
// Per the holistic redesign, public/instancing/persistence/spectator
// settings live on a LEVEL, not on a MAP — see the levels package's
// LevelDraft for those fields.
type MapDraft struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
	Seed *int64 `json:"seed,omitempty"`
}

// Validate enforces invariants at the draft layer; SQL CHECK
// constraints catch the same things server-side.
func (d MapDraft) Validate() error {
	if d.Name == "" {
		return errors.New("map draft: name is required")
	}
	if d.Mode != "" {
		switch d.Mode {
		case "authored", "procedural":
		default:
			return fmt.Errorf("map draft: mode %q invalid", d.Mode)
		}
	}
	return nil
}

// Descriptor drives the generic form renderer.
func (MapDraft) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: 128},
		{
			Key: "mode", Label: "Mode", Kind: configurable.KindEnum,
			Options: []configurable.EnumOption{
				{Value: "authored", Label: "Authored (paint by hand)"},
				{Value: "procedural", Label: "Procedural (generated from samples + constraints)"},
			},
		},
		{
			Key: "seed", Label: "Procedural seed", Kind: configurable.KindInt,
			Help: "Procedural maps only. Re-roll to regenerate the layout.",
		},
	}
}

// Handler implements artifact.Handler for the "map" kind.
type Handler struct{ Svc *Service }

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler { return &Handler{Svc: svc} }

func (*Handler) Kind() artifact.Kind { return artifact.KindMap }

func (h *Handler) Validate(_ context.Context, draft artifact.DraftRow) error {
	var d MapDraft
	if err := json.Unmarshal(draft.DraftJSON, &d); err != nil {
		return fmt.Errorf("map draft: bad json: %w", err)
	}
	return d.Validate()
}

// Publish applies the draft inside the supplied tx.
func (h *Handler) Publish(ctx context.Context, tx pgx.Tx, draft artifact.DraftRow) (artifact.PublishResult, error) {
	var d MapDraft
	if err := json.Unmarshal(draft.DraftJSON, &d); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("map draft: bad json: %w", err)
	}

	prev, err := h.loadPrev(ctx, tx, draft.ArtifactID)
	if err != nil {
		return artifact.PublishResult{}, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE maps SET name = $2, mode = $3, seed = $4, updated_at = now()
		WHERE id = $1
	`, draft.ArtifactID, d.Name, d.Mode, d.Seed,
	); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply map update: %w", err)
	}

	diff := configurable.DiffJSON(prev, d)
	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: diff}, nil
}

func (h *Handler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (MapDraft, error) {
	var d MapDraft
	err := tx.QueryRow(ctx, `
		SELECT name, mode, seed FROM maps WHERE id = $1
	`, id).Scan(&d.Name, &d.Mode, &d.Seed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return d, fmt.Errorf("map %d: %w", id, ErrMapNotFound)
		}
		return d, err
	}
	return d, nil
}
