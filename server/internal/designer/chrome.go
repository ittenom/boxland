package designer

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"boxland/server/internal/assets"
	"boxland/server/internal/characters"
	"boxland/server/internal/entities"
	mapsservice "boxland/server/internal/maps"
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

	// Asset list (also populates count + first 6 sample items)
	if d.Assets != nil {
		items, err := d.Assets.List(ctx, assets.ListOpts{Limit: 200})
		if err != nil {
			slog.Warn("chrome: list assets", "err", err)
		} else {
			out.Project.AssetCount = len(items)
			out.Tree.Assets = makeAssetSection(items)
		}
	}

	// Entity types
	if d.Entities != nil {
		items, err := d.Entities.List(ctx, entities.ListOpts{Limit: 200})
		if err != nil {
			slog.Warn("chrome: list entities", "err", err)
		} else {
			out.Project.EntityCount = len(items)
			out.Tree.Entities = makeEntitySection(items)
		}

		// Edge sockets and tile groups are "small" lists. The tree
		// shows them but doesn't break them out by name in chrome
		// counts (kept for the tree section only).
		if sockets, err := d.Entities.ListSockets(ctx); err == nil {
			out.Project.SocketCount = len(sockets)
			out.Tree.Sockets = makeSocketSection(sockets)
		} else {
			slog.Warn("chrome: list sockets", "err", err)
		}
		if groups, err := d.Entities.ListTileGroups(ctx); err == nil {
			out.Project.GroupCount = len(groups)
			out.Tree.Groups = makeGroupSection(groups)
		} else {
			slog.Warn("chrome: list tile groups", "err", err)
		}
	}

	// Maps
	if d.Maps != nil {
		items, err := d.Maps.List(ctx, "")
		if err != nil {
			slog.Warn("chrome: list maps", "err", err)
		} else {
			out.Project.MapCount = len(items)
			out.Tree.Maps = makeMapSection(items)
		}
	}

	// Characters (NPC templates power the tree section; full counts
	// also include slots + parts but the tree shows NPC templates as
	// the most-actionable rollup).
	if d.Characters != nil {
		items, err := d.Characters.ListNpcTemplates(ctx)
		if err != nil {
			slog.Warn("chrome: list npc templates", "err", err)
		} else {
			out.Project.CharacterCount = len(items)
			out.Tree.Characters = makeCharacterSection(items)
		}
	}

	// Pending drafts badge
	if d.PublishPipeline != nil {
		if n, err := d.PublishPipeline.CountDrafts(ctx); err == nil {
			out.Project.DraftCount = n
		} else {
			slog.Warn("chrome: count drafts", "err", err)
		}
	}

	// Update pill. Cached-only read — the TLI is the only place
	// that refreshes the GitHub probe, so a designer hitting the
	// page 100x in a session never spends a quota call.
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

func makeAssetSection(items []assets.Asset) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, a := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   a.ID,
			Name: a.Name,
			Meta: kindMeta(string(a.Kind)),
			Href: fmt.Sprintf("/design/assets#%d", a.ID), // anchor-scrolls to the asset card
		})
	}
	return out
}

func makeEntitySection(items []entities.EntityType) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, e := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		warn := ""
		if e.SpriteAssetID == nil {
			warn = "no sprite assigned"
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   e.ID,
			Name: e.Name,
			Meta: entityMeta(e),
			Warn: warn,
			Href: fmt.Sprintf("/design/entities/%d", e.ID),
		})
	}
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

func makeSocketSection(items []entities.EdgeSocketType) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, s := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   s.ID,
			Name: s.Name,
			Href: "/design/sockets",
		})
	}
	return out
}

func makeGroupSection(items []entities.TileGroup) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, g := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   g.ID,
			Name: g.Name,
			Meta: fmt.Sprintf("%d×%d", g.Width, g.Height),
			Href: fmt.Sprintf("/design/tile-groups/%d", g.ID),
		})
	}
	return out
}

func makeCharacterSection(items []characters.NpcTemplate) views.IndexSection {
	out := views.IndexSection{Total: len(items)}
	for _, n := range items {
		if len(out.Items) >= treeItemsPerSection {
			break
		}
		warn := ""
		if n.ActiveBakeID == nil {
			warn = "no bake yet"
		}
		out.Items = append(out.Items, views.IndexItem{
			ID:   n.ID,
			Name: n.Name,
			Meta: "npc",
			Warn: warn,
			Href: fmt.Sprintf("/design/characters/npc-templates/%d", n.ID),
		})
	}
	return out
}

// ---- meta strings ----

func kindMeta(kind string) string {
	switch kind {
	case "sprite":
		return "spr"
	case "tile":
		return "til"
	case "audio":
		return "aud"
	default:
		return ""
	}
}

func entityMeta(e entities.EntityType) string {
	for _, t := range e.Tags {
		switch t {
		case "tile", "npc", "item", "trigger":
			return t
		}
	}
	if len(e.Tags) > 0 {
		return e.Tags[0]
	}
	return "ent"
}

func mapMeta(m mapsservice.Map) string {
	if m.Mode == "procedural" {
		return "proc"
	}
	// Compact "64²" if square, else "64×96"
	if m.Width == m.Height {
		return strconv.FormatInt(int64(m.Width), 10) + "²"
	}
	return fmt.Sprintf("%d×%d", m.Width, m.Height)
}
