package designer

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"boxland/server/internal/levels"
)

// level_entities_handlers.go — JSON HTTP surface for LEVEL entity
// placements (NPCs, PCs, doors, region triggers, spawn points, …).
//
// Per the holistic redesign:
//
//   MAP   = pure tile geometry (paint on the map editor).
//   LEVEL = a MAP + non-tile entity placements (this surface).
//   WORLD = a graph of LEVELs.
//
// The Service methods (Place/Move/Set overrides/Remove/List) already
// exist and are tested in server/internal/levels. This file is the
// thin HTTP/JSON skin the visual level editor talks to. All routes
// are mounted under /design and require a designer session via auth.
//
// Tenant safety: every mutating handler verifies that the placement
// id belongs to the level id in the URL before calling the service.
// That stops a designer from poking at another level's placements by
// guessing entity ids — even when both levels are theirs, the URL is
// the source of truth, and editor UIs only ever target their own
// level. Read-side handlers go through ListEntities(level_id)
// directly so they're scoped by construction.

// levelEntityWire is the JSON shape exchanged with the level editor
// JS. snake_case to match the rest of the design API; rotation in
// degrees (0/90/180/270); overrides + tags pass through as-is.
type levelEntityWire struct {
	ID                int64           `json:"id"`
	EntityTypeID      int64           `json:"entity_type_id"`
	X                 int32           `json:"x"`
	Y                 int32           `json:"y"`
	RotationDegrees   int16           `json:"rotation_degrees"`
	InstanceOverrides json.RawMessage `json:"instance_overrides"`
	Tags              []string        `json:"tags"`
}

func toLevelEntityWire(le levels.LevelEntity) levelEntityWire {
	overrides := le.InstanceOverridesJSON
	if len(overrides) == 0 {
		overrides = json.RawMessage("{}")
	}
	tags := le.Tags
	if tags == nil {
		tags = []string{}
	}
	return levelEntityWire{
		ID:                le.ID,
		EntityTypeID:      le.EntityTypeID,
		X:                 le.X,
		Y:                 le.Y,
		RotationDegrees:   le.RotationDegrees,
		InstanceOverrides: overrides,
		Tags:              tags,
	}
}

// validRotation is the same gate the levels.Service uses; we mirror
// it here so the handler can return 400 instead of 500 on bad input.
func validRotation(r int16) bool {
	switch r {
	case 0, 90, 180, 270:
		return true
	}
	return false
}

// pathInt64 reads a named PathValue (e.g. "eid") and returns it as
// int64. Used for the placement id alongside the level id (which the
// existing pathID helper already covers).
func pathInt64(r *http.Request, name string) (int64, error) {
	raw := strings.TrimSpace(r.PathValue(name))
	if raw == "" {
		return 0, fmt.Errorf("missing %s", name)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s: %q", name, raw)
	}
	return n, nil
}

// ---- GET /design/levels/{id}/entities --------------------------------

