// Package artifact is the generic lifecycle framework that every
// designer-managed object (assets, entity types, maps, palettes, edge
// socket types, tile groups) plugs into. See PLAN.md §4o.
//
// Each artifact kind registers a Handler with the package-level Registry.
// The publish Pipeline walks dirty drafts in the `drafts` table, asks each
// handler to validate and publish in a single transaction, computes a
// structured diff, writes summary lines to publish_diffs, and emits a
// LivePublish broadcast (broadcast wired in task #132).
package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/configurable"
)

// Kind identifies an artifact kind on the wire and in the database.
// Mirrors design.fbs ArtifactKind. Stable; do not rename.
type Kind string

const (
	KindAsset          Kind = "asset"
	KindEntityType     Kind = "entity_type"
	KindMap            Kind = "map"
	KindPalette        Kind = "palette"
	KindPaletteVariant Kind = "palette_variant"
	KindEdgeSocketType Kind = "edge_socket_type"
	KindTileGroup      Kind = "tile_group"
)

// Op enumerates the kind of change a draft represents.
type Op string

const (
	OpCreated Op = "created"
	OpUpdated Op = "updated"
	OpDeleted Op = "deleted"
)

// DraftRow is a row from the `drafts` table. Handlers receive this as input.
type DraftRow struct {
	ArtifactKind Kind            `db:"artifact_kind" json:"kind"`
	ArtifactID   int64           `db:"artifact_id"   json:"id"`
	DraftJSON    json.RawMessage `db:"draft_json"    json:"draft"`
	CreatedBy    int64           `db:"created_by"    json:"created_by"`
}

// PublishResult is what a handler returns after committing within the
// pipeline transaction. The diff feeds publish_diffs and the LivePublish
// broadcast.
type PublishResult struct {
	Op   Op
	Diff configurable.StructuredDiff
}

// Handler implements the per-kind logic the pipeline needs. Each artifact
// kind (assets, entity types, maps, ...) provides one Handler at boot via
// Register.
type Handler interface {
	Kind() Kind

	// Validate runs the artifact's typed Validate() against the draft
	// payload. Called before transaction begins so bad drafts surface
	// quickly without holding a transaction open.
	Validate(ctx context.Context, draft DraftRow) error

	// Publish applies the draft inside the supplied transaction and
	// returns a PublishResult describing the change. Implementations
	// should compute the diff against whatever the live row was prior
	// to this transaction (typically via configurable.DiffJSON).
	Publish(ctx context.Context, tx pgx.Tx, draft DraftRow) (PublishResult, error)
}

// Registry holds per-kind handlers. The package exposes a default registry
// for production use; tests construct fresh registries to stay isolated.
type Registry struct {
	mu       sync.RWMutex
	handlers map[Kind]Handler
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[Kind]Handler)}
}

// Register adds h to the registry. Panics on duplicate kinds so the bug
// surfaces at boot time.
func (r *Registry) Register(h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.handlers[h.Kind()]; ok {
		panic(fmt.Sprintf("artifact: duplicate handler for kind %q", h.Kind()))
	}
	r.handlers[h.Kind()] = h
}

// HandlerFor returns the registered handler or false.
func (r *Registry) HandlerFor(k Kind) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[k]
	return h, ok
}

// ---- Pipeline ----

// PostCommitHook fires AFTER the publish transaction commits, with the
// outcomes the publish produced. PLAN.md §132: hooks let the pipeline
// trigger inline palette bake + LivePublish broadcast without coupling
// `artifact` to the assets / runtime packages.
//
// Hooks run sequentially in registration order; an error is logged but
// does NOT roll back the publish (the commit already landed). Hooks
// that need transactional behaviour should be wired inside the handler
// instead.
type PostCommitHook func(ctx context.Context, outcomes []PublishOutcome) error

// Pipeline runs Push-to-Live: collect dirty drafts, validate each one,
// publish them inside a single transaction, write publish_diffs rows, and
// fire post-commit hooks (palette bake + LivePublish broadcast).
type Pipeline struct {
	pool     *pgxpool.Pool
	registry *Registry
	hooks    []PostCommitHook
}

