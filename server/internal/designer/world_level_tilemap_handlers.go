package designer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/levels"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/worlds"
	"boxland/server/views"
)

// world_level_tilemap_handlers.go — Phase 2 top-level surfaces from the
// holistic redesign. Adds /design/worlds, /design/levels, /design/tilemaps,
// /design/library, and the per-class entity pages
// (/design/entities/{tiles|npcs|pcs|logic}).
//
// These are intentionally minimal first cuts: list pages + create
// modals + delete + (where applicable) detail-stub redirects. The rich
// editors (level-with-tabs, world graph, tilemap viewer) live in
// dedicated files alongside.

// ---- worlds list page ------------------------------------------------

func getWorldsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		layout := BuildChrome(r, d)
		layout.Title = "Worlds"
		layout.ActiveKind = "world"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Worlds"}}

		var items []worlds.World
		if d.Worlds != nil {
			search := strings.TrimSpace(r.URL.Query().Get("q"))
			got, err := d.Worlds.List(r.Context(), worlds.ListOpts{
				Search: search, Limit: 200,
			})
			if err != nil {
				slog.Error("worlds list", "err", err)
				http.Error(w, "list worlds", http.StatusInternalServerError)
				return
			}
			items = got
		}
		// Annotate each world with its level count for the card badge.
		// Single bulk query keeps this N+1-safe.
		levelsByWorld := map[int64]int{}
		if d.Levels != nil {
			lvs, err := d.Levels.List(r.Context(), levels.ListOpts{Limit: 1024})
			if err == nil {
				for _, lv := range lvs {
					if lv.WorldID != nil {
						levelsByWorld[*lv.WorldID]++
					}
				}
			}
		}
		renderHTML(w, r, views.WorldsListPage(views.WorldsListProps{
			Layout:        layout,
			Items:         items,
			LevelsByWorld: levelsByWorld,
			Search:        strings.TrimSpace(r.URL.Query().Get("q")),
		}))
	}
}

func getWorldNewModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = d
		renderHTML(w, r, views.WorldNewModal())
	}
}

func postWorldCreate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Worlds == nil {
			http.Error(w, "worlds service unavailable", http.StatusServiceUnavailable)
			return
		}
		w0, err := d.Worlds.Create(r.Context(), worlds.CreateInput{
			Name:      strings.TrimSpace(r.FormValue("name")),
			CreatedBy: dr.ID,
		})
		if err != nil {
			if errors.Is(err, worlds.ErrNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if errors.Is(err, worlds.ErrInvalidName) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Redirect", fmt.Sprintf("/design/worlds/%d", w0.ID))
		w.WriteHeader(http.StatusCreated)
	}
}

func getWorldDetail(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Worlds == nil {
			http.NotFound(w, r)
			return
		}
		wld, err := d.Worlds.FindByID(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var lvs []levels.Level
		if d.Levels != nil {
			lvs, _ = d.Levels.List(r.Context(), levels.ListOpts{WorldID: &wld.ID, Limit: 1024})
		}
		layout := BuildChrome(r, d)
		layout.Title = wld.Name
		layout.ActiveKind = "world"
		layout.ActiveID = wld.ID
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{
			{Label: "Worlds", Href: "/design/worlds"},
			{Label: wld.Name},
		}
		renderHTML(w, r, views.WorldDetailPage(views.WorldDetailProps{
			Layout: layout,
			World:  *wld,
			Levels: lvs,
		}))
	}
}

func deleteWorld(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Worlds == nil {
			http.Error(w, "worlds service unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := d.Worlds.Delete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Redirect", "/design/worlds")
		w.WriteHeader(http.StatusOK)
	}
}

