// Boxland — edge sockets + tile-edge assignments.
//
// Edge sockets are project-wide named connection types used by the
// procedural Mapmaker (PLAN.md §4g). Each tile-kind entity assigns one
// socket per cardinal direction; WFC reads the (entity_type, edge,
// socket) graph to decide which tiles can sit next to each other.
//
// The CRUD surface is small enough to live alongside the entity service
// rather than getting its own package.

package entities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"

	"boxland/server/internal/persistence/repo"
)

// EdgeSocketType is one row from edge_socket_types.
type EdgeSocketType struct {
	ID        int64     `db:"id"        pk:"auto" json:"id"`
	Name      string    `db:"name"                json:"name"`
	Color     int64     `db:"color"               json:"color"` // 0xRRGGBBAA, fits in int64
	CreatedBy *int64    `db:"created_by"          json:"created_by,omitempty"`
	CreatedAt time.Time `db:"created_at" repo:"readonly" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" repo:"readonly" json:"updated_at"`
}

// TileEdgeAssignment is one row from tile_edge_assignments. Sockets are
// nullable because designers commonly attach an entity-type before the
// matching socket types exist.
//
// Not driven by Repo[T] (the upsert lives in SetTileEdges via raw SQL),
// so no db: tags here -- the JSON shape is what callers consume.
type TileEdgeAssignment struct {
	EntityTypeID  int64  `json:"entity_type_id"`
	NorthSocketID *int64 `json:"north_socket_id,omitempty"`
	EastSocketID  *int64 `json:"east_socket_id,omitempty"`
	SouthSocketID *int64 `json:"south_socket_id,omitempty"`
	WestSocketID  *int64 `json:"west_socket_id,omitempty"`
}

// SocketRepo is a thin Repo[T] wrapper for edge_socket_types CRUD.
type SocketRepo struct {
	repo *repo.Repo[EdgeSocketType]
}

// NewSocketRepo returns a SocketRepo bound to the given service's pool.
func (s *Service) Sockets() *SocketRepo {
	return &SocketRepo{repo: repo.New[EdgeSocketType](s.Pool, "edge_socket_types")}
}

// CreateSocket inserts a new edge-socket type. Returns ErrSocketNameInUse
// on a unique-name collision.
func (s *Service) CreateSocket(ctx context.Context, name string, color int64, createdBy int64) (*EdgeSocketType, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("edge socket: name is required")
	}
	row := &EdgeSocketType{Name: name, Color: color, CreatedBy: &createdBy}
	if err := s.Sockets().repo.Insert(ctx, row); err != nil {
		if isUniqueViolation(err, "edge_socket_types_name_key") {
			return nil, fmt.Errorf("%w: %q", ErrSocketNameInUse, name)
		}
		return nil, fmt.Errorf("create edge socket: %w", err)
	}
	return row, nil
}

// ListSockets returns every socket, ordered alphabetically by name.
func (s *Service) ListSockets(ctx context.Context) ([]EdgeSocketType, error) {
	return s.Sockets().repo.List(ctx, repo.ListOpts{Order: "name ASC, id ASC"})
}

// FindSocketByID is a convenience around Sockets().Repo.Get.
func (s *Service) FindSocketByID(ctx context.Context, id int64) (*EdgeSocketType, error) {
	got, err := s.Sockets().repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrSocketNotFound
		}
		return nil, err
	}
	return got, nil
}

// FindSocketByName is handy for the assignment UI's typeahead.
func (s *Service) FindSocketByName(ctx context.Context, name string) (*EdgeSocketType, error) {
	rows, err := s.Sockets().repo.List(ctx, repo.ListOpts{
		Where: squirrel.Eq{"name": name},
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrSocketNotFound
	}
	return &rows[0], nil
}

// DeleteSocket removes a socket. ON DELETE SET NULL on tile_edge_assignments
// keeps the entity-type rows intact; their socket references just go null.
func (s *Service) DeleteSocket(ctx context.Context, id int64) error {
	if err := s.Sockets().repo.Delete(ctx, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrSocketNotFound
		}
		return err
	}
	return nil
}

// ---- Tile-edge assignments ----

// SetTileEdges upserts the four socket ids for a tile entity type.
// Pass nil for "no socket on this edge" -- the column accepts NULL.
func (s *Service) SetTileEdges(ctx context.Context, entityTypeID int64, north, east, south, west *int64) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO tile_edge_assignments (entity_type_id, north_socket_id, east_socket_id, south_socket_id, west_socket_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (entity_type_id) DO UPDATE
		SET north_socket_id = EXCLUDED.north_socket_id,
		    east_socket_id  = EXCLUDED.east_socket_id,
		    south_socket_id = EXCLUDED.south_socket_id,
		    west_socket_id  = EXCLUDED.west_socket_id
	`, entityTypeID, north, east, south, west)
	return err
}

// TileEdges returns the four socket ids for an entity type, or all-nil if
// no row exists yet.
func (s *Service) TileEdges(ctx context.Context, entityTypeID int64) (TileEdgeAssignment, error) {
	a := TileEdgeAssignment{EntityTypeID: entityTypeID}
	err := s.Pool.QueryRow(ctx, `
		SELECT north_socket_id, east_socket_id, south_socket_id, west_socket_id
		FROM tile_edge_assignments WHERE entity_type_id = $1
	`, entityTypeID).Scan(&a.NorthSocketID, &a.EastSocketID, &a.SouthSocketID, &a.WestSocketID)
	if err != nil {
		// pgx.ErrNoRows -> empty assignment is a valid state.
		return a, nil
	}
	return a, nil
}

// Errors returned by socket helpers.
var (
	ErrSocketNotFound  = errors.New("edge socket: not found")
	ErrSocketNameInUse = errors.New("edge socket: name already exists")
)
