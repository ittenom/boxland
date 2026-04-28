package designer

import (
	"errors"
	"net/http"

	"boxland/server/internal/entities"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
)

// palette_handlers.go — JSON endpoints feeding the Pixi-driven editor
// canvases. Both return the same `PaletteAtlasEntry` shape (see
// palette_helper.go); callers in TS use them to seed a
// `StaticAssetCatalog` so the renderer can resolve sprite URLs +
// atlas cells without a second round-trip per asset.
//
// Endpoint pairing:
//
//   GET /design/maps/{id}/tile-types       — atlas info for tile-class
//                                             entity_types referenced
//                                             on this map's tiles.
//   GET /design/levels/{id}/entity-types   — atlas info for the
//                                             placeable classes (npc,
//                                             pc, logic) project-wide.
//
// Both are auth-gated by the same `auth(...)` wrapper as every other
// /design route. Both are read-only.

// getMapTileTypes returns the atlas catalog needed to render the
// map's tile geometry. Scoped to entity_types actually present on the
// map (not the project-wide tile catalog) so the editor doesn't pay
// for textures it won't draw.
func getMapTileTypes(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Maps == nil {
			http.Error(w, "maps service unavailable", http.StatusServiceUnavailable)
			return
		}
		entries, err := BuildMapTileAtlas(r.Context(), d, id)
		if err != nil {
			if errors.Is(err, mapsservice.ErrMapNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = writePaletteJSON(w, entries)
	}
}

// getLevelEntityTypes returns the placement catalog: every npc/pc/
// logic entity_type the designer can drop on a level. Tile-class
// entity_types are excluded — those are painted on the map.
//
// Currently project-wide (no level-scoped narrowing). If a project
// grows past a few thousand entity_types we'll add folder + class
// query parameters; for v0 the editor JS filters in-page on a search
// box, which scales to ~10k items easily.
func getLevelEntityTypes(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		// 404 the route on a nonexistent level so URL typos surface
		// cleanly. The list itself isn't level-scoped today, but
		// keeping the level-id in the URL means a future scope-down
		// (per-level allowlist? per-world tag filter?) won't break
		// the wire shape.
		if _, err := d.Levels.FindByID(r.Context(), id); err != nil {
			if errors.Is(err, levels.ErrLevelNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		entries := BuildPaletteAtlas(r.Context(), d, []entities.Class{
			entities.ClassNPC, entities.ClassPC, entities.ClassLogic,
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = writePaletteJSON(w, entries)
	}
}
