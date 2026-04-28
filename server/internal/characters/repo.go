// Boxland — characters: Service + repo wiring.
//
// Constructed once at boot. The Service holds typed Repo[T] handles for
// every characters table that needs CRUD plus a few convenience methods
// the design tools and publish handlers consume.
//
// Hot paths (live game spawn, AOI loads) are not expected to hit this
// package; they read pre-baked sprite assets via the existing assets
// pipeline. See PLAN.md §1 "Postgres access pattern".

package characters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	"boxland/server/internal/persistence"
	"boxland/server/internal/persistence/repo"
)

// pgxBeginOpts returns the default tx options used by recipe
// propagation. Hoisted so the import surface stays obvious.
func pgxBeginOpts() pgx.TxOptions { return pgx.TxOptions{} }

// Service bundles repos and pool. One per process.
//
// Bake dependencies (Store + Assets) are optional at construction time.
// They MUST be set via SetBakeDeps before any NPC-template publish runs;
// the publish handler returns a clear error if they're missing. We
// accept this two-step wiring so that lower-level call sites (chrome,
// dashboard counts, repo CRUD) can construct the Service without
// pulling in the asset/object-store graph.
type Service struct {
	Pool *pgxpool.Pool

	Slots       *repo.Repo[Slot]
	Parts       *repo.Repo[Part]
	Recipes     *repo.Repo[Recipe]
	Bakes       *repo.Repo[Bake]
	StatSets    *repo.Repo[StatSet]
	TalentTrees *repo.Repo[TalentTree]
	TalentNodes *repo.Repo[TalentNode]
	// NpcTemplate is a logical view: an NPC is an entity_types row
	// with entity_class='npc'. The Go façade preserves the old field
	// shape (recipe_id, active_bake_id, entity_type_id) but the
	// methods that touch it talk to entity_types directly. There's
	// no Repo here because entity_types isn't a 1:1 column-mapping
	// match for the NpcTemplate struct.
	PlayerCharacters *repo.Repo[PlayerCharacter]

	// Bake dependencies. Nil-safe at construction; required only when
	// RunBake is invoked (i.e. NPC-template publish). See SetBakeDeps.
	Store  *persistence.ObjectStore
	Assets *assets.Service

	// SystemDesignerID is the designer-row id used as `created_by` on
	// player-bake outputs. Player characters aren't owned by any
	// designer in particular, but `assets.created_by` is NOT NULL FK
	// to designers(id), so we attribute the row to whichever designer
	// the service was configured with (typically the realm owner).
	// Set via SetSystemDesignerID; zero disables player-side bakes.
	SystemDesignerID int64
}

// New constructs the Service and panics on misconfigured row tags (the
// repo.New panic is the right behavior — a misconfigured row should
// crash boot, not surface on the first request).
func New(pool *pgxpool.Pool) *Service {
	return &Service{
		Pool:             pool,
		Slots:            repo.New[Slot](pool, "character_slots"),
		Parts:            repo.New[Part](pool, "character_parts"),
		Recipes:          repo.New[Recipe](pool, "character_recipes"),
		Bakes:            repo.New[Bake](pool, "character_bakes"),
		StatSets:         repo.New[StatSet](pool, "character_stat_sets"),
		TalentTrees:      repo.New[TalentTree](pool, "character_talent_trees"),
		TalentNodes:      repo.New[TalentNode](pool, "character_talent_nodes"),
		PlayerCharacters: repo.New[PlayerCharacter](pool, "player_characters"),
	}
}

// SetBakeDeps installs the object store + asset service the bake
// pipeline needs. Called once at boot from cmd/boxland/main.go after
// the asset service exists.
func (s *Service) SetBakeDeps(store *persistence.ObjectStore, asvc *assets.Service) {
	s.Store = store
	s.Assets = asvc
}

// SetSystemDesignerID configures the designer id used for player-bake
// asset attribution. Must be a real row in `designers`; typically the
// realm owner's id. Without it, player-side bakes return an error.
func (s *Service) SetSystemDesignerID(id int64) {
	s.SystemDesignerID = id
}