func postWorldRename(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Worlds == nil {
			http.Error(w, "worlds service unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := d.Worlds.Rename(r.Context(), id, strings.TrimSpace(r.FormValue("name"))); err != nil {
			if errors.Is(err, worlds.ErrNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func postWorldStartLevel(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Worlds == nil {
			http.Error(w, "worlds service unavailable", http.StatusServiceUnavailable)
			return
		}
		raw := strings.TrimSpace(r.FormValue("level_id"))
		var levelID *int64
		if raw != "" {
			n, err := strconvAtoi64(raw)
			if err != nil {
				http.Error(w, "bad level_id", http.StatusBadRequest)
				return
			}
			levelID = &n
		}
		if err := d.Worlds.SetStartLevel(r.Context(), id, levelID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ---- levels list page ------------------------------------------------

func getLevelsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		layout := BuildChrome(r, d)
		layout.Title = "Levels"
		layout.ActiveKind = "level"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Levels"}}

		var items []levels.Level
		if d.Levels != nil {
			search := strings.TrimSpace(r.URL.Query().Get("q"))
			got, err := d.Levels.List(r.Context(), levels.ListOpts{
				Search: search, Limit: 400,
			})
			if err != nil {
				slog.Error("levels list", "err", err)
				http.Error(w, "list levels", http.StatusInternalServerError)
				return
			}
			items = got
		}
		// Resolve map names + world names in bulk so the cards don't
		// trigger N+1.
		mapNames := map[int64]string{}
		worldNames := map[int64]string{}
		if d.Maps != nil {
			ms, _ := d.Maps.List(r.Context(), "")
			for _, m := range ms {
				mapNames[m.ID] = m.Name
			}
		}
		if d.Worlds != nil {
			ws, _ := d.Worlds.List(r.Context(), worlds.ListOpts{Limit: 1024})
			for _, w := range ws {
				worldNames[w.ID] = w.Name
			}
		}
		renderHTML(w, r, views.LevelsListPage(views.LevelsListProps{
			Layout:     layout,
			Items:      items,
			MapNames:   mapNames,
			WorldNames: worldNames,
			Search:     strings.TrimSpace(r.URL.Query().Get("q")),
		}))
	}
}

func getLevelNewModal(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var maps []views.LevelNewMapOption
		if d.Maps != nil {
			ms, _ := d.Maps.List(r.Context(), "")
			for _, m := range ms {
				maps = append(maps, views.LevelNewMapOption{ID: m.ID, Name: m.Name})
			}
		}
		var ws []views.LevelNewWorldOption
		if d.Worlds != nil {
			items, _ := d.Worlds.List(r.Context(), worlds.ListOpts{Limit: 1024})
			for _, w := range items {
				ws = append(ws, views.LevelNewWorldOption{ID: w.ID, Name: w.Name})
			}
		}
		renderHTML(w, r, views.LevelNewModal(views.LevelNewProps{Maps: maps, Worlds: ws}))
	}
}

func postLevelCreate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		mapID, err := strconvAtoi64(r.FormValue("map_id"))
		if err != nil || mapID == 0 {
			http.Error(w, "map_id is required", http.StatusBadRequest)
			return
		}
		var worldID *int64
		if raw := strings.TrimSpace(r.FormValue("world_id")); raw != "" {
			n, err := strconvAtoi64(raw)
			if err == nil && n > 0 {
				worldID = &n
			}
		}
		lv, err := d.Levels.Create(r.Context(), levels.CreateInput{
			Name:      strings.TrimSpace(r.FormValue("name")),
			MapID:     mapID,
			WorldID:   worldID,
			Public:    parseCheckbox(r.FormValue("public")),
			CreatedBy: dr.ID,
		})
		if err != nil {
			if errors.Is(err, levels.ErrNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("HX-Redirect", fmt.Sprintf("/design/levels/%d", lv.ID))
		w.WriteHeader(http.StatusCreated)
	}
}

func getLevelDetail(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.NotFound(w, r)
			return
		}
		lv, err := d.Levels.FindByID(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		mapName := ""
		var mapWidth, mapHeight int32
		if d.Maps != nil {
			if m, err := d.Maps.FindByID(r.Context(), lv.MapID); err == nil && m != nil {
				mapName = m.Name
				mapWidth = m.Width
				mapHeight = m.Height
			}
		}
		worldName := ""
		if lv.WorldID != nil && d.Worlds != nil {
			if wld, err := d.Worlds.FindByID(r.Context(), *lv.WorldID); err == nil {
				worldName = wld.Name
			}
		}
		// Placement count for the Entities tab badge.
		var placementCount int
		if ents, err := d.Levels.ListEntities(r.Context(), lv.ID); err == nil {
			placementCount = len(ents)
		}
		layout := BuildChrome(r, d)
		layout.Title = lv.Name
		layout.ActiveKind = "level"
		layout.ActiveID = lv.ID
		layout.Variant = "no-rail"
		crumbs := []views.Crumb{{Label: "Levels", Href: "/design/levels"}}
		if worldName != "" {
			crumbs = []views.Crumb{
				{Label: "Worlds", Href: "/design/worlds"},
				{Label: worldName, Href: fmt.Sprintf("/design/worlds/%d", *lv.WorldID)},
			}
		}
		crumbs = append(crumbs, views.Crumb{Label: lv.Name})
		layout.Crumbs = crumbs
		renderHTML(w, r, views.LevelEditorPage(views.LevelEditorProps{
			Layout:         layout,
			Level:          *lv,
			MapName:        mapName,
			MapWidth:       mapWidth,
			MapHeight:      mapHeight,
			WorldName:      worldName,
			PlacementCount: placementCount,
			ActiveTab:      tabFromQuery(r),
		}))
	}
}

func tabFromQuery(r *http.Request) string {
	t := strings.TrimSpace(r.URL.Query().Get("tab"))
	switch t {
	case "geometry", "entities", "hud", "automations", "settings":
		return t
	}
	return "geometry"
}

func deleteLevel(d Deps) http.HandlerFunc {
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
		if err := d.Levels.Delete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Redirect", "/design/levels")
		w.WriteHeader(http.StatusOK)
	}
}

func postLevelPublic(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Levels == nil {
			http.Error(w, "levels service unavailable", http.StatusServiceUnavailable)
			return
		}
		on := parseCheckbox(r.FormValue("public"))
		if err := d.Levels.SetPublic(r.Context(), id, on); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// ---- tilemaps list page ----------------------------------------------

func getTilemapsList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		layout := BuildChrome(r, d)
		layout.Title = "Tilemaps"
		layout.ActiveKind = "tilemap"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{
			{Label: "Library", Href: "/design/library"},
			{Label: "Tilemaps"},
		}
		var items []tilemaps.Tilemap
		if d.Tilemaps != nil {
			search := strings.TrimSpace(r.URL.Query().Get("q"))
			got, err := d.Tilemaps.List(r.Context(), tilemaps.ListOpts{
				Search: search, Limit: 200,
			})
			if err != nil {
				slog.Error("tilemaps list", "err", err)
				http.Error(w, "list tilemaps", http.StatusInternalServerError)
				return
			}
			items = got
		}
		// Resolve backing-asset URLs for thumbnail rendering. Use the
		// designer-realm /design/assets/blob/{id} route so the
		// browser doesn't have to hit the (possibly private) bucket
		// directly — same pattern as the asset grid + mapmaker
		// palette.
		assetURLs := map[int64]string{}
		if d.Assets != nil {
			for _, tm := range items {
				if a, err := d.Assets.FindByID(r.Context(), tm.AssetID); err == nil && a != nil {
					assetURLs[tm.ID] = fmt.Sprintf("/design/assets/blob/%d", a.ID)
				}
			}
		}
		renderHTML(w, r, views.TilemapsListPage(views.TilemapsListProps{
			Layout:    layout,
			Items:     items,
			AssetURLs: assetURLs,
			Search:    strings.TrimSpace(r.URL.Query().Get("q")),
		}))
	}
}

