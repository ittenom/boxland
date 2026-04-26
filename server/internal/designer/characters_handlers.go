// Package designer — character generator handlers.
//
// Phase 1 surface: a /design/characters dashboard plus minimal CRUD
// endpoints for slots, parts, and NPC templates so the designer can
// register parts and create NPC template shells. Edit detail pages
// (the Character Generator itself) land in Phase 2.
//
// Routes are mounted in handlers.go alongside the existing entities
// block. Drafts are persisted via the same inline INSERT into `drafts`
// pattern every other artifact uses — no helper, deliberately.

package designer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"boxland/server/internal/characters"
	"boxland/server/views"
)

// maxRecipeBodyBytes caps the JSON the recipe save endpoint accepts so
// a malicious or accidental huge body can't blow up the server.
// Aligned with characters.MaxRecipeJSONBytes plus a margin for envelope.
const maxRecipeBodyBytes = 64 * 1024

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

// recentNpcLimit caps the "Recent NPC templates" strip on the
// dashboard. Small enough to be a quick at-a-glance — the full list
// lives behind /design/characters/npc-templates.
const recentNpcLimit = 10

func getCharactersList(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Best-effort counts. Each query is independent; a failure
		// degrades the dashboard to a partial view rather than 500ing
		// the whole page.
		slots, err := d.Characters.ListSlots(ctx)
		if err != nil {
			slog.Warn("characters dashboard: list slots", "err", err)
		}
		parts, err := d.Characters.ListParts(ctx, characters.ListPartsOpts{Limit: 1})
		if err != nil {
			slog.Warn("characters dashboard: list parts probe", "err", err)
		}
		_ = parts // we only need a count, but ListParts returns rows; cheaper to count via a separate query
		var partCount int
		if err := d.Characters.Pool.QueryRow(ctx, `SELECT count(*) FROM character_parts`).Scan(&partCount); err != nil {
			slog.Warn("characters dashboard: count parts", "err", err)
		}
		var statSetCount, talentTreeCount int
		_ = d.Characters.Pool.QueryRow(ctx, `SELECT count(*) FROM character_stat_sets`).Scan(&statSetCount)
		_ = d.Characters.Pool.QueryRow(ctx, `SELECT count(*) FROM character_talent_trees`).Scan(&talentTreeCount)

		npcs, err := d.Characters.ListNpcTemplates(ctx)
		if err != nil {
			slog.Warn("characters dashboard: list npc templates", "err", err)
		}
		recent := npcs
		if len(recent) > recentNpcLimit {
			recent = recent[:recentNpcLimit]
		}

		layout := BuildChrome(r, d)
		layout.Title = "Characters"
		layout.Surface = "characters"
		layout.ActiveKind = "character"
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{{Label: "Characters"}}
		renderHTML(w, r, views.CharactersList(views.CharactersListProps{
			Layout:             layout,
			SlotCount:          len(slots),
			PartCount:          partCount,
			StatSetCount:       statSetCount,
			TalentTreeCount:    talentTreeCount,
			NpcTemplateCount:   len(npcs),
			RecentNpcTemplates: recent,
		}))
	}
}

// ---------------------------------------------------------------------------
// Slots
// ---------------------------------------------------------------------------