// ListStatSets returns every stat set, ordered by name. Trivial wrapper
// so handlers don't need to import the repo package.
func (s *Service) ListStatSets(ctx context.Context) ([]StatSet, error) {
	return s.StatSets.List(ctx, repo.ListOpts{Order: "name ASC, id ASC"})
}

// ListTalentTrees returns every talent tree, ordered by name.
func (s *Service) ListTalentTrees(ctx context.Context) ([]TalentTree, error) {
	return s.TalentTrees.List(ctx, repo.ListOpts{Order: "name ASC, id ASC"})
}

// LoadTalentNodesGroupedByTree fetches every talent node in one query
// and groups them by tree id. Used by the catalog endpoint to ship
// trees + nodes without an N+1.
func (s *Service) LoadTalentNodesGroupedByTree(ctx context.Context) (map[int64][]TalentNode, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, tree_id, key, name, description, icon_asset_id, max_rank,
		       cost_json, prerequisites_json, effect_json, layout_json, mutex_group,
		       created_at, updated_at
		FROM character_talent_nodes ORDER BY tree_id, key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]TalentNode{}
	for rows.Next() {
		var n TalentNode
		if err := rows.Scan(&n.ID, &n.TreeID, &n.Key, &n.Name, &n.Description,
			&n.IconAssetID, &n.MaxRank, &n.CostJSON, &n.PrerequisitesJSON, &n.EffectJSON,
			&n.LayoutJSON, &n.MutexGroup, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out[n.TreeID] = append(out[n.TreeID], n)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Domain errors. Stable for HTTP handler mapping.
// ---------------------------------------------------------------------------

var (
	ErrSlotNotFound        = errors.New("characters: slot not found")
	ErrPartNotFound        = errors.New("characters: part not found")
	ErrStatSetNotFound     = errors.New("characters: stat_set not found")
	ErrTalentTreeNotFound  = errors.New("characters: talent_tree not found")
	ErrTalentNodeNotFound  = errors.New("characters: talent_node not found")
	ErrNpcTemplateNotFound = errors.New("characters: npc_template not found")
	ErrRecipeNotFound      = errors.New("characters: recipe not found")
	ErrPlayerCharNotFound  = errors.New("characters: player_character not found")

	// ErrForbidden is returned when an authenticated player tries to
	// touch a player_character that doesn't belong to them. Mapped to
	// HTTP 404 (not 403) by the HTTP layer to avoid leaking existence.
	ErrForbidden = errors.New("characters: forbidden")

	// ErrKeyInUse is returned when a designer tries to create a slot
	// or stat-set/talent-tree with a key that already exists.
	ErrKeyInUse = errors.New("characters: key already in use")

	// ErrNameInUse is returned when a designer tries to create an NPC
	// template with a name that already exists.
	ErrNameInUse = errors.New("characters: name already in use")
)

// ---------------------------------------------------------------------------
// Slots
// ---------------------------------------------------------------------------

// CreateSlotInput drives CreateSlot. CreatedBy is *required* for
// designer-authored slots; pass nil only when seeding system slots
// (which the migration handles, not this code path).
type CreateSlotInput struct {
	Key               string
	Label             string
	Required          bool
	OrderIndex        int32
	DefaultLayerOrder int32
	AllowsPalette     bool
	CreatedBy         int64
}

// CreateSlot inserts a new slot and returns the refreshed row.
func (s *Service) CreateSlot(ctx context.Context, in CreateSlotInput) (*Slot, error) {
	in.Key = strings.TrimSpace(in.Key)
	in.Label = strings.TrimSpace(in.Label)
	if in.CreatedBy <= 0 {
		return nil, errors.New("characters: CreateSlot requires created_by")
	}
	createdBy := in.CreatedBy
	row := &Slot{
		Key:               in.Key,
		Label:             in.Label,
		Required:          in.Required,
		OrderIndex:        in.OrderIndex,
		DefaultLayerOrder: in.DefaultLayerOrder,
		AllowsPalette:     in.AllowsPalette,
		CreatedBy:         &createdBy,
	}
	if err := row.Validate(); err != nil {
		return nil, err
	}
	if err := s.Slots.Insert(ctx, row); err != nil {
		if isUniqueViolation(err, "character_slots_key_key") {
			return nil, fmt.Errorf("%w: %q", ErrKeyInUse, in.Key)
		}
		return nil, fmt.Errorf("characters: insert slot: %w", err)
	}
	return row, nil
}

// FindSlotByID returns one slot. ErrSlotNotFound if missing.
func (s *Service) FindSlotByID(ctx context.Context, id int64) (*Slot, error) {
	got, err := s.Slots.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrSlotNotFound
		}
		return nil, err
	}
	return got, nil
}