func getTilemapDetail(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Tilemaps == nil {
			http.NotFound(w, r)
			return
		}
		tm, err := d.Tilemaps.FindByID(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// Backing asset URL for the viewer canvas. We point at the
		// designer-realm /design/assets/blob/{id} streamer rather
		// than the object store's public URL — that endpoint uses
		// the designer's session, so it works even when the bucket
		// isn't configured for public-read (typical dev MinIO).
		assetURL := ""
		if d.Assets != nil {
			if a, err := d.Assets.FindByID(r.Context(), tm.AssetID); err == nil && a != nil {
				assetURL = fmt.Sprintf("/design/assets/blob/%d", a.ID)
			}
		}
		// Per-cell entity rows for hover labels. Bulk-resolve names
		// in one query rather than N FindByID calls (a 12×16 grid is
		// 108+ entities).
		cells, _ := d.Tilemaps.Cells(r.Context(), tm.ID)
		entityNames := map[int64]string{}
		if d.Entities != nil && len(cells) > 0 {
			byClass, err := d.Entities.ListByClass(r.Context(), entities.ClassTile, entities.ListOpts{Limit: 4096})
			if err == nil {
				for _, et := range byClass {
					if et.TilemapID != nil && *et.TilemapID == tm.ID {
						entityNames[et.ID] = et.Name
					}
				}
			}
		}
		// Adjacency graph for the optional overlay.
		graph, _ := d.Tilemaps.AdjacencyGraph(r.Context(), tm.ID)
		layout := BuildChrome(r, d)
		layout.Title = tm.Name
		layout.ActiveKind = "tilemap"
		layout.ActiveID = tm.ID
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{
			{Label: "Library", Href: "/design/library"},
			{Label: "Tilemaps", Href: "/design/tilemaps"},
			{Label: tm.Name},
		}
		renderHTML(w, r, views.TilemapViewerPage(views.TilemapViewerProps{
			Layout:      layout,
			Tilemap:     *tm,
			AssetURL:    assetURL,
			Cells:       cells,
			EntityNames: entityNames,
			Adjacency:   graph,
		}))
	}
}

func deleteTilemap(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Tilemaps == nil {
			http.Error(w, "tilemaps service unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := d.Tilemaps.Delete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Redirect", "/design/tilemaps")
		w.WriteHeader(http.StatusOK)
	}
}

// ---- library shell ---------------------------------------------------

// getLibraryPage renders the Library landing page that hosts a
// tabstrip across the four kinds (Sprites / Tilemaps / Audio / UI
// panels). Each kind has its own URL; the bare /design/library
// endpoint redirects to the Sprites tab.
func getLibraryPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/design/library/sprites", http.StatusFound)
	}
}

