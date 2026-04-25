// Boxland — Asset as Artifact[T] (PLAN.md §4o lifecycle plug-in).
//
// Asset is the first (and reference) implementation of the artifact
// publish pipeline. The shape established here is the template for every
// other designer-managed artifact (entity types, maps, palettes, ...).
//
// What "publishing an asset" means in v1:
//   * rename
//   * retag
//   * (later) attach/detach palette variants -- recipe rows are separate
//     artifacts wired in their own handler
//
// Replacing the bytes is NOT a publish — it's a fresh upload that produces
// a new asset_id; outdated references are migrated separately.

package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/configurable"
	"boxland/server/internal/publishing/artifact"
)

// AssetDraft is the shape of an asset draft stored in drafts.draft_json
// when ArtifactKind = "asset". Only the editable fields appear; immutable
// fields (kind, content_addressed_path) are not in the draft surface.
type AssetDraft struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// Validate enforces basic invariants. Stricter rules (uniqueness of
// (kind, name)) are enforced at SQL level via the assets_kind_name_idx
// constraint, and surfaced as a publish-time error.
func (d AssetDraft) Validate() error {
	if d.Name == "" {
		return errors.New("asset draft: name is required")
	}
	for _, t := range d.Tags {
		if t == "" {
			return errors.New("asset draft: tags must not contain empty strings")
		}
	}
	return nil
}

// Descriptor maps to the generic form renderer (task #33). Asset edits use
// the same renderer as every other Configurable.
func (AssetDraft) Descriptor() []configurable.FieldDescriptor {
	return []configurable.FieldDescriptor{
		{Key: "name", Label: "Name", Kind: configurable.KindString, Required: true, MaxLen: 128},
		{Key: "tags", Label: "Tags", Kind: configurable.KindList, Children: []configurable.FieldDescriptor{
			{Key: "tag", Label: "Tag", Kind: configurable.KindString, MaxLen: 32},
		}},
	}
}

// Handler implements artifact.Handler for the asset kind.
type Handler struct {
	Svc *Service
}

// NewHandler constructs the handler. Registered into the artifact registry
// at server boot.
func NewHandler(svc *Service) *Handler {
	return &Handler{Svc: svc}
}

func (*Handler) Kind() artifact.Kind { return artifact.KindAsset }

func (h *Handler) Validate(ctx context.Context, draft artifact.DraftRow) error {
	var d AssetDraft
	if err := json.Unmarshal(draft.DraftJSON, &d); err != nil {
		return fmt.Errorf("asset draft: bad json: %w", err)
	}
	return d.Validate()
}

// Publish applies the draft within the supplied transaction. Returns the
// op (always OpUpdated for v1; create + delete arrive in later iterations)
// and a structured diff against the previous row.
func (h *Handler) Publish(ctx context.Context, tx pgx.Tx, draft artifact.DraftRow) (artifact.PublishResult, error) {
	var d AssetDraft
	if err := json.Unmarshal(draft.DraftJSON, &d); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("asset draft: bad json: %w", err)
	}

	// Read the live row inside the tx so the diff is consistent with what
	// other artifact handlers see in the same publish. We use raw SQL here
	// rather than the Repo[T] surface so we honour the supplied tx.
	var prevName string
	var prevTags []string
	err := tx.QueryRow(ctx,
		`SELECT name, tags FROM assets WHERE id = $1`,
		draft.ArtifactID,
	).Scan(&prevName, &prevTags)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return artifact.PublishResult{}, fmt.Errorf("asset %d: %w", draft.ArtifactID, ErrAssetNotFound)
		}
		return artifact.PublishResult{}, err
	}

	// Apply the update.
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE assets SET name = $2, tags = $3, updated_at = now() WHERE id = $1`,
		draft.ArtifactID, d.Name, tags,
	); err != nil {
		return artifact.PublishResult{}, fmt.Errorf("apply asset update: %w", err)
	}

	// Compute the diff against the previous values.
	prev := AssetDraft{Name: prevName, Tags: prevTags}
	if prev.Tags == nil {
		prev.Tags = []string{}
	}
	diff := configurable.DiffJSON(prev, d)

	return artifact.PublishResult{
		Op:   artifact.OpUpdated,
		Diff: diff,
	}, nil
}