// NewPipeline binds a pool and registry.
func NewPipeline(pool *pgxpool.Pool, registry *Registry) *Pipeline {
	return &Pipeline{pool: pool, registry: registry}
}

// OnPostCommit registers a hook that fires after a successful publish.
// Multiple hooks can be registered; they fire in registration order.
func (p *Pipeline) OnPostCommit(h PostCommitHook) {
	p.hooks = append(p.hooks, h)
}

// PublishOutcome is one entry in the result of Run.
type PublishOutcome struct {
	Kind         Kind
	ArtifactID   int64
	ChangesetID  int64
	Op           Op
	SummaryLine  string
	// Diff is the per-field structured delta. Populated for both
	// Run (post-commit) and Preview (rolled-back). The diff modal
	// (PLAN.md §134) renders SummaryLine + Diff.Changes.
	Diff         configurable.StructuredDiff
}

// ErrUnknownKind is returned when a draft references a kind no handler is
// registered for. A sentinel because callers commonly want to skip-and-warn
// rather than abort the whole publish.
var ErrUnknownKind = errors.New("artifact: no handler registered for kind")

// Preview computes the same per-artifact outcomes Run would produce
// without committing -- runs each handler inside a transaction that
// is always rolled back. PLAN.md §134: the diff preview modal calls
// this before the user confirms a Push-to-Live.
//
// SummaryLine + Diff.Changes are populated; ChangesetID is 0
// (no changeset is allocated since nothing commits).
func (p *Pipeline) Preview(ctx context.Context) ([]PublishOutcome, error) {
	drafts, err := p.loadDrafts(ctx)
	if err != nil {
		return nil, fmt.Errorf("load drafts: %w", err)
	}
	if len(drafts) == 0 {
		return nil, nil
	}

	for _, d := range drafts {
		h, ok := p.registry.HandlerFor(d.ArtifactKind)
		if !ok {
			return nil, fmt.Errorf("draft %s/%d: %w (%q)",
				d.ArtifactKind, d.ArtifactID, ErrUnknownKind, d.ArtifactKind)
		}
		if err := h.Validate(ctx, d); err != nil {
			return nil, fmt.Errorf("validate %s/%d: %w", d.ArtifactKind, d.ArtifactID, err)
		}
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// ALWAYS roll back; this is a preview.
	defer func() { _ = tx.Rollback(ctx) }()

	outcomes := make([]PublishOutcome, 0, len(drafts))
	for _, d := range drafts {
		h, _ := p.registry.HandlerFor(d.ArtifactKind)
		res, err := h.Publish(ctx, tx, d)
		if err != nil {
			return nil, fmt.Errorf("preview %s/%d: %w", d.ArtifactKind, d.ArtifactID, err)
		}
		outcomes = append(outcomes, PublishOutcome{
			Kind:        d.ArtifactKind,
			ArtifactID:  d.ArtifactID,
			ChangesetID: 0,
			Op:          res.Op,
			SummaryLine: res.Diff.SummaryLine,
			Diff:        res.Diff,
		})
	}
	return outcomes, nil
}

// Run executes the publish pipeline. Either every draft is published and
// removed atomically, or none are. publishedBy is the designer id that
// initiated the push; recorded on publish_diffs.
//
// Returns the per-artifact outcomes in deterministic order (kind, id).
func (p *Pipeline) Run(ctx context.Context, publishedBy int64) ([]PublishOutcome, error) {
	drafts, err := p.loadDrafts(ctx)
	if err != nil {
		return nil, fmt.Errorf("load drafts: %w", err)
	}
	if len(drafts) == 0 {
		slog.Info("publish: no drafts to publish")
		return nil, nil
	}

	// Validate everything up front so we don't open a transaction on
	// known-bad data.
	for _, d := range drafts {
		h, ok := p.registry.HandlerFor(d.ArtifactKind)
		if !ok {
			return nil, fmt.Errorf("draft %s/%d: %w (%q)",
				d.ArtifactKind, d.ArtifactID, ErrUnknownKind, d.ArtifactKind)
		}
		if err := h.Validate(ctx, d); err != nil {
			return nil, fmt.Errorf("validate %s/%d: %w", d.ArtifactKind, d.ArtifactID, err)
		}
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	changesetID, err := p.allocateChangesetID(ctx, tx)
	if err != nil {
		return nil, err
	}

	outcomes := make([]PublishOutcome, 0, len(drafts))
	for _, d := range drafts {
		h, _ := p.registry.HandlerFor(d.ArtifactKind) // already verified
		res, err := h.Publish(ctx, tx, d)
		if err != nil {
			return nil, fmt.Errorf("publish %s/%d: %w", d.ArtifactKind, d.ArtifactID, err)
		}
		if err := p.recordDiff(ctx, tx, changesetID, d, res, publishedBy); err != nil {
			return nil, fmt.Errorf("record diff %s/%d: %w", d.ArtifactKind, d.ArtifactID, err)
		}
		if err := p.deleteDraft(ctx, tx, d); err != nil {
			return nil, fmt.Errorf("delete draft %s/%d: %w", d.ArtifactKind, d.ArtifactID, err)
		}
		outcomes = append(outcomes, PublishOutcome{
			Kind:        d.ArtifactKind,
			ArtifactID:  d.ArtifactID,
			ChangesetID: changesetID,
			Op:          res.Op,
			SummaryLine: res.Diff.SummaryLine,
			Diff:        res.Diff,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	slog.Info("publish: committed",
		"changeset_id", changesetID,
		"artifact_count", len(outcomes),
		"published_by", publishedBy,
	)
	// Post-commit hooks (palette bake, LivePublish broadcast). Errors
	// are logged but don't roll back -- the publish transaction has
	// already landed; rolling back here would leave the canonical
	// state and the broadcast out of sync.
	for _, hk := range p.hooks {
		if err := hk(ctx, outcomes); err != nil {
			slog.Warn("publish: post-commit hook failed",
				"changeset_id", changesetID, "err", err)
		}
	}
	return outcomes, nil
}

// CountDrafts returns the number of pending drafts. Used by the chrome's
// "drafts" badge so the designer always knows how much they have queued
// for the next Push-to-Live, without paying the cost of a full Preview.
func (p *Pipeline) CountDrafts(ctx context.Context) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, `SELECT count(*) FROM drafts`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count drafts: %w", err)
	}
	return n, nil
}

// ---- internals ----

func (p *Pipeline) loadDrafts(ctx context.Context) ([]DraftRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT artifact_kind, artifact_id, draft_json, created_by
		FROM drafts
		ORDER BY artifact_kind, artifact_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowToStructByName[DraftRow])
}

func (p *Pipeline) allocateChangesetID(ctx context.Context, tx pgx.Tx) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT nextval('publish_changeset_seq')`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("allocate changeset id: %w", err)
	}
	return id, nil
}

func (p *Pipeline) recordDiff(
	ctx context.Context,
	tx pgx.Tx,
	changesetID int64,
	d DraftRow,
	res PublishResult,
	publishedBy int64,
) error {
	body, err := json.Marshal(res.Diff)
	if err != nil {
		return fmt.Errorf("marshal diff: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO publish_diffs
			(changeset_id, artifact_kind, artifact_id, op, summary_line, structured_diff_json, published_by, created_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, now())
	`, changesetID, string(d.ArtifactKind), d.ArtifactID, string(res.Op), res.Diff.SummaryLine, body, publishedBy)
	return err
}

func (p *Pipeline) deleteDraft(ctx context.Context, tx pgx.Tx, d DraftRow) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM drafts WHERE artifact_kind = $1 AND artifact_id = $2
	`, string(d.ArtifactKind), d.ArtifactID)
	return err
}
