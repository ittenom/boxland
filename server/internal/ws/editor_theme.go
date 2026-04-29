package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"boxland/server/internal/assets"
	"boxland/server/internal/entities"
	"boxland/server/internal/entities/components"
)

// editor_theme.go — server-side role table + theme assembler.
//
// The editor's UI primitive layer references widgets by *role*
// (semantic name like "button_md_release_a"). The server builds
// the role → entity_type binding here, then ships it as part of
// the EditorSnapshot. Roles are stable across the codebase; the
// role-to-canonical-entity-name map below is the source of truth.
//
// Why server-side: the snapshot is the only place we know which
// ClassUI entity_types the operator has seeded. The client-side
// `Roles` constant in web/src/render/ui/theme.ts mirrors these
// names so TS callers get autocomplete.

// uiRoleAliases maps role id -> the lower-snake entity_type name
// the seeder produces for the matching sprite. The client's
// `Roles` constant uses the same role ids; both sides agree on
// the names by code review.
//
// Adding a new role: write a new entry here, mirror the constant
// in `web/src/render/ui/theme.ts:Roles`, and wire any new widget
// that uses the role on both sides.
var uiRoleAliases = map[string]string{
	// Frames.
	"frame_standard":   "ui_gradient_frame_standard",
	"frame_lite":       "ui_gradient_frame_lite",
	"frame_inward":     "ui_gradient_frame_inward",
	"frame_outward":    "ui_gradient_frame_outward",
	"frame_horizontal": "ui_gradient_frame_horizontal",
	"frame_vertical":   "ui_gradient_frame_vertical",

	// Buttons (frame "01a1" = the at-rest pose).
	"button_sm_release_a": "ui_gradient_button_small_release_01a1",
	"button_sm_press_a":   "ui_gradient_button_small_press_01a1",
	"button_sm_lock_a":    "ui_gradient_button_small_lock_01a1",
	"button_md_release_a": "ui_gradient_button_medium_release_01a1",
	"button_md_press_a":   "ui_gradient_button_medium_press_01a1",
	"button_md_lock_a":    "ui_gradient_button_medium_lock_01a1",
	"button_lg_release_a": "ui_gradient_button_large_release_01a1",
	"button_lg_press_a":   "ui_gradient_button_large_press_01a1",
	"button_lg_lock_a":    "ui_gradient_button_large_lock_01a1",

	// Form inputs.
	"textfield":       "ui_gradient_textfield",
	"dropdown_bar":    "ui_gradient_dropdown_bar",
	"dropdown_handle": "ui_gradient_dropdown_handle",
	"slider_bar":      "ui_gradient_slider_bar",
	"slider_filler":   "ui_gradient_slider_filler",
	"slider_handle":   "ui_gradient_slider_handle",
	"scroll_bar":      "ui_gradient_scroll_bar",
	"scroll_handle":   "ui_gradient_scroll_handle",
	"fill_bar":        "ui_gradient_fill_bar",
	"fill_filler":     "ui_gradient_fill_filler",

	// Inventory + selection slots.
	"slot_available":   "ui_gradient_slot_available",
	"slot_selected":    "ui_gradient_slot_selected",
	"slot_unavailable": "ui_gradient_slot_unavailable",

	// Decorative + indicators.
	"banner":        "ui_gradient_banner",
	"arrow_sm":      "ui_gradient_arrow_small",
	"arrow_md":      "ui_gradient_arrow_medium",
	"arrow_lg":      "ui_gradient_arrow_large",
	"checkmark_sm":  "ui_gradient_checkmark_small",
	"checkmark_md":  "ui_gradient_checkmark_medium",
	"checkmark_lg":  "ui_gradient_checkmark_large",
	"cross_sm":      "ui_gradient_cross_small",
	"cross_md":      "ui_gradient_cross_medium",
	"cross_lg":      "ui_gradient_cross_large",
}

// EditorThemeEntry is the wire shape one role binding takes. Mirrors
// the client's `ThemeEntry` interface in web/src/render/ui/theme.ts.
type EditorThemeEntry struct {
	Role         string             `json:"role"`
	EntityTypeID int64              `json:"entity_type_id"`
	AssetURL     string             `json:"asset_url"`
	NineSlice    EditorThemeInsets  `json:"nine_slice"`
	Width        int32              `json:"width"`
	Height       int32              `json:"height"`
}

// EditorThemeInsets is the JSON-friendly version of components.NineSlice.
// We use the same field names the client expects.
type EditorThemeInsets struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
}