// ListSlots returns every slot ordered by (order_index, id). Tiny
// vocabulary; no pagination.
func (s *Service) ListSlots(ctx context.Context) ([]Slot, error) {
	return s.Slots.List(ctx, repo.ListOpts{Order: "order_index ASC, id ASC"})
}

// DeleteSlot removes a slot. ErrSlotNotFound if missing.
func (s *Service) DeleteSlot(ctx context.Context, id int64) error {
	if err := s.Slots.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrSlotNotFound
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Parts
// ---------------------------------------------------------------------------

// CreatePartInput drives CreatePart. Caller is responsible for ensuring
// SlotID and AssetID exist; FK violations surface as a generic error.
type CreatePartInput struct {
	SlotID         int64
	AssetID        int64
	Name           string
	Tags           []string
	CompatibleTags []string
	LayerOrder     *int32
	FrameMapJSON   []byte
	CreatedBy      int64
}

// CreatePart inserts a new part. The unique (slot_id, asset_id) constraint
// surfaces as ErrKeyInUse so designers get a clean message instead of a
// raw pgx error string.
func (s *Service) CreatePart(ctx context.Context, in CreatePartInput) (*Part, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.CreatedBy <= 0 {
		return nil, errors.New("characters: CreatePart requires created_by")
	}
	row := &Part{
		SlotID:         in.SlotID,
		AssetID:        in.AssetID,
		Name:           in.Name,
		Tags:           valOrEmpty(in.Tags),
		CompatibleTags: valOrEmpty(in.CompatibleTags),
		LayerOrder:     in.LayerOrder,
		FrameMapJSON:   in.FrameMapJSON,
		CreatedBy:      in.CreatedBy,
	}
	if len(row.FrameMapJSON) == 0 {
		// Default to "{}" so DB NOT NULL holds and parts validate
		// before the designer attaches any animation coverage.
		row.FrameMapJSON = []byte(`{}`)
	}
	if err := row.Validate(); err != nil {
		return nil, err
	}
	if err := s.Parts.Insert(ctx, row); err != nil {
		if isUniqueViolation(err, "character_parts_slot_id_asset_id_key") {
			return nil, fmt.Errorf("%w: slot=%d asset=%d", ErrKeyInUse, in.SlotID, in.AssetID)
		}
		return nil, fmt.Errorf("characters: insert part: %w", err)
	}
	return row, nil
}

// FindPartByID returns one part.
func (s *Service) FindPartByID(ctx context.Context, id int64) (*Part, error) {
	got, err := s.Parts.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrPartNotFound
		}
		return nil, err
	}
	return got, nil
}

// ListPartsOpts narrows the part list.
type ListPartsOpts struct {
	SlotID int64    // 0 = all slots
	Tags   []string // ANY-of match against tags
	Search string   // ILIKE on name
	Limit  uint64
	Offset uint64
}