// getLevelEntities returns every placement on a level as a flat array.
// One DB call (ListEntities), then a single JSON encode — N+1 free
// regardless of placement count.
func getLevelEntities(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		levelID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		// Confirm the level exists so callers see a 404 rather than
		// an empty 200 when they hit a stale URL.
		if _, err := d.Levels.FindByID(r.Context(), levelID); err != nil {
			if errors.Is(err, levels.ErrLevelNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("find level for entities", "err", err, "level_id", levelID)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		ents, err := d.Levels.ListEntities(r.Context(), levelID)
		if err != nil {
			slog.Error("list level entities", "err", err, "level_id", levelID)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out := make([]levelEntityWire, 0, len(ents))
		for _, e := range ents {
			out = append(out, toLevelEntityWire(e))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"level_id": levelID,
			"entities": out,
		})
	}
}

// ---- POST /design/levels/{id}/entities -------------------------------

// postLevelEntity creates one placement.
//
// Body: { entity_type_id, x, y, rotation_degrees?, instance_overrides?, tags? }
// Returns: { entity: levelEntityWire }
//
// One placement per call keeps the editor wire shape simple — the
// canvas is interactive (one click = one placement) and bulk import
// has its own /design/levels/import path.
func postLevelEntity(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		levelID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		if _, err := d.Levels.FindByID(r.Context(), levelID); err != nil {
			if errors.Is(err, levels.ErrLevelNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		var body struct {
			EntityTypeID      int64           `json:"entity_type_id"`
			X                 int32           `json:"x"`
			Y                 int32           `json:"y"`
			RotationDegrees   int16           `json:"rotation_degrees"`
			InstanceOverrides json.RawMessage `json:"instance_overrides"`
			Tags              []string        `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.EntityTypeID <= 0 {
			http.Error(w, "entity_type_id is required", http.StatusBadRequest)
			return
		}
		if !validRotation(body.RotationDegrees) {
			http.Error(w, fmt.Sprintf("invalid rotation_degrees: %d", body.RotationDegrees), http.StatusBadRequest)
			return
		}
		le, err := d.Levels.PlaceEntity(r.Context(), levels.PlaceEntityInput{
			LevelID:               levelID,
			EntityTypeID:          body.EntityTypeID,
			X:                     body.X,
			Y:                     body.Y,
			RotationDegrees:       body.RotationDegrees,
			InstanceOverridesJSON: body.InstanceOverrides,
			Tags:                  body.Tags,
		})
		if err != nil {
			slog.Error("place level entity", "err", err, "level_id", levelID)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"entity": toLevelEntityWire(*le)})
	}
}

// ---- PATCH /design/levels/{id}/entities/{eid} ------------------------

// patchLevelEntity updates one placement's position/rotation and/or
// overrides. Pointer-typed body fields let the client send a partial
// update — the editor sends {x,y,rotation_degrees} on drag-move,
// {instance_overrides} on inspector save, etc.
func patchLevelEntity(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		levelID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		eid, err := pathInt64(r, "eid")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		// Fetch the current row so we can (a) verify level scoping
		// and (b) fill in unspecified fields on the move call.
		current, err := findLevelEntity(d, r, levelID, eid)
		if err != nil {
			respondNotFoundOr500(w, r, err, "find level entity", "level_id", levelID, "eid", eid)
			return
		}
		var body struct {
			X                 *int32           `json:"x"`
			Y                 *int32           `json:"y"`
			RotationDegrees   *int16           `json:"rotation_degrees"`
			InstanceOverrides *json.RawMessage `json:"instance_overrides"`
			Tags              *[]string        `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Move (position/rotation). Only fire the SQL UPDATE if at
		// least one of these is in the body.
		if body.X != nil || body.Y != nil || body.RotationDegrees != nil {
			x := current.X
			y := current.Y
			rot := current.RotationDegrees
			if body.X != nil {
				x = *body.X
			}
			if body.Y != nil {
				y = *body.Y
			}
			if body.RotationDegrees != nil {
				rot = *body.RotationDegrees
			}
			if !validRotation(rot) {
				http.Error(w, fmt.Sprintf("invalid rotation_degrees: %d", rot), http.StatusBadRequest)
				return
			}
			if err := d.Levels.MoveEntity(r.Context(), eid, x, y, rot); err != nil {
				slog.Error("move level entity", "err", err, "level_id", levelID, "eid", eid)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}
		// Overrides + tags. Tags currently piggyback on the
		// overrides setter — we don't have a dedicated SetTags yet,
		// and shipping it here would balloon the diff. Editor uses
		// instance_overrides for now; tags-on-placement is a small
		// follow-up.
		if body.InstanceOverrides != nil {
			if err := d.Levels.SetEntityOverrides(r.Context(), eid, *body.InstanceOverrides); err != nil {
				slog.Error("set level entity overrides", "err", err, "level_id", levelID, "eid", eid)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}
		// Re-fetch so the client sees the authoritative row.
		updated, err := findLevelEntity(d, r, levelID, eid)
		if err != nil {
			respondNotFoundOr500(w, r, err, "refetch level entity", "level_id", levelID, "eid", eid)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"entity": toLevelEntityWire(*updated)})
	}
}

// ---- DELETE /design/levels/{id}/entities/{eid} -----------------------

// deleteLevelEntity removes one placement after verifying it belongs
// to the level in the URL.
func deleteLevelEntity(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		levelID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		eid, err := pathInt64(r, "eid")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		if _, err := findLevelEntity(d, r, levelID, eid); err != nil {
			respondNotFoundOr500(w, r, err, "find level entity for delete", "level_id", levelID, "eid", eid)
			return
		}
		if err := d.Levels.RemoveEntity(r.Context(), eid); err != nil {
			if errors.Is(err, levels.ErrLevelEntityNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("remove level entity", "err", err, "level_id", levelID, "eid", eid)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// findLevelEntity is the tenant-isolation gate every mutating
// handler runs before touching a placement. We list the level's
// placements (cheap; bounded by the level's own size) and pick the
// requested id out — that way a 404 is returned both when the eid
// doesn't exist AND when it exists on a *different* level.
//
// A targeted SELECT would be more efficient but the levels.Service
// doesn't currently expose a "find by id with level scoping" method,
// and this keeps the handler honest about scoping without reaching
// past the service layer into pgx directly.
func findLevelEntity(d Deps, r *http.Request, levelID, eid int64) (*levels.LevelEntity, error) {
	ents, err := d.Levels.ListEntities(r.Context(), levelID)
	if err != nil {
		return nil, err
	}
	for i := range ents {
		if ents[i].ID == eid {
			return &ents[i], nil
		}
	}
	return nil, levels.ErrLevelEntityNotFound
}

func respondNotFoundOr500(w http.ResponseWriter, r *http.Request, err error, msg string, kv ...any) {
	if errors.Is(err, levels.ErrLevelEntityNotFound) || errors.Is(err, levels.ErrLevelNotFound) {
		http.NotFound(w, r)
		return
	}
	slog.Error(msg, append([]any{"err", err}, kv...)...)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
