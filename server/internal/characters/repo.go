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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"boxland/server/internal/assets"
	"boxland/server/internal/persistence"
	"boxland/server/internal/persistence/repo"
)

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

	Slots            *repo.Repo[Slot]
	Parts            *repo.Repo[Part]
	Recipes          *repo.Repo[Recipe]
	Bakes            *repo.Repo[Bake]
	StatSets         *repo.Repo[StatSet]
	TalentTrees      *repo.Repo[TalentTree]
	TalentNodes      *repo.Repo[TalentNode]
	NpcTemplates     *repo.Repo[NpcTemplate]
	PlayerCharacters *repo.Repo[PlayerCharacter]

	// Bake dependencies. Nil-safe at construction; required only when
	// RunBake is invoked (i.e. NPC-template publish). See SetBakeDeps.
	Store  *persistence.ObjectStore
	Assets *assets.Service
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
		NpcTemplates:     repo.New[NpcTemplate](pool, "npc_templates"),
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
	return existing, nil
}

// AttachRecipeToNpcTemplate sets npc_templates.recipe_id. Used by the
// generator UI's "save & link" path. Doesn't bake — that happens on
// the next publish.
func (s *Service) AttachRecipeToNpcTemplate(ctx context.Context, templateID, recipeID int64) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE npc_templates SET recipe_id = $1, updated_at = now() WHERE id = $2
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

// CreateNpcTemplate inserts a new NPC template shell.
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
	if err := s.NpcTemplates.Insert(ctx, row); err != nil {
		if isUniqueViolation(err, "npc_templates_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrNameInUse, in.Name)
		}
		return nil, fmt.Errorf("characters: insert npc_template: %w", err)
	}
	return row, nil
}

// FindNpcTemplateByID returns one NPC template.
func (s *Service) FindNpcTemplateByID(ctx context.Context, id int64) (*NpcTemplate, error) {
	got, err := s.NpcTemplates.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrNpcTemplateNotFound
		}
		return nil, err
	}
	return got, nil
}

// ListNpcTemplates returns templates ordered by name.
func (s *Service) ListNpcTemplates(ctx context.Context) ([]NpcTemplate, error) {
	return s.NpcTemplates.List(ctx, repo.ListOpts{Order: "name ASC, id ASC"})
}

// DeleteNpcTemplate removes a template.
func (s *Service) DeleteNpcTemplate(ctx context.Context, id int64) error {
	if err := s.NpcTemplates.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrNpcTemplateNotFound
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Player characters — owner-scoped
// ---------------------------------------------------------------------------

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