// ListParts returns parts ordered by (slot_id, name).
func (s *Service) ListParts(ctx context.Context, opts ListPartsOpts) ([]Part, error) {
	var clauses squirrel.And
	if opts.SlotID > 0 {
		clauses = append(clauses, squirrel.Eq{"slot_id": opts.SlotID})
	}
	if len(opts.Tags) > 0 {
		clauses = append(clauses, squirrel.Expr("tags && ?::text[]", opts.Tags))
	}
	if opts.Search != "" {
		clauses = append(clauses, squirrel.ILike{"name": "%" + opts.Search + "%"})
	}
	listOpts := repo.ListOpts{
		Order:  "slot_id ASC, name ASC, id ASC",
		Limit:  opts.Limit,
		Offset: opts.Offset,
	}
	if len(clauses) > 0 {
		listOpts.Where = clauses
	}
	return s.Parts.List(ctx, listOpts)
}

// DeletePart removes a part.
func (s *Service) DeletePart(ctx context.Context, id int64) error {
	if err := s.Parts.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrPartNotFound
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Recipes
// ---------------------------------------------------------------------------

// CreateRecipeInput drives CreateRecipe. Designer flow: OwnerKind=designer,
// OwnerID = designer id. Player flow (Phase 4): OwnerKind=player,
// OwnerID = player id. The caller is responsible for picking the right
// owner; the service does NOT consult auth context.
type CreateRecipeInput struct {
	OwnerKind      OwnerKind
	OwnerID        int64
	Name           string
	AppearanceJSON []byte
	StatsJSON      []byte
	TalentsJSON    []byte
	CreatedBy      int64
}

// CreateRecipe inserts a new recipe row, computing the canonical hash
// from the supplied JSON blobs. Empty blobs canonicalize to minimal
// envelopes (no error).
func (s *Service) CreateRecipe(ctx context.Context, in CreateRecipeInput) (*Recipe, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("characters: recipe name is required")
	}
	if err := in.OwnerKind.Validate(); err != nil {
		return nil, err
	}
	if in.OwnerID <= 0 {
		return nil, errors.New("characters: recipe owner_id is required")
	}
	if in.CreatedBy <= 0 {
		return nil, errors.New("characters: CreateRecipe requires created_by")
	}

	hash, err := ComputeRecipeHash(in.Name, in.AppearanceJSON, in.StatsJSON, in.TalentsJSON)
	if err != nil {
		return nil, fmt.Errorf("characters: hash recipe: %w", err)
	}

	row := &Recipe{
		OwnerKind:      in.OwnerKind,
		OwnerID:        in.OwnerID,
		Name:           in.Name,
		AppearanceJSON: defaultJSON(in.AppearanceJSON, `{"slots":[]}`),
		StatsJSON:      defaultJSON(in.StatsJSON, `{}`),
		TalentsJSON:    defaultJSON(in.TalentsJSON, `{}`),
		RecipeHash:     hash,
		CreatedBy:      in.CreatedBy,
	}
	if err := row.Validate(); err != nil {
		return nil, err
	}
	if err := s.Recipes.Insert(ctx, row); err != nil {
		return nil, fmt.Errorf("characters: insert recipe: %w", err)
	}
	return row, nil
}

// FindRecipeByID returns one recipe.
func (s *Service) FindRecipeByID(ctx context.Context, id int64) (*Recipe, error) {
	got, err := s.Recipes.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrRecipeNotFound
		}
		return nil, err
	}
	return got, nil
}

// UpdateRecipeInput drives UpdateRecipe. The caller passes the recipe
// id + the new payload; the service re-validates ownership and rewrites
// the row + recipe_hash. Cross-owner updates are rejected (ErrForbidden).
type UpdateRecipeInput struct {
	ID             int64
	OwnerKind      OwnerKind
	OwnerID        int64
	Name           string
	AppearanceJSON []byte
	StatsJSON      []byte
	TalentsJSON    []byte
}

