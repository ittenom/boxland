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

// MapDraft is the editable surface of a map artifact: the metadata
// fields the designer can change between publishes. Tile placements +
// lighting cells flow through their own per-stroke endpoints (PlaceTiles
// etc) rather than the draft path because they're high-frequency edits.
type MapDraft struct {
	Name                 string  `json:"name"`
	Public               bool    `json:"public"`
	InstancingMode       string  `json:"instancing_mode"`
	PersistenceMode      string  `json:"persistence_mode"`
	RefreshWindowSeconds *int32  `json:"refresh_window_seconds,omitempty"`
	SpectatorPolicy      string  `json:"spectator_policy"`
	Seed                 *int64  `json:"seed,omitempty"`
}

// Validate enforces invariants at the draft layer; SQL CHECK constraints
// catch the same things server-side.
func (d MapDraft) Validate() error {
	if d.Name == "" {
		return errors.New("map draft: name is required")
	}
	if d.InstancingMode != "" {
		switch d.InstancingMode {
		case "shared", "per_user", "per_party":
		default:
			return fmt.Errorf("map draft: instancing_mode %q invalid", d.InstancingMode)
		}
	}
	if d.PersistenceMode != "" {
		switch d.PersistenceMode {
		case "persistent", "transient":
		default:
			return fmt.Errorf("map draft: persistence_mode %q invalid", d.PersistenceMode)
		}
	}
	if d.SpectatorPolicy != "" {
		switch d.SpectatorPolicy {
		case "public", "private", "invite":
		default:
			return fmt.Errorf("map draft: spectator_policy %q invalid", d.SpectatorPolicy)
		}
	}
	return nil
}

// Descriptor drives the generic form renderer (PLAN.md task #33).
func (MapDraft) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: 128},
		{Key: "public", Label: "Public", Kind: configurable.KindBool},
		{
			Key: "instancing_mode", Label: "Instancing", Kind: configurable.KindEnum,
			Options: []configurable.EnumOption{
				{Value: "shared", Label: "Shared (one instance, everyone joins it)"},
				{Value: "per_user", Label: "Per user"},
				{Value: "per_party", Label: "Per party"},
			},
		},
		{
			Key: "persistence_mode", Label: "Persistence", Kind: configurable.KindEnum,
			Options: []configurable.EnumOption{
				{Value: "persistent", Label: "Persistent"},
				{Value: "transient", Label: "Transient (resets per refresh window)"},
			},
		},
		{
			Key: "refresh_window_seconds", Label: "Refresh window (seconds)", Kind: configurable.KindInt,
			Help: "For transient maps: how often to regenerate. Ignored for persistent maps.",
		},
		{
			Key: "spectator_policy", Label: "Spectator policy", Kind: configurable.KindEnum,
			Options: []configurable.EnumOption{
				{Value: "public", Label: "Public (any authenticated player)"},
				{Value: "private", Label: "Private (designer realm only)"},
				{Value: "invite", Label: "Invite only"},
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
		UPDATE maps SET
			name = $2,
			public = $3,
			instancing_mode = $4,
			persistence_mode = $5,
			refresh_window_seconds = $6,
			spectator_policy = $7,
			seed = $8,
			updated_at = now()
		WHERE id = $1
	`, draft.ArtifactID, d.Name, d.Public, d.InstancingMode, d.PersistenceMode,
		d.RefreshWindowSeconds, d.SpectatorPolicy, d.Seed,
	); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply map update: %w", err)
	}

	diff := configurable.DiffJSON(prev, d)
	return artifact.PublishResult{Op: artifact.OpUpdated, Diff: diff}, nil
}

func (h *Handler) loadPrev(ctx context.Context, tx pgx.Tx, id int64) (MapDraft, error) {
	var d MapDraft
	err := tx.QueryRow(ctx, `
		SELECT name, public, instancing_mode, persistence_mode,
		       refresh_window_seconds, spectator_policy, seed
		FROM maps WHERE id = $1
	`, id).Scan(
		&d.Name, &d.Public, &d.InstancingMode, &d.PersistenceMode,
		&d.RefreshWindowSeconds, &d.SpectatorPolicy, &d.Seed,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return d, fmt.Errorf("map %d: %w", id, ErrMapNotFound)
		}
		return d, err
	}
	return d, nil
}