// getLibraryTab renders one Library tab. The tab id comes from the
// path; the handler scopes the asset list to the matching kind, or
// (for tilemaps) lists from the tilemaps service.
func getLibraryTab(tab string) func(d Deps) http.HandlerFunc {
	return func(d Deps) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			layout := BuildChrome(r, d)
			layout.ActiveKind = libraryActiveKindForTab(tab)
			layout.Variant = "no-rail"
			layout.Title = libraryTabTitle(tab)
			layout.Crumbs = []views.Crumb{
				{Label: "Library", Href: "/design/library"},
				{Label: libraryTabTitle(tab)},
			}
			counts := map[views.LibraryTab]int{}
			props := views.LibraryProps{
				Layout: layout,
				Active: views.LibraryTab(tab),
				Counts: counts,
				PublicURL: func(p string) string {
					if d.ObjectStore == nil {
						return ""
					}
					return d.ObjectStore.PublicURL(p)
				},
			}
			// Counts: cheap List calls per kind. Phase 3 may
			// consolidate these into a single SELECT … GROUP BY kind.
			if d.Assets != nil {
				if items, err := d.Assets.List(ctx, assetsListOptsForKind("sprite")); err == nil {
					counts[views.LibraryTabSprites] = len(items)
					if tab == "sprites" {
						props.Sprites = items
					}
				}
				if items, err := d.Assets.List(ctx, assetsListOptsForKind("audio")); err == nil {
					counts[views.LibraryTabAudio] = len(items)
					if tab == "audio" {
						props.Audio = items
					}
				}
				if items, err := d.Assets.List(ctx, assetsListOptsForKind("ui_panel")); err == nil {
					counts[views.LibraryTabUIPanels] = len(items)
					if tab == "ui-panels" {
						props.UIPanels = items
					}
				}
			}
			if d.Tilemaps != nil {
				if items, err := d.Tilemaps.List(ctx, tilemaps.ListOpts{Limit: 200}); err == nil {
					counts[views.LibraryTabTilemaps] = len(items)
					if tab == "tilemaps" {
						props.Tilemaps = items
						props.TilemapAssetURLs = libraryResolveTilemapURLs(ctx, d, items)
					}
				} else {
					slog.Warn("library: list tilemaps", "err", err)
				}
			}
			renderHTML(w, r, views.LibraryPage(props))
		}
	}
}

func libraryActiveKindForTab(tab string) string {
	switch tab {
	case "sprites":
		return "sprite"
	case "tilemaps":
		return "tilemap"
	case "audio":
		return "audio"
	case "ui-panels":
		return "ui_panel"
	}
	return "asset"
}

func libraryTabTitle(tab string) string {
	switch tab {
	case "sprites":
		return "Sprites"
	case "tilemaps":
		return "Tilemaps"
	case "audio":
		return "Audio"
	case "ui-panels":
		return "UI panels"
	}
	return "Library"
}

// libraryResolveTilemapURLs builds the tilemap_id → backing PNG URL
// map the Library tilemaps tab needs. URLs route through the
// designer-realm /design/assets/blob/{id} streamer so the browser
// doesn't need direct read access to the object-store bucket. One
// asset lookup per tilemap; Phase 3+ may consolidate via a JOIN.
func libraryResolveTilemapURLs(ctx context.Context, d Deps, items []tilemaps.Tilemap) map[int64]string {
	out := make(map[int64]string, len(items))
	if d.Assets == nil {
		return out
	}
	for _, tm := range items {
		if a, err := d.Assets.FindByID(ctx, tm.AssetID); err == nil && a != nil {
			out[tm.ID] = fmt.Sprintf("/design/assets/blob/%d", a.ID)
		}
	}
	return out
}

// assetsListOptsForKind builds a ListOpts that scopes to one asset
// kind. Tiny helper to keep the Library tab handlers tidy.
func assetsListOptsForKind(kind string) assets.ListOpts {
	return assets.ListOpts{
		Kind:  assets.Kind(kind),
		Limit: 200,
	}
}

// ---- per-class entity routes -----------------------------------------

// The four entity-class pages ride on top of the existing entities list
// view; we just pre-filter by class. Phase 3 may give each class its own
// tailored page; for v1 this is enough to surface them as separate IDE
// destinations.

func getEntitiesByClass(class string) func(d Deps) http.HandlerFunc {
	return func(d Deps) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Forward into the existing entities list with a synthetic
			// `class` query param the list view keys off.
			q := r.URL.Query()
			q.Set("class", class)
			r.URL.RawQuery = q.Encode()
			getEntitiesList(d)(w, r)
		}
	}
}