// UpdateRecipe rewrites a recipe row owned by (OwnerKind, OwnerID).
// Returns ErrRecipeNotFound when the id doesn't exist; ErrForbidden
// when the recipe belongs to a different owner.
//
// Library-shared semantics (per spec resolved decision #6): a designer
// recipe edit propagates to every NPC template linked to the recipe.
// If bake deps are configured AND the recipe content changed, this
// method runs a fresh bake inside one tx and updates every linked
// NPC template's active_bake_id. The hash dedup makes no-op edits
// cheap (no new asset row, no new bake row).
//
// Player recipes don't propagate (each player_character has its own
// recipe by design).
func (s *Service) UpdateRecipe(ctx context.Context, in UpdateRecipeInput) (*Recipe, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("characters: recipe name is required")
	}
	existing, err := s.FindRecipeByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if existing.OwnerKind != in.OwnerKind || existing.OwnerID != in.OwnerID {
		return nil, ErrForbidden
	}
	hash, err := ComputeRecipeHash(in.Name, in.AppearanceJSON, in.StatsJSON, in.TalentsJSON)
	if err != nil {
		return nil, fmt.Errorf("characters: hash recipe: %w", err)
	}
	hashChanged := !bytesEqual(existing.RecipeHash, hash)

	existing.Name = in.Name
	existing.AppearanceJSON = defaultJSON(in.AppearanceJSON, `{"slots":[]}`)
	existing.StatsJSON = defaultJSON(in.StatsJSON, `{}`)
	existing.TalentsJSON = defaultJSON(in.TalentsJSON, `{}`)
	existing.RecipeHash = hash
	if err := existing.Validate(); err != nil {
		return nil, err
	}
	if err := s.Recipes.Update(ctx, existing); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrRecipeNotFound
		}
		return nil, fmt.Errorf("characters: update recipe: %w", err)
	}

	// Library-shared propagation: re-bake + push to every linked NPC.
	// Skip the work when the hash hasn't changed (the existing bake
	// is still authoritative) OR when bake deps aren't wired (e.g.
	// in unit tests that don't need the bake side effect).
	if existing.OwnerKind == OwnerKindDesigner && hashChanged && s.Store != nil && s.Assets != nil {
		if err := s.propagateRecipeToLinkedNPCs(ctx, existing); err != nil {
			// Surface the propagation error to the caller — better
			// for the designer to know "edit landed but propagation
			// failed; please republish manually" than to silently
			// fail.
			return existing, fmt.Errorf("characters: recipe updated but propagation failed: %w", err)
		}
	}

	return existing, nil
}

// propagateRecipeToLinkedNPCs runs a fresh bake for the supplied
// recipe and updates every npc_template that points at it. All work
// happens inside one transaction so a bake failure leaves the linked
// NPC templates unchanged (they keep their old active_bake_id).
//
// Important: this only touches the *bake* + *active_bake_id* link.
// It does NOT affect any draft NPC template rows in the publish
// pipeline; the next publish will see the freshly-baked id and
// happily reuse it via the recipe_hash dedup.
func (s *Service) propagateRecipeToLinkedNPCs(ctx context.Context, recipe *Recipe) error {
	tx, err := s.Pool.BeginTx(ctx, pgxBeginOpts())
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	bakeRecipe, err := LoadBakeRecipe(ctx, tx, recipe.ID)
	if err != nil {
		// Empty/invalid recipe = no propagation possible. Surface as
		// a soft no-op rather than an error so partial designer state
		// (e.g. mid-edit recipe with no slots) doesn't break unrelated
		// links.
		return nil
	}
	out, err := RunBake(ctx, tx, BakeDeps{
		Store:            s.Store,
		Assets:           s.Assets,
		SystemDesignerID: s.SystemDesignerID,
	}, bakeRecipe, recipe.ID)
	if err != nil {
		return fmt.Errorf("run bake: %w", err)
	}
	// Update every NPC entity_type linked to this recipe. The
	// active_bake_id + sprite_asset_id flip atomically alongside the
	// bake row's existence, so spawned NPCs render the new look on
	// the next instance reload.
	if _, err := tx.Exec(ctx, `
		UPDATE entity_types
		SET active_bake_id = $1, sprite_asset_id = $2, updated_at = now()
		WHERE entity_class = 'npc' AND recipe_id = $3
	`, out.BakeID, out.AssetID, recipe.ID); err != nil {
		return fmt.Errorf("propagate to npc entity_types: %w", err)
	}
	return tx.Commit(ctx)
}

