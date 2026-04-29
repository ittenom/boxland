package designer

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/levels"
	mapsservice "boxland/server/internal/maps"
	"boxland/server/internal/tilemaps"
	"boxland/server/internal/worlds"
	"boxland/server/views"
)

// themeCookieName is read on every full page load to set
// `<html data-theme="...">` server-side, avoiding the one-frame flash
// that a JS-only theme detection would cause. The same cookie is
// written client-side by `bxToggleTheme()` (see layout.templ).
const themeCookieName = "bx_theme"

// treeItemsPerSection bounds how many items the project tree shows per
// kind. Anything beyond this lands behind the "+N more" link to the
// list view. Six is enough to be useful at a glance and small enough
// to keep the chrome quiet.
const treeItemsPerSection = 6

// treeItemsPerSubSection bounds nested entity-class / library-kind
// sub-sections. Smaller than the top-level cap so the column doesn't
// scroll on a project with a healthy mix of kinds.
const treeItemsPerSubSection = 4

// BuildChrome populates the layout props that every shell-bearing
// surface needs: theme, designer, project counts + draft badge, and the
// project tree. Surfaces add their own ActiveKind / ActiveID, Crumbs,
// and (optionally) RailData on top of the result.
//
// All lookups are best-effort. A failed sub-query logs and returns an
// empty section rather than failing the page render — the chrome is a
// navigation aid, not authoritative data.
func BuildChrome(r *http.Request, d Deps) views.LayoutProps {
	ctx := r.Context()
	out := views.LayoutProps{
		Theme:    readThemeCookie(r),
		Designer: CurrentDesigner(ctx),
		Project:  views.ProjectInfo{Name: "my world"},
	}

	// ---- Worlds + Levels + Maps (the canonical hierarchy) -------------

	if d.Worlds != nil {
		ws, err := d.Worlds.List(ctx, worlds.ListOpts{Limit: 200})
		if err != nil {
			slog.Warn("chrome: list worlds", "err", err)
		} else {
			out.Project.WorldCount = len(ws)
			out.Tree.Worlds = makeWorldSection(ws)
		}
	}

	if d.Levels != nil {
		lvs, err := d.Levels.List(ctx, levels.ListOpts{Limit: 200})
		if err != nil {
			slog.Warn("chrome: list levels", "err", err)
		} else {
			out.Project.LevelCount = len(lvs)
			out.Tree.Levels = makeLevelSection(lvs)
		}
	}

	if d.Maps != nil {
		items, err := d.Maps.List(ctx, "")
		if err != nil {
			slog.Warn("chrome: list maps", "err", err)
		} else {
			out.Project.MapCount = len(items)
			out.Tree.Maps = makeMapSection(items)
		}
	}

	// ---- Entities (split by class) -----------------------------------

	if d.Entities != nil {
		// One ListByClass call per class. N+1 by class is fine — there
		// are exactly five classes by construction. Each is bounded by
		// treeItemsPerSubSection.
		out.Tree.TileEntities = makeEntitySectionByClass(ctx, d.Entities, entities.ClassTile, "/design/entities/tiles")
		out.Tree.NPCEntities = makeEntitySectionByClass(ctx, d.Entities, entities.ClassNPC, "/design/entities/npcs")
		out.Tree.PCEntities = makeEntitySectionByClass(ctx, d.Entities, entities.ClassPC, "/design/entities/pcs")
		out.Tree.LogicEntities = makeEntitySectionByClass(ctx, d.Entities, entities.ClassLogic, "/design/entities/logic")
		out.Tree.UIEntities = makeEntitySectionByClass(ctx, d.Entities, entities.ClassUI, "/design/entities/ui")
		out.Project.TileEntityCount = out.Tree.TileEntities.Total
		out.Project.NPCEntityCount = out.Tree.NPCEntities.Total
		out.Project.PCEntityCount = out.Tree.PCEntities.Total
		out.Project.LogicEntityCount = out.Tree.LogicEntities.Total
		out.Project.UIEntityCount = out.Tree.UIEntities.Total
		out.Project.EntityCount =
			out.Project.TileEntityCount + out.Project.NPCEntityCount +
				out.Project.PCEntityCount + out.Project.LogicEntityCount +
				out.Project.UIEntityCount
	}

	// ---- Library kinds -----------------------------------------------

	if d.Assets != nil {
		// Sprites
		if items, err := d.Assets.List(ctx, assets.ListOpts{Kind: assets.KindSprite, Limit: 200}); err != nil {
			slog.Warn("chrome: list sprites", "err", err)
		} else {
			out.Project.SpriteCount = len(items)
			out.Tree.Sprites = makeAssetSection(items, "sprite", "/design/library/sprites")
		}
		// Audio
		if items, err := d.Assets.List(ctx, assets.ListOpts{Kind: assets.KindAudio, Limit: 200}); err != nil {
			slog.Warn("chrome: list audio", "err", err)
		} else {
			out.Project.AudioCount = len(items)
			out.Tree.Audio = makeAssetSection(items, "audio", "/design/library/audio")
		}
		// UI panels
		if items, err := d.Assets.List(ctx, assets.ListOpts{Kind: assets.KindUIPanel, Limit: 200}); err != nil {
			slog.Warn("chrome: list ui panels", "err", err)
		} else {
			out.Project.UIPanelCount = len(items)
			out.Tree.UIPanels = makeAssetSection(items, "ui_panel", "/design/library/ui-panels")
		}
	}

	if d.Tilemaps != nil {
		tms, err := d.Tilemaps.List(ctx, tilemaps.ListOpts{Limit: 200})
		if err != nil {
			slog.Warn("chrome: list tilemaps", "err", err)
		} else {
			out.Project.TilemapCount = len(tms)
			out.Tree.Tilemaps = makeTilemapSection(tms)
		}
	}

	// ---- Drafts + update badge ---------------------------------------

	if d.PublishPipeline != nil {
		if n, err := d.PublishPipeline.CountDrafts(ctx); err == nil {
			out.Project.DraftCount = n
		} else {
			slog.Warn("chrome: count drafts", "err", err)
		}
	}

	if d.Updates != nil {
		if s := d.Updates.Cached(); s != nil && s.HasUpdate {
			out.UpdateBadge = &views.UpdateBadge{
				Current:    s.Current,
				Latest:     s.Latest,
				ReleaseURL: s.ReleaseURL,
			}
		}
	}

	out.Tree.Loaded = true
	return out
}

