// Boxland — non-local procedural-constraint CRUD.
//
// One row per constraint per map. The procedural runner reads them on
// every preview/materialize and feeds them to the WFC engine via
// GenerateOptions.Constraints / OverlappingOptions.Constraints. See
// server/internal/maps/wfc/constraints.go for the engine-side
// implementations and migration 0040 for the schema.
//
// Wire shape (stored in `params` JSONB):
//
//   border:
//     {
//       "entity_type_id": 42,
//       "edges": ["top", "right", "bottom", "left"],   // any subset
//       "restrict": false                              // optional
//     }
//
//   path:
//     {
//       "entity_type_ids": [42, 43]   // empty = "any non-zero counts"
//     }
//
// Unknown kinds fall through with a warning rather than failing the
// whole generation — callers should validate at insert time.

package maps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"boxland/server/internal/maps/wfc"
)

// MapConstraint is one row of map_constraints.
type MapConstraint struct {
	ID     int64           `json:"id"`
	MapID  int64           `json:"map_id"`
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params"`
}

// Constraint kinds.
const (
	ConstraintKindBorder = "border"
	ConstraintKindPath   = "path"
)

// ErrConstraintInvalid is returned when a payload fails validation.
var ErrConstraintInvalid = errors.New("maps: constraint payload invalid")

// MapConstraints returns every constraint on a map, in stable id order.
// Single indexed query.
func (s *Service) MapConstraints(ctx context.Context, mapID int64) ([]MapConstraint, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, map_id, kind, params
		FROM map_constraints
		WHERE map_id = $1
		ORDER BY id
	`, mapID)
	if err != nil {
		return nil, fmt.Errorf("map constraints: %w", err)
	}
	defer rows.Close()
	var out []MapConstraint
	for rows.Next() {
		var c MapConstraint
		if err := rows.Scan(&c.ID, &c.MapID, &c.Kind, &c.Params); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddMapConstraintInput drives AddMapConstraint.
type AddMapConstraintInput struct {
	MapID  int64
	Kind   string
	Params json.RawMessage
}

// AddMapConstraint inserts one constraint and returns the new id. The
// kind / params are validated up-front so a future runProcedural call
// won't trip over bad data.
func (s *Service) AddMapConstraint(ctx context.Context, in AddMapConstraintInput) (int64, error) {
	if !validConstraintKind(in.Kind) {
		return 0, fmt.Errorf("%w: unknown kind %q", ErrConstraintInvalid, in.Kind)
	}
	if _, err := parseConstraintParams(in.Kind, in.Params); err != nil {
		return 0, err
	}
	if len(in.Params) == 0 {
		in.Params = json.RawMessage(`{}`)
	}
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO map_constraints (map_id, kind, params)
		VALUES ($1, $2, $3)
		RETURNING id
	`, in.MapID, in.Kind, in.Params).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert constraint: %w", err)
	}
	return id, nil
}

// DeleteMapConstraint removes one constraint. No-op if it doesn't exist.
func (s *Service) DeleteMapConstraint(ctx context.Context, mapID, constraintID int64) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM map_constraints WHERE id = $1 AND map_id = $2`,
		constraintID, mapID,
	)
	if err != nil {
		return fmt.Errorf("delete constraint: %w", err)
	}
	return nil
}

// validConstraintKind mirrors the CHECK constraint in migration 0040.
func validConstraintKind(kind string) bool {
	switch kind {
	case ConstraintKindBorder, ConstraintKindPath:
		return true
	}
	return false
}

// borderParamsJSON / pathParamsJSON mirror the JSONB shapes documented
// at the top of this file.
type borderParamsJSON struct {
	EntityTypeID int64    `json:"entity_type_id"`
	Edges        []string `json:"edges"`
	Restrict     bool     `json:"restrict"`
}

type pathParamsJSON struct {
	EntityTypeIDs []int64 `json:"entity_type_ids"`
}

// parseConstraintParams validates `params` for `kind` and returns the
// engine-side wfc.Constraint. Used both at insert time (validation)
// and at run time (translation).
func parseConstraintParams(kind string, params json.RawMessage) (wfc.Constraint, error) {
	switch kind {
	case ConstraintKindBorder:
		var p borderParamsJSON
		if err := json.Unmarshal(orEmptyJSON(params), &p); err != nil {
			return nil, fmt.Errorf("%w: border params: %w", ErrConstraintInvalid, err)
		}
		if p.EntityTypeID <= 0 {
			return nil, fmt.Errorf("%w: border requires entity_type_id > 0", ErrConstraintInvalid)
		}
		mask, err := parseBorderEdges(p.Edges)
		if err != nil {
			return nil, err
		}
		return &wfc.BorderConstraint{
			EntityType: wfc.EntityTypeID(p.EntityTypeID),
			Edges:      mask,
			Restrict:   p.Restrict,
		}, nil
	case ConstraintKindPath:
		var p pathParamsJSON
		if err := json.Unmarshal(orEmptyJSON(params), &p); err != nil {
			return nil, fmt.Errorf("%w: path params: %w", ErrConstraintInvalid, err)
		}
		ids := make([]wfc.EntityTypeID, 0, len(p.EntityTypeIDs))
		for _, id := range p.EntityTypeIDs {
			if id > 0 {
				ids = append(ids, wfc.EntityTypeID(id))
			}
		}
		return &wfc.PathConstraint{PathTypes: ids}, nil
	}
	return nil, fmt.Errorf("%w: unknown kind %q", ErrConstraintInvalid, kind)
}

func orEmptyJSON(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return b
}

func parseBorderEdges(edges []string) (wfc.BorderEdgeMask, error) {
	if len(edges) == 0 {
		return wfc.BorderAll, nil
	}
	var mask wfc.BorderEdgeMask
	for _, e := range edges {
		switch e {
		case "top":
			mask |= wfc.BorderTop
		case "right":
			mask |= wfc.BorderRight
		case "bottom":
			mask |= wfc.BorderBottom
		case "left":
			mask |= wfc.BorderLeft
		case "all":
			mask = wfc.BorderAll
		default:
			return 0, fmt.Errorf("%w: unknown border edge %q (want top/right/bottom/left/all)",
				ErrConstraintInvalid, e)
		}
	}
	if mask == 0 {
		return wfc.BorderAll, nil
	}
	return mask, nil
}

// loadMapConstraints reads + parses every constraint for a map. Bad
// rows are skipped with a slog warning rather than failing the whole
// generation — the alternative is "designer painted a map with one bad
// constraint, the whole map breaks", which is the wrong default.
func (s *Service) loadMapConstraints(ctx context.Context, mapID int64) ([]wfc.Constraint, error) {
	rows, err := s.MapConstraints(ctx, mapID)
	if err != nil {
		return nil, err
	}
	out := make([]wfc.Constraint, 0, len(rows))
	for _, r := range rows {
		c, err := parseConstraintParams(r.Kind, r.Params)
		if err != nil {
			slog.Warn("skipping invalid map constraint",
				"map_id", mapID, "constraint_id", r.ID, "kind", r.Kind, "err", err)
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