// bytesEqual is a tiny constant-time-friendly equality. We don't need
// constant-time here (the hash is a derivation, not a secret), but
// using bytes.Equal would force an additional import.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AttachRecipeToNpcTemplate sets the recipe_id on an npc-class
// entity_types row. Used by the generator UI's "save & link" path.
// Doesn't bake — that happens on the next publish.
func (s *Service) AttachRecipeToNpcTemplate(ctx context.Context, templateID, recipeID int64) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE entity_types SET recipe_id = $1, updated_at = now()
		WHERE id = $2 AND entity_class = 'npc'
	`, recipeID, templateID)
	if err != nil {
		return fmt.Errorf("characters: attach recipe: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNpcTemplateNotFound
	}
	return nil
}

// defaultJSON returns def (as a byte slice) when in is empty/nil.
func defaultJSON(in []byte, def string) []byte {
	if len(in) == 0 {
		return []byte(def)
	}
	return in
}

// ---------------------------------------------------------------------------
// NPC templates
// ---------------------------------------------------------------------------

// CreateNpcTemplateInput drives CreateNpcTemplate. The recipe and bake
// links are nullable on creation — designers attach them via the
// generator UI after creating the template shell.
type CreateNpcTemplateInput struct {
	Name      string
	Tags      []string
	CreatedBy int64
}

// CreateNpcTemplate inserts a new NPC: an entity_types row with
// entity_class='npc'. The row's id IS the NPC's identity — it doubles
// as the template id and the entity_type id (the lazy-mint-on-publish
// dance is gone in the holistic redesign).
func (s *Service) CreateNpcTemplate(ctx context.Context, in CreateNpcTemplateInput) (*NpcTemplate, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.CreatedBy <= 0 {
		return nil, errors.New("characters: CreateNpcTemplate requires created_by")
	}
	row := &NpcTemplate{
		Name:      in.Name,
		Tags:      valOrEmpty(in.Tags),
		CreatedBy: in.CreatedBy,
	}
	if err := row.Validate(); err != nil {
		return nil, err
	}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO entity_types (name, entity_class, tags, created_by)
		VALUES ($1, 'npc', $2, $3)
		RETURNING id, created_at, updated_at
	`, row.Name, row.Tags, row.CreatedBy).Scan(&row.ID, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err, "entity_types_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		return nil, fmt.Errorf("characters: insert npc entity_type: %w", err)
	}
	// EntityTypeID is the same as the row's id — kept on the struct
	// for back-compat with code that explicitly passes the entity_type
	// id around.
	id := row.ID
	row.EntityTypeID = &id
	return row, nil
}

// FindNpcTemplateByID returns one NPC.
func (s *Service) FindNpcTemplateByID(ctx context.Context, id int64) (*NpcTemplate, error) {
	row := &NpcTemplate{}
	err := s.Pool.QueryRow(ctx, `
		SELECT id, name, recipe_id, active_bake_id, tags, created_by, created_at, updated_at
		FROM entity_types WHERE id = $1 AND entity_class = 'npc'
	`, id).Scan(&row.ID, &row.Name, &row.RecipeID, &row.ActiveBakeID,
		&row.Tags, &row.CreatedBy, &row.CreatedAt, &row.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNpcTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("characters: find npc: %w", err)
	}
	rid := row.ID
	row.EntityTypeID = &rid
	return row, nil
}