// readThemeCookie returns "dark" by default, "light" only when the
// cookie is present and equal to "light". Anything else is treated as
// dark to keep behavior deterministic.
func readThemeCookie(r *http.Request) string {
	if r == nil {
		return "dark"
	}
	c, err := r.Cookie(themeCookieName)
	if err != nil || c == nil {
		return "dark"
	}
	if c.Value == "light" {
		return "light"
	}
	return "dark"
}

// ---- per-kind tree builders ----

func makeAssetSection(items []assets.Asset, kindLabel, listHref string) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, a := range items {
		if len(out.Items) >= treeItemsPerSubSection {
			break
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   a.ID,
			Name: a.Name,
			Meta: kindMeta(string(a.Kind)),
			Href: fmt.Sprintf("%s#%d", listHref, a.ID),
		})
	}
	return out
}

func makeTilemapSection(items []tilemaps.Tilemap) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, tm := range items {
		if len(out.Items) >= treeItemsPerSubSection {
			break
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   tm.ID,
			Name: tm.Name,
			Meta: fmt.Sprintf("%d×%d", tm.Cols, tm.Rows),
			Href: fmt.Sprintf("/design/tilemaps/%d", tm.ID),
		})
	}
	return out
}

func makeEntitySectionByClass(ctx context.Context, esvc *entities.Service, class entities.Class, listHref string) views.IndexSection {
	items, err := esvc.ListByClass(ctx, class, entities.ListOpts{Limit: 200})
	if err != nil {
		slog.Warn("chrome: list entities by class", "class", class, "err", err)
		return views.IndexSection{}
	}
	out := views.IndexSection{Total: len(items)}
	for _, e := range items {
		if len(out.Items) >= treeItemsPerSubSection {
			break
		}
		warn := ""
		if class != entities.ClassLogic && e.SpriteAssetID == nil {
			warn = "no sprite assigned"
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   e.ID,
			Name: e.Name,
			Meta: entityMetaCompact(e),
			Warn: warn,
			Href: fmt.Sprintf("/design/entities/%d", e.ID),
		})
	}
	_ = listHref // reserved; sub-section already wires its ListHref
	return out
}

func makeMapSection(items []mapsservice.Map) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, m := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   m.ID,
			Name: m.Name,
			Meta: mapMeta(m),
			Href: fmt.Sprintf("/design/maps/%d", m.ID),
		})
	}
	return out
}

func makeWorldSection(items []worlds.World) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, w := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		warn := ""
		if w.StartLevelID == nil {
			warn = "no start level"
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   w.ID,
			Name: w.Name,
			Warn: warn,
			Href: fmt.Sprintf("/design/worlds/%d", w.ID),
		})
	}
	return out
}

func makeLevelSection(items []levels.Level) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, lv := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		meta := ""
		if lv.Public {
			meta = "public"
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   lv.ID,
			Name: lv.Name,
			Meta: meta,
			Href: fmt.Sprintf("/design/levels/%d", lv.ID),
		})
	}
	return out
}

// ---- meta strings ----

func kindMeta(kind string) string {
	switch kind {
	case "sprite":
		return "spr"
	case "sprite_animated":
		return "anim"
	case "audio":
		return "aud"
	case "ui_panel":
		return "9-slice"
	default:
		return ""
	}
}

// entityMetaCompact returns the small chip on a tree row. We don't
// repeat the class name (the tree section already calls it out); we
// surface the most useful per-entity hint instead — an atlas index
// for tile entities, a tag for npcs, a recipe presence for pcs, a
// tag for logic.
func entityMetaCompact(e entities.EntityType) string {
	switch e.EntityClass {
	case entities.ClassTile:
		if e.CellCol != nil && e.CellRow != nil {
			return fmt.Sprintf("r%dc%d", *e.CellRow, *e.CellCol)
		}
		return "tile"
	case entities.ClassNPC:
		if e.RecipeID == nil {
			return "no recipe"
		}
		return "npc"
	case entities.ClassPC:
		return "pc"
	case entities.ClassUI:
		if len(e.Tags) > 0 {
			return e.Tags[0]
		}
		return "ui"
	default:
		if len(e.Tags) > 0 {
			return e.Tags[0]
		}
		return "logic"
	}
}

func mapMeta(m mapsservice.Map) string {
	if m.Mode == "procedural" {
		return "proc"
	}
	if m.Width == m.Height {
		return strconv.FormatInt(int64(m.Width), 10) + "²"
	}
	return fmt.Sprintf("%d×%d", m.Width, m.Height)
}


