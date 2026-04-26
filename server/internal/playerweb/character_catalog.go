// Boxland — player-facing character catalog.
//
// One round trip per generator open. Returns the slot vocabulary plus
// every part in the requesting player's inventory. The inventory view
// is the audit seam: today every authenticated player sees every live
// `character_parts` row (matches the spec's first slice). When real
// inventory mechanics land — unlocks, purchases, quest rewards — only
// the SQL inside playerInventoryParts changes; the endpoint surface
// stays stable.
//
// Designer-mode parallel: `/design/characters/catalog`. The two endpoints
// share the wire shape so the same generator UI module handles both.
//
// Security:
//   * RequirePlayer guards the route (anonymous users see /play/login).
//   * Player id is read from auth context, never from the body or query.
//   * `character_parts` row visibility is filtered through the inventory
//     join — there's no "show me arbitrary part ids" surface.
//   * Stat sets and talent trees are public per-realm (the player needs
//     them to understand point spends), so they're returned unfiltered
//     once the player is authenticated.

package playerweb

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"boxland/server/internal/characters"
)

// characterCatalogResponse is the player-side wire shape. Mirrors the
// designer-mode catalog so the JS generator module handles both with
// one code path.
type characterCatalogResponse struct {
	Slots       []characterCatalogSlot       `json:"slots"`
	StatSets    []characterCatalogStatSet    `json:"stat_sets"`
	TalentTrees []characterCatalogTalentTree `json:"talent_trees"`
}

type characterCatalogSlot struct {
	ID                int64                    `json:"id"`
	Key               string                   `json:"key"`
	Label             string                   `json:"label"`
	Required          bool                     `json:"required"`
	OrderIndex        int32                    `json:"order_index"`
	DefaultLayerOrder int32                    `json:"default_layer_order"`
	AllowsPalette     bool                     `json:"allows_palette"`
	Parts             []characterCatalogPart   `json:"parts"`
}

type characterCatalogPart struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	AssetID    int64  `json:"asset_id"`
	AssetURL   string `json:"asset_url"`
	LayerOrder *int32 `json:"layer_order,omitempty"`
	FrameMap   any    `json:"frame_map,omitempty"`
}

type characterCatalogStatSet struct {
	ID            int64                    `json:"id"`
	Key           string                   `json:"key"`
	Name          string                   `json:"name"`
	Stats         []characters.StatDef     `json:"stats"`
	CreationRules characters.CreationRules `json:"creation_rules"`
}