// ListNpcTemplates returns NPCs ordered by name.
func (s *Service) ListNpcTemplates(ctx context.Context) ([]NpcTemplate, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, recipe_id, active_bake_id, tags, created_by, created_at, updated_at
		FROM entity_types WHERE entity_class = 'npc'
		ORDER BY name ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("characters: list npcs: %w", err)
	}
	defer rows.Close()
	var out []NpcTemplate
	for rows.Next() {
		var r NpcTemplate
		if err := rows.Scan(&r.ID, &r.Name, &r.RecipeID, &r.ActiveBakeID,
			&r.Tags, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rid := r.ID
		r.EntityTypeID = &rid
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteNpcTemplate removes the underlying entity_type. components,
// automations, level_entities placements, etc. cascade via FK.
func (s *Service) DeleteNpcTemplate(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM entity_types WHERE id = $1 AND entity_class = 'npc'
	`, id)
	if err != nil {
		return fmt.Errorf("characters: delete npc: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNpcTemplateNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Player characters — owner-scoped
// ---------------------------------------------------------------------------

// CreatePlayerCharacterInput drives CreatePlayerCharacter.
type CreatePlayerCharacterInput struct {
	PlayerID  int64
	Name      string
	PublicBio string
}

// CreatePlayerCharacter inserts a new shell row scoped to playerID.
// The recipe + active_bake are nil at creation; the player attaches
// them via the generator's save flow.
//
// Cross-player creation is impossible: the caller passes playerID
// from the authenticated context, never from a request body.
func (s *Service) CreatePlayerCharacter(ctx context.Context, in CreatePlayerCharacterInput) (*PlayerCharacter, error) {
	if in.PlayerID <= 0 {
		return nil, errors.New("characters: CreatePlayerCharacter requires player_id")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("characters: player_character name is required")
	}
	row := &PlayerCharacter{
		PlayerID:  in.PlayerID,
		Name:      in.Name,
		PublicBio: in.PublicBio,
	}
	if err := row.Validate(); err != nil {
		return nil, err
	}
	if err := s.PlayerCharacters.Insert(ctx, row); err != nil {
		return nil, fmt.Errorf("characters: insert player_character: %w", err)
	}
	return row, nil
}

// LinkPlayerCharacterRecipe sets the player_character.recipe_id and
// active_bake_id atomically. Used by the player-mode save endpoint.
// Cross-player writes are rejected (ErrForbidden) so a player can't
// re-target another player's character.
func (s *Service) LinkPlayerCharacterRecipe(ctx context.Context, playerID, charID, recipeID, bakeID int64) error {
	if playerID <= 0 {
		return errors.New("characters: LinkPlayerCharacterRecipe requires player_id")
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE player_characters
		SET recipe_id = $1, active_bake_id = $2, updated_at = now()
		WHERE id = $3 AND player_id = $4
	`, recipeID, bakeID, charID, playerID)
	if err != nil {
		return fmt.Errorf("characters: link player_character recipe: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the char doesn't exist OR it belongs to another
		// player. Map both to NotFound to avoid leaking existence.
		return ErrPlayerCharNotFound
	}
	return nil
}

// FindPlayerCharacter returns the character if and only if it belongs to
// playerID. Cross-player access returns ErrForbidden so the HTTP layer
// can map it to 404 without leaking existence.
func (s *Service) FindPlayerCharacter(ctx context.Context, playerID, charID int64) (*PlayerCharacter, error) {
	if playerID <= 0 {
		return nil, errors.New("characters: FindPlayerCharacter requires player_id")
	}
	got, err := s.PlayerCharacters.Get(ctx, charID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrPlayerCharNotFound
		}
		return nil, err
	}
	if got.PlayerID != playerID {
		return nil, ErrForbidden
	}
	return got, nil
}

// ListPlayerCharacters returns every character owned by playerID.
func (s *Service) ListPlayerCharacters(ctx context.Context, playerID int64) ([]PlayerCharacter, error) {
	if playerID <= 0 {
		return nil, errors.New("characters: ListPlayerCharacters requires player_id")
	}
	return s.PlayerCharacters.List(ctx, repo.ListOpts{
		Where: squirrel.Eq{"player_id": playerID},
		Order: "name ASC, id ASC",
	})
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation on the named index. Mirrors the helper in entities/.
func isUniqueViolation(err error, constraintName string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "23505" {
		return false
	}
	return constraintName == "" || pgErr.ConstraintName == constraintName
}

// valOrEmpty returns an empty slice when in is nil. Postgres TEXT[]
// columns are NOT NULL DEFAULT '{}', so nil slices would fail to insert.
func valOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
