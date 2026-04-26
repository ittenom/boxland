// Boxland — tile groups (PLAN.md §4e).
//
// A tile group is an N x M grid of tile-kind entity-type ids that the
// Mapmaker treats as a single paint stroke. The runtime materializes the
// grid into N x M individual tile entities at the final position.
//
// layout_json is a 2D []int64 array ([row][col]). 0 = "no tile in this
// slot" so designers can sculpt non-rectangular shapes (L-shapes, etc.).
package entities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/persistence/repo"
)

// TileGroup is one row from tile_groups.
type TileGroup struct {
	ID                           int64           `db:"id"          pk:"auto" json:"id"`
	Name                         string          `db:"name"                  json:"name"`
	Width                        int32           `db:"width"                 json:"width"`
	Height                       int32           `db:"height"                json:"height"`
	LayoutJSON                   json.RawMessage `db:"layout_json"           json:"layout"`
	Tags                         []string        `db:"tags"                  json:"tags"`
	ExcludeMembersFromProcedural bool            `db:"exclude_members_from_procedural" json:"exclude_members_from_procedural"`
	UseGroupInProcedural         bool            `db:"use_group_in_procedural"         json:"use_group_in_procedural"`
	CreatedBy                    int64           `db:"created_by"            json:"created_by"`
	CreatedAt                    time.Time       `db:"created_at" repo:"readonly" json:"created_at"`
	UpdatedAt                    time.Time       `db:"updated_at" repo:"readonly" json:"updated_at"`
}

// Layout is the typed in-memory shape of layout_json. The outer slice is
// rows; the inner slice is columns. cells contain entity_type_ids (or 0).
type Layout [][]int64

// Errors. Stable for handler mapping.
var (
	ErrTileGroupNotFound = errors.New("tile group: not found")
	ErrTileGroupNameUsed = errors.New("tile group: name already exists")
	ErrLayoutSize        = errors.New("tile group: layout dimensions don't match width x height")
)

// CreateTileGroupInput drives CreateTileGroup.
type CreateTileGroupInput struct {
	Name                 string
	Width                int32
	Height               int32
	Tags                 []string
	CreatedBy            int64
	UseGroupInProcedural *bool // nil = default true
}

type UpdateTileGroupLayoutInput struct {
	Layout                       Layout
	ExcludeMembersFromProcedural bool
	UseGroupInProcedural         bool
}

// CreateTileGroup inserts a tile group with an empty (all-zeros) layout.
func (s *Service) CreateTileGroup(ctx context.Context, in CreateTileGroupInput) (*TileGroup, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("tile group: name is required")
	}
	if in.Width < 1 || in.Width > 16 || in.Height < 1 || in.Height > 16 {
		return nil, errors.New("tile group: width and height must be 1..16")
	}
	emptyLayout := make(Layout, in.Height)
	for r := range emptyLayout {
		emptyLayout[r] = make([]int64, in.Width)
	}
	body, _ := json.Marshal(emptyLayout)

	useGroup := true
	if in.UseGroupInProcedural != nil {
		useGroup = *in.UseGroupInProcedural
	}
	row := &TileGroup{
		Name:                 in.Name,
		Width:                in.Width,
		Height:               in.Height,
		LayoutJSON:           body,
		Tags:                 valOrEmpty(in.Tags),
		UseGroupInProcedural: useGroup,
		CreatedBy:            in.CreatedBy,
	}
	r := repo.New[TileGroup](s.Pool, "tile_groups")
	if err := r.Insert(ctx, row); err != nil {
		if isUniqueViolation(err, "tile_groups_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrTileGroupNameUsed, in.Name)
		}
		return nil, fmt.Errorf("create tile group: %w", err)
	}
	return row, nil
}

// FindTileGroupByID returns one tile group.
func (s *Service) FindTileGroupByID(ctx context.Context, id int64) (*TileGroup, error) {
	r := repo.New[TileGroup](s.Pool, "tile_groups")
	got, err := r.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrTileGroupNotFound
		}
		return nil, err
	}
	return got, nil
}

// ListTileGroups returns every tile group, ordered by name.
func (s *Service) ListTileGroups(ctx context.Context) ([]TileGroup, error) {
	r := repo.New[TileGroup](s.Pool, "tile_groups")
	return r.List(ctx, repo.ListOpts{Order: "name ASC, id ASC"})
}

// UpdateTileGroupLayout replaces the layout. Width/height are taken from
// the existing row; the supplied layout MUST match those dimensions.
func (s *Service) UpdateTileGroupLayout(ctx context.Context, id int64, layout Layout) error {
	tg, err := s.FindTileGroupByID(ctx, id)
	if err != nil {
		return err
	}
	return s.UpdateTileGroupLayoutAndProcedural(ctx, id, UpdateTileGroupLayoutInput{
		Layout:                       layout,
		ExcludeMembersFromProcedural: tg.ExcludeMembersFromProcedural,
		UseGroupInProcedural:         tg.UseGroupInProcedural,
	})
}

// UpdateTileGroupLayoutAndProcedural updates the group's cell layout and the
// procedural-generation flags in one write so the editor save is atomic.
func (s *Service) UpdateTileGroupLayoutAndProcedural(ctx context.Context, id int64, in UpdateTileGroupLayoutInput) error {
	tg, err := s.FindTileGroupByID(ctx, id)
	if err != nil {
		return err
	}
	layout := in.Layout
	if int32(len(layout)) != tg.Height {
		return fmt.Errorf("%w: rows=%d height=%d", ErrLayoutSize, len(layout), tg.Height)
	}
	for r, row := range layout {
		if int32(len(row)) != tg.Width {
			return fmt.Errorf("%w: row %d cols=%d width=%d", ErrLayoutSize, r, len(row), tg.Width)
		}
	}
	body, _ := json.Marshal(layout)
	if _, err := s.Pool.Exec(ctx, `
		UPDATE tile_groups
		SET layout_json = $2,
		    exclude_members_from_procedural = $3,
		    use_group_in_procedural = $4,
		    updated_at = now()
		WHERE id = $1
	`, id, body, in.ExcludeMembersFromProcedural, in.UseGroupInProcedural); err != nil {
		return fmt.Errorf("update layout: %w", err)
	}
	return nil
}

// DeleteTileGroup removes one row.
func (s *Service) DeleteTileGroup(ctx context.Context, id int64) error {
	r := repo.New[TileGroup](s.Pool, "tile_groups")
	if err := r.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrTileGroupNotFound
		}
		return err
	}
	return nil
}

// _ keeps pgx imported for the rare error variables we may need to compare
// against without forcing every call site to import it.
var _ = pgx.ErrNoRows