type characterCatalogTalentTree struct {
	ID          int64                       `json:"id"`
	Key         string                      `json:"key"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	CurrencyKey string                      `json:"currency_key"`
	LayoutMode  string                      `json:"layout_mode"`
	Nodes       []characterCatalogTalentNode `json:"nodes"`
}

type characterCatalogTalentNode struct {
	Key           string                      `json:"key"`
	Name          string                      `json:"name"`
	Description   string                      `json:"description"`
	MaxRank       int32                       `json:"max_rank"`
	Cost          characters.TalentCost       `json:"cost"`
	Prerequisites []characters.TalentPrereq   `json:"prerequisites"`
	MutexGroup    string                      `json:"mutex_group,omitempty"`
}

// getCharacterCatalog handles GET /play/character-catalog.
func getCharacterCatalog(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Characters == nil {
			http.Error(w, "character catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		p := PlayerFromContext(r.Context())
		if p == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := r.Context()

		// Slots are the same vocabulary the designer sees; they're not
		// player-private and are needed to render the picker.
		slots, err := d.Characters.ListSlots(ctx)
		if err != nil {
			slog.Error("player character catalog: list slots", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Inventory-scoped parts. Today: every live part. Tomorrow:
		// only parts the player has unlocked.
		parts, err := playerInventoryParts(ctx, d.Characters, p.ID)
		if err != nil {
			slog.Error("player character catalog: inventory query", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Batched URL resolution.
		assetIDs := dedupedAssetIDs(parts)
		urlByID := map[int64]string{}
		if len(assetIDs) > 0 && d.Assets != nil && d.ObjectStore != nil {
			rows, err := d.Assets.ListByIDs(ctx, assetIDs)
			if err != nil {
				slog.Error("player character catalog: list assets", "err", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, a := range rows {
				urlByID[a.ID] = d.ObjectStore.PublicURL(a.ContentAddressedPath)
			}
		}

		bySlot := map[int64][]characterCatalogPart{}
		for _, pt := range parts {
			var fm any
			if len(pt.FrameMapJSON) > 0 {
				_ = json.Unmarshal(pt.FrameMapJSON, &fm)
			}
			bySlot[pt.SlotID] = append(bySlot[pt.SlotID], characterCatalogPart{
				ID: pt.ID, Name: pt.Name, AssetID: pt.AssetID,
				AssetURL: urlByID[pt.AssetID], LayerOrder: pt.LayerOrder, FrameMap: fm,
			})
		}

		out := characterCatalogResponse{Slots: make([]characterCatalogSlot, 0, len(slots))}
		for _, s := range slots {
			out.Slots = append(out.Slots, characterCatalogSlot{
				ID: s.ID, Key: s.Key, Label: s.Label, Required: s.Required,
				OrderIndex: s.OrderIndex, DefaultLayerOrder: s.DefaultLayerOrder,
				AllowsPalette: s.AllowsPalette,
				Parts:         bySlot[s.ID],
			})
		}

		// Stat sets + talent trees: public per-realm. Players need them
		// to allocate points + pick talents under published rules.
		statRows, err := d.Characters.ListStatSets(ctx)
		if err != nil {
			slog.Error("player character catalog: list stat sets", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out.StatSets = make([]characterCatalogStatSet, 0, len(statRows))
		for _, sr := range statRows {
			parsed, perr := characters.ParseStatSet(sr)
			if perr != nil {
				slog.Warn("player character catalog: bad stat set", "id", sr.ID, "err", perr)
				out.StatSets = append(out.StatSets, characterCatalogStatSet{ID: sr.ID, Key: sr.Key, Name: sr.Name})
				continue
			}
			out.StatSets = append(out.StatSets, characterCatalogStatSet{
				ID: sr.ID, Key: sr.Key, Name: sr.Name,
				Stats:         parsed.Defs,
				CreationRules: parsed.Rules,
			})
		}

		treeRows, err := d.Characters.ListTalentTrees(ctx)
		if err != nil {
			slog.Error("player character catalog: list talent trees", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		nodesByTreeID, err := d.Characters.LoadTalentNodesGroupedByTree(ctx)
		if err != nil {
			slog.Error("player character catalog: load talent nodes", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out.TalentTrees = make([]characterCatalogTalentTree, 0, len(treeRows))
		for _, tr := range treeRows {
			parsed, perr := characters.ParseTalentTree(tr, nodesByTreeID[tr.ID])
			if perr != nil {
				slog.Warn("player character catalog: bad talent tree", "id", tr.ID, "err", perr)
				continue
			}
			ct := characterCatalogTalentTree{
				ID: tr.ID, Key: tr.Key, Name: tr.Name, Description: tr.Description,
				CurrencyKey: tr.CurrencyKey, LayoutMode: string(tr.LayoutMode),
				Nodes: make([]characterCatalogTalentNode, 0, len(parsed.Nodes)),
			}
			for _, pn := range parsed.Nodes {
				ct.Nodes = append(ct.Nodes, characterCatalogTalentNode{
					Key: pn.Node.Key, Name: pn.Node.Name, Description: pn.Node.Description,
					MaxRank: pn.Node.MaxRank, Cost: pn.Cost, Prerequisites: pn.Prereqs,
					MutexGroup: pn.Node.MutexGroup,
				})
			}
			out.TalentTrees = append(out.TalentTrees, ct)
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=10")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// playerInventoryParts returns the parts the given player is allowed to
// use in the character generator. THIS IS THE AUDIT SEAM: when real
// inventory mechanics land (unlocks, drops, purchases), only the SQL
// inside this function changes — the endpoint surface stays stable.
//
// Phase 4 first cut: every live `character_parts` row is in every
// player's inventory. The argument is unused but kept on the
// signature so callers don't need to be rewired when scoping lands.
func playerInventoryParts(ctx context.Context, svc *characters.Service, _ int64) ([]characters.Part, error) {
	return svc.ListParts(ctx, characters.ListPartsOpts{})
}

// dedupedAssetIDs collects the unique asset ids referenced by the
// supplied parts, in stable insertion order. Tiny helper to keep the
// catalog handler readable.
func dedupedAssetIDs(parts []characters.Part) []int64 {
	seen := make(map[int64]struct{}, len(parts))
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		if _, dup := seen[p.AssetID]; dup {
			continue
		}
		seen[p.AssetID] = struct{}{}
		out = append(out, p.AssetID)
	}
	return out
}