// postCharacterSlot creates a new designer-authored slot. Phase 1
// returns plain text; the dashboard refresh / list page lands later.
func postCharacterSlot(d Deps) http.HandlerFunc {
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
		in := characters.CreateSlotInput{
			Key:               strings.TrimSpace(r.FormValue("key")),
			Label:             strings.TrimSpace(r.FormValue("label")),
			Required:          r.FormValue("required") == "on",
			OrderIndex:        int32(parseIntOr(r.FormValue("order_index"), 0)),
			DefaultLayerOrder: int32(parseIntOr(r.FormValue("default_layer_order"), 0)),
			AllowsPalette:     r.FormValue("allows_palette") == "on",
			CreatedBy:         dr.ID,
		}
		_, err := d.Characters.CreateSlot(r.Context(), in)
		if err != nil {
			if errors.Is(err, characters.ErrKeyInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("create character slot", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}
}

// deleteCharacterSlot removes a slot.
func deleteCharacterSlot(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Characters.DeleteSlot(r.Context(), id); err != nil {
			if errors.Is(err, characters.ErrSlotNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("delete character slot", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// postCharacterSlotDraft upserts a draft of a slot. Same inline UPSERT
// pattern as every other artifact's draft endpoint.
func postCharacterSlotDraft(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft := characters.SlotDraft{
			Key:               strings.TrimSpace(r.FormValue("key")),
			Label:             strings.TrimSpace(r.FormValue("label")),
			Required:          r.FormValue("required") == "on",
			OrderIndex:        int32(parseIntOr(r.FormValue("order_index"), 0)),
			DefaultLayerOrder: int32(parseIntOr(r.FormValue("default_layer_order"), 0)),
			AllowsPalette:     r.FormValue("allows_palette") == "on",
		}
		if err := draft.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := json.Marshal(draft)
		if _, err := d.Characters.Pool.Exec(r.Context(), `
			INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (artifact_kind, artifact_id) DO UPDATE
			SET draft_json = EXCLUDED.draft_json, updated_at = now()
		`, "character_slot", id, body, dr.ID); err != nil {
			slog.Error("character_slot draft save", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeDraftSavedToast(w, "character_slot")
	}
}

// ---------------------------------------------------------------------------
// Parts
// ---------------------------------------------------------------------------

// postCharacterPart creates a new part registration.
func postCharacterPart(d Deps) http.HandlerFunc {
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
		slotID := int64(parseIntOr(r.FormValue("slot_id"), 0))
		assetID := int64(parseIntOr(r.FormValue("asset_id"), 0))
		in := characters.CreatePartInput{
			SlotID:    slotID,
			AssetID:   assetID,
			Name:      strings.TrimSpace(r.FormValue("name")),
			Tags:      parseTags(r.FormValue("tags")),
			CreatedBy: dr.ID,
		}
		_, err := d.Characters.CreatePart(r.Context(), in)
		if err != nil {
			if errors.Is(err, characters.ErrKeyInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("create character part", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}
}

// deleteCharacterPart removes a part.
func deleteCharacterPart(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Characters.DeletePart(r.Context(), id); err != nil {
			if errors.Is(err, characters.ErrPartNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("delete character part", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// postCharacterPartDraft upserts a part draft.
func postCharacterPartDraft(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft := characters.PartDraft{
			SlotID:  int64(parseIntOr(r.FormValue("slot_id"), 0)),
			AssetID: int64(parseIntOr(r.FormValue("asset_id"), 0)),
			Name:    strings.TrimSpace(r.FormValue("name")),
			Tags:    parseTags(r.FormValue("tags")),
		}
		// FrameMapJSON arrives as a raw JSON blob from the generator UI.
		// In Phase 1 the form is missing; default to {} so handler-side
		// validation passes.
		if fm := strings.TrimSpace(r.FormValue("frame_map_json")); fm != "" {
			draft.FrameMapJSON = json.RawMessage(fm)
		} else {
			draft.FrameMapJSON = json.RawMessage(`{}`)
		}
		if err := draft.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := json.Marshal(draft)
		if _, err := d.Characters.Pool.Exec(r.Context(), `
			INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (artifact_kind, artifact_id) DO UPDATE
			SET draft_json = EXCLUDED.draft_json, updated_at = now()
		`, "character_part", id, body, dr.ID); err != nil {
			slog.Error("character_part draft save", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeDraftSavedToast(w, "character_part")
	}
}

// ---------------------------------------------------------------------------
// NPC templates
// ---------------------------------------------------------------------------

// getCharacterNpcTemplateNewModal renders a tiny "create new NPC template"
// form — the same minimal pattern getEntityNewModal uses. The detail
// editor (Generator) lands in Phase 2.
func getCharacterNpcTemplateNewModal(_ Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<div class="bx-modal-backdrop" data-bx-dismissible role="dialog" aria-modal="true">
  <div class="bx-modal">
    <header class="bx-modal__header">
      <h2 data-copy-slot="characters.npc.new.title">New NPC template</h2>
      <button type="button" class="bx-btn bx-btn--ghost"
              hx-on:click="this.closest('.bx-modal-backdrop').remove()"
              aria-label="Close">Esc</button>
    </header>
    <div class="bx-modal__body">
      <form hx-post="/design/characters/npc-templates" hx-target="this" hx-swap="outerHTML"
            hx-on:htmx:after-request="this.closest('.bx-modal-backdrop')?.remove()"
            class="bx-stack">
        <div class="bx-field">
          <label for="new-npc-name" class="bx-label" data-copy-slot="characters.npc.new.name">Name</label>
          <input id="new-npc-name" name="name" class="bx-input" required maxlength="128" autofocus>
        </div>
        <div class="bx-row bx-row--end">
          <button type="submit" class="bx-btn bx-btn--primary" data-copy-slot="characters.npc.new.submit">Create</button>
        </div>
      </form>
    </div>
  </div>
</div>
`))
	}
}

// postCharacterNpcTemplate creates a new NPC template shell. On
// success returns an HX-Redirect header so HTMX bounces the designer
// straight into the Generator for the new template.
func postCharacterNpcTemplate(d Deps) http.HandlerFunc {
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
		row, err := d.Characters.CreateNpcTemplate(r.Context(), characters.CreateNpcTemplateInput{
			Name:      strings.TrimSpace(r.FormValue("name")),
			Tags:      parseTags(r.FormValue("tags")),
			CreatedBy: dr.ID,
		})
		if err != nil {
			if errors.Is(err, characters.ErrNameInUse) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("create npc template", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// HTMX-friendly redirect into the Generator. Plain HTTP clients
		// (and our tests) get the 201 + JSON.
		target := fmt.Sprintf("/design/characters/generator/%d", row.ID)
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":` + intStr64(row.ID) + `,"href":"` + target + `"}`))
	}
}

// intStr64 stringifies an int64 in decimal. Local helper to avoid pulling
// strconv into the test path.
func intStr64(n int64) string {
	return fmt.Sprintf("%d", n)
}

// deleteCharacterNpcTemplate removes a template.
func deleteCharacterNpcTemplate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Characters.DeleteNpcTemplate(r.Context(), id); err != nil {
			if errors.Is(err, characters.ErrNpcTemplateNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("delete npc template", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// Generator page
// ---------------------------------------------------------------------------

// getCharacterGeneratorPage renders the designer-mode Character Generator
// for an existing NPC template. Returns 404 if the template doesn't exist.
func getCharacterGeneratorPage(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		tmpl, err := d.Characters.FindNpcTemplateByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, characters.ErrNpcTemplateNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("get generator page", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		layout := BuildChrome(r, d)
		layout.Title = "Generator: " + tmpl.Name
		layout.Surface = "character-generator"
		layout.ActiveKind = "character"
		layout.ActiveID = tmpl.ID
		layout.Variant = "no-rail"
		layout.Crumbs = []views.Crumb{
			{Label: "Characters", Href: "/design/characters"},
			{Label: tmpl.Name},
		}
		var recipeID int64
		if tmpl.RecipeID != nil {
			recipeID = *tmpl.RecipeID
		}
		renderHTML(w, r, views.CharacterGenerator(views.CharacterGeneratorProps{
			Layout:   layout,
			Template: *tmpl,
			RecipeID: recipeID,
		}))
	}
}

// ---------------------------------------------------------------------------
// Recipe + catalog endpoints (consumed by the Generator UI)
// ---------------------------------------------------------------------------

// recipePayload is the JSON shape the generator UI POSTs for save and
// the GET endpoint returns for load. Mirrors the typed selection
// shapes in characters/recipes.go so the client doesn't need to know
// about the DB column layout.
type recipePayload struct {
	ID         int64                          `json:"id,omitempty"`
	Name       string                         `json:"name"`
	OwnerKind  string                         `json:"owner_kind,omitempty"`
	OwnerID    int64                          `json:"owner_id,omitempty"`
	Appearance characters.AppearanceSelection `json:"appearance"`
	Stats      characters.StatSelection       `json:"stats"`
	Talents    characters.TalentSelection     `json:"talents"`
}

// catalogResponse is the JSON shape the generator UI fetches once at
// boot to populate slot/part pickers. Includes only published-eligible
// content (Phase 3 = every live row; Phase 4 will add player scoping
// for the player-mode endpoint).
type catalogResponse struct {
	Slots       []catalogSlot       `json:"slots"`
	StatSets    []catalogStatSet    `json:"stat_sets"`
	TalentTrees []catalogTalentTree `json:"talent_trees"`
}

type catalogSlot struct {
	ID                int64        `json:"id"`
	Key               string       `json:"key"`
	Label             string       `json:"label"`
	Required          bool         `json:"required"`
	OrderIndex        int32        `json:"order_index"`
	DefaultLayerOrder int32        `json:"default_layer_order"`
	AllowsPalette     bool         `json:"allows_palette"`
	Parts             []catalogPart `json:"parts"`
}

type catalogPart struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	AssetID    int64  `json:"asset_id"`
	AssetURL   string `json:"asset_url"`
	LayerOrder *int32 `json:"layer_order,omitempty"`
	FrameMap   any    `json:"frame_map,omitempty"`
}

// catalogStatSet ships the parsed StatDefs + creation rules so the UI
// can render the allocator without a second round trip.
type catalogStatSet struct {
	ID            int64                  `json:"id"`
	Key           string                 `json:"key"`
	Name          string                 `json:"name"`
	Stats         []characters.StatDef   `json:"stats"`
	CreationRules characters.CreationRules `json:"creation_rules"`
}

// catalogTalentTree ships a tree + its nodes (with parsed cost /
// prereqs / mutex / max_rank) for the talent picker.
type catalogTalentTree struct {
	ID          int64                `json:"id"`
	Key         string               `json:"key"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	CurrencyKey string               `json:"currency_key"`
	LayoutMode  string               `json:"layout_mode"`
	Nodes       []catalogTalentNode  `json:"nodes"`
}

type catalogTalentNode struct {
	Key           string                  `json:"key"`
	Name          string                  `json:"name"`
	Description   string                  `json:"description"`
	MaxRank       int32                   `json:"max_rank"`
	Cost          characters.TalentCost   `json:"cost"`
	Prerequisites []characters.TalentPrereq `json:"prerequisites"`
	MutexGroup    string                  `json:"mutex_group,omitempty"`
}

// getCharacterCatalog returns the slot vocabulary + every registered
// part, grouped by slot. One round trip per generator open.
func getCharacterCatalog(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		slots, err := d.Characters.ListSlots(ctx)
		if err != nil {
			slog.Error("character catalog: list slots", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// One unfiltered list query — typical part counts are small;
		// Phase 4's player catalog endpoint adds the inventory join.
		parts, err := d.Characters.ListParts(ctx, characters.ListPartsOpts{})
		if err != nil {
			slog.Error("character catalog: list parts", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Resolve asset URLs in one batched query so the UI doesn't have
		// to round-trip per part.
		assetIDs := make([]int64, 0, len(parts))
		seen := make(map[int64]struct{}, len(parts))
		for _, p := range parts {
			if _, dup := seen[p.AssetID]; dup {
				continue
			}
			seen[p.AssetID] = struct{}{}
			assetIDs = append(assetIDs, p.AssetID)
		}
		urlByID := make(map[int64]string, len(assetIDs))
		if len(assetIDs) > 0 && d.Assets != nil && d.ObjectStore != nil {
			rows, err := d.Assets.ListByIDs(ctx, assetIDs)
			if err != nil {
				slog.Error("character catalog: list assets", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, a := range rows {
				urlByID[a.ID] = d.ObjectStore.PublicURL(a.ContentAddressedPath)
			}
		}
		// Bucket parts by slot for stable ordering.
		bySlot := make(map[int64][]catalogPart, len(slots))
		for _, p := range parts {
			var fm any
			if len(p.FrameMapJSON) > 0 {
				_ = json.Unmarshal(p.FrameMapJSON, &fm)
			}
			bySlot[p.SlotID] = append(bySlot[p.SlotID], catalogPart{
				ID:         p.ID,
				Name:       p.Name,
				AssetID:    p.AssetID,
				AssetURL:   urlByID[p.AssetID],
				LayerOrder: p.LayerOrder,
				FrameMap:   fm,
			})
		}
		out := catalogResponse{Slots: make([]catalogSlot, 0, len(slots))}
		for _, s := range slots {
			out.Slots = append(out.Slots, catalogSlot{
				ID: s.ID, Key: s.Key, Label: s.Label, Required: s.Required,
				OrderIndex: s.OrderIndex, DefaultLayerOrder: s.DefaultLayerOrder,
				AllowsPalette: s.AllowsPalette,
				Parts:         bySlot[s.ID],
			})
		}

		// Stat sets — small list; one query, parse each row's JSON.
		statRows, err := d.Characters.ListStatSets(ctx)
		if err != nil {
			slog.Error("character catalog: list stat sets", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out.StatSets = make([]catalogStatSet, 0, len(statRows))
		for _, sr := range statRows {
			parsed, perr := characters.ParseStatSet(sr)
			if perr != nil {
				// Surface the bad set as an empty entry rather than
				// failing the whole catalog — designers expect a
				// dashboard, not a 500, when one row is malformed.
				slog.Warn("character catalog: bad stat set", "id", sr.ID, "err", perr)
				out.StatSets = append(out.StatSets, catalogStatSet{ID: sr.ID, Key: sr.Key, Name: sr.Name})
				continue
			}
			out.StatSets = append(out.StatSets, catalogStatSet{
				ID: sr.ID, Key: sr.Key, Name: sr.Name,
				Stats:         parsed.Defs,
				CreationRules: parsed.Rules,
			})
		}

		// Talent trees + their nodes. Two batched queries.
		treeRows, err := d.Characters.ListTalentTrees(ctx)
		if err != nil {
			slog.Error("character catalog: list talent trees", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		nodesByTreeID, err := d.Characters.LoadTalentNodesGroupedByTree(ctx)
		if err != nil {
			slog.Error("character catalog: load talent nodes", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out.TalentTrees = make([]catalogTalentTree, 0, len(treeRows))
		for _, tr := range treeRows {
			parsed, perr := characters.ParseTalentTree(tr, nodesByTreeID[tr.ID])
			if perr != nil {
				slog.Warn("character catalog: bad talent tree", "id", tr.ID, "err", perr)
				continue
			}
			ct := catalogTalentTree{
				ID: tr.ID, Key: tr.Key, Name: tr.Name, Description: tr.Description,
				CurrencyKey: tr.CurrencyKey, LayoutMode: string(tr.LayoutMode),
				Nodes: make([]catalogTalentNode, 0, len(parsed.Nodes)),
			}
			for _, pn := range parsed.Nodes {
				ct.Nodes = append(ct.Nodes, catalogTalentNode{
					Key: pn.Node.Key, Name: pn.Node.Name, Description: pn.Node.Description,
					MaxRank: pn.Node.MaxRank, Cost: pn.Cost, Prerequisites: pn.Prereqs,
					MutexGroup: pn.Node.MutexGroup,
				})
			}
			out.TalentTrees = append(out.TalentTrees, ct)
		}

		writeJSON(w, http.StatusOK, out)
	}
}

// getCharacterRecipe returns one recipe by id. Designer-mode only:
// designer rows are returned as-is; player rows are forbidden so a
// designer can't peek at private player content via this endpoint.
func getCharacterRecipe(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		row, err := d.Characters.FindRecipeByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, characters.ErrRecipeNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("get recipe", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if row.OwnerKind != characters.OwnerKindDesigner {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		out := recipePayload{
			ID:        row.ID,
			Name:      row.Name,
			OwnerKind: string(row.OwnerKind),
			OwnerID:   row.OwnerID,
		}
		_ = json.Unmarshal(row.AppearanceJSON, &out.Appearance)
		_ = json.Unmarshal(row.StatsJSON, &out.Stats)
		_ = json.Unmarshal(row.TalentsJSON, &out.Talents)
		writeJSON(w, http.StatusOK, out)
	}
}

// postCharacterRecipe creates a new designer-owned recipe. Returns the
// new id so the UI can immediately PATCH-style save into it.
func postCharacterRecipe(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecipeBodyBytes))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var in recipePayload
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		appearance, _ := json.Marshal(in.Appearance)
		stats, _ := json.Marshal(in.Stats)
		talents, _ := json.Marshal(in.Talents)
		row, err := d.Characters.CreateRecipe(r.Context(), characters.CreateRecipeInput{
			OwnerKind: characters.OwnerKindDesigner,
			OwnerID:   dr.ID,
			Name:      in.Name,
			AppearanceJSON: appearance,
			StatsJSON:      stats,
			TalentsJSON:    talents,
			CreatedBy:      dr.ID,
		})
		if err != nil {
			slog.Error("create recipe", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, recipePayload{
			ID: row.ID, Name: row.Name,
			OwnerKind: string(row.OwnerKind), OwnerID: row.OwnerID,
			Appearance: in.Appearance, Stats: in.Stats, Talents: in.Talents,
		})
	}
}

// updateCharacterRecipe rewrites an existing designer recipe's
// appearance/stats/talents/name. Cross-designer writes are forbidden:
// even though both rows have OwnerKind=designer, the OwnerID must
// match the requesting designer.
func updateCharacterRecipe(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRecipeBodyBytes))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var in recipePayload
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		appearance, _ := json.Marshal(in.Appearance)
		stats, _ := json.Marshal(in.Stats)
		talents, _ := json.Marshal(in.Talents)
		row, err := d.Characters.UpdateRecipe(r.Context(), characters.UpdateRecipeInput{
			ID:             id,
			OwnerKind:      characters.OwnerKindDesigner,
			OwnerID:        dr.ID,
			Name:           in.Name,
			AppearanceJSON: appearance,
			StatsJSON:      stats,
			TalentsJSON:    talents,
		})
		if err != nil {
			switch {
			case errors.Is(err, characters.ErrRecipeNotFound), errors.Is(err, characters.ErrForbidden):
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("update recipe", "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, recipePayload{
			ID: row.ID, Name: row.Name,
			OwnerKind: string(row.OwnerKind), OwnerID: row.OwnerID,
			Appearance: in.Appearance, Stats: in.Stats, Talents: in.Talents,
		})
	}
}

// postCharacterNpcTemplateAttachRecipe links a template to a recipe.
// Used by the generator UI's "save & link" path. Doesn't bake — that
// happens on the next publish.
func postCharacterNpcTemplateAttachRecipe(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if CurrentDesigner(r.Context()) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
		if err != nil {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var in struct {
			RecipeID int64 `json:"recipe_id"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if in.RecipeID <= 0 {
			http.Error(w, "recipe_id is required", http.StatusBadRequest)
			return
		}
		// Verify the recipe exists + is designer-owned before linking.
		recipe, err := d.Characters.FindRecipeByID(r.Context(), in.RecipeID)
		if err != nil || recipe.OwnerKind != characters.OwnerKindDesigner {
			http.Error(w, "recipe not found", http.StatusNotFound)
			return
		}
		if err := d.Characters.AttachRecipeToNpcTemplate(r.Context(), id, in.RecipeID); err != nil {
			if errors.Is(err, characters.ErrNpcTemplateNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("attach recipe", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeJSON emits a JSON body with the right Content-Type and status.
// Local helper so we don't pull in a third-party render package.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// postCharacterNpcTemplateDraft upserts an NPC template draft.
func postCharacterNpcTemplateDraft(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dr := CurrentDesigner(r.Context())
		if dr == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft := characters.NpcTemplateDraft{
			Name: strings.TrimSpace(r.FormValue("name")),
			Tags: parseTags(r.FormValue("tags")),
		}
		if v := strings.TrimSpace(r.FormValue("recipe_id")); v != "" {
			if rid, err := strconvAtoi64(v); err == nil {
				draft.RecipeID = &rid
			}
		}
		if v := strings.TrimSpace(r.FormValue("entity_type_id")); v != "" {
			if eid, err := strconvAtoi64(v); err == nil {
				draft.EntityTypeID = &eid
			}
		}
		if err := draft.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := json.Marshal(draft)
		if _, err := d.Characters.Pool.Exec(r.Context(), `
			INSERT INTO drafts (artifact_kind, artifact_id, draft_json, created_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (artifact_kind, artifact_id) DO UPDATE
			SET draft_json = EXCLUDED.draft_json, updated_at = now()
		`, "npc_template", id, body, dr.ID); err != nil {
			slog.Error("npc_template draft save", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeDraftSavedToast(w, "npc_template")
	}
}