// uiPackAssetMetadata mirrors setup.uiPackAssetMetadata so we can
// decode the asset's pixel dimensions back out of MetadataJSON
// without a circular dependency on the setup package.
type uiPackAssetMetadata struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Source string `json:"source"`
}

// BuildEditorTheme returns one ThemeEntry per role-bound entity_type.
// Roles whose canonical entity name doesn't exist in the DB are
// quietly omitted — the editor degrades gracefully (a missing role
// renders as a placeholder). One bulk lookup; no N+1.
func BuildEditorTheme(
	ctx context.Context,
	es *entities.Service,
	as *assets.Service,
) ([]EditorThemeEntry, error) {
	if es == nil || as == nil {
		return nil, fmt.Errorf("editor_theme: entities and assets services required")
	}
	uiClass, err := es.ListByClass(ctx, entities.ClassUI, entities.ListOpts{Limit: 4096})
	if err != nil {
		return nil, fmt.Errorf("list ui entity_types: %w", err)
	}
	if len(uiClass) == 0 {
		return nil, nil
	}
	// Build name -> entity_type.
	byName := make(map[string]entities.EntityType, len(uiClass))
	assetIDs := make([]int64, 0, len(uiClass))
	seen := make(map[int64]struct{}, len(uiClass))
	for _, et := range uiClass {
		byName[strings.ToLower(et.Name)] = et
		if et.SpriteAssetID == nil {
			continue
		}
		if _, ok := seen[*et.SpriteAssetID]; ok {
			continue
		}
		seen[*et.SpriteAssetID] = struct{}{}
		assetIDs = append(assetIDs, *et.SpriteAssetID)
	}
	// Bulk asset lookup so we can resolve URLs + dims without N+1.
	assetByID := make(map[int64]assets.Asset, len(assetIDs))
	if len(assetIDs) > 0 {
		rows, err := as.ListByIDs(ctx, assetIDs)
		if err != nil {
			return nil, fmt.Errorf("list assets by id: %w", err)
		}
		for _, a := range rows {
			assetByID[a.ID] = a
		}
	}
	// Bulk component lookup for nine_slice insets. Components()
	// is per-entity-type; the loop is bounded by the number of
	// UI entity_types, which is small (~70 in the seeded pack).
	out := make([]EditorThemeEntry, 0, len(uiRoleAliases))
	for role, name := range uiRoleAliases {
		et, ok := byName[name]
		if !ok {
			// Role not seeded yet (or the operator renamed it).
			continue
		}
		if et.SpriteAssetID == nil {
			continue
		}
		asset, ok := assetByID[*et.SpriteAssetID]
		if !ok {
			continue
		}
		insets, ok := loadNineSliceInsets(ctx, es, et.ID)
		if !ok {
			// Without insets we can't 9-slice safely. Use 1px
			// fallback so the role still binds — the renderer's
			// NineSlice helper degrades to a placeholder fill.
			insets = EditorThemeInsets{Left: 1, Top: 1, Right: 1, Bottom: 1}
		}
		w, h := decodeUIPackDims(asset.MetadataJSON)
		out = append(out, EditorThemeEntry{
			Role:         role,
			EntityTypeID: et.ID,
			AssetURL:     fmt.Sprintf("/design/assets/blob/%d", asset.ID),
			NineSlice:    insets,
			Width:        int32(w),
			Height:       int32(h),
		})
	}
	return out, nil
}

func loadNineSliceInsets(ctx context.Context, es *entities.Service, entityTypeID int64) (EditorThemeInsets, bool) {
	rows, err := es.Components(ctx, entityTypeID)
	if err != nil {
		slog.Warn("editor_theme: load components", "entity_type_id", entityTypeID, "err", err)
		return EditorThemeInsets{}, false
	}
	for _, r := range rows {
		if r.Kind != components.KindNineSlice {
			continue
		}
		var ns components.NineSlice
		if err := json.Unmarshal(r.ConfigJSON, &ns); err != nil {
			return EditorThemeInsets{}, false
		}
		return EditorThemeInsets{
			Left: ns.Left, Top: ns.Top, Right: ns.Right, Bottom: ns.Bottom,
		}, true
	}
	return EditorThemeInsets{}, false
}

func decodeUIPackDims(metadata json.RawMessage) (int, int) {
	if len(metadata) == 0 {
		return 0, 0
	}
	var m uiPackAssetMetadata
	if err := json.Unmarshal(metadata, &m); err != nil {
		return 0, 0
	}
	return m.Width, m.Height
}
