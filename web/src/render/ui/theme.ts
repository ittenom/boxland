// Boxland — editor UI theme.
//
// A `Theme` is a registry mapping a *role* (semantic name, e.g.
// "button_md_release") to a Pixi `Texture` and 9-slice insets for
// that texture. Editors use roles, not raw `entity_type_id`s, so a
// designer can swap the underlying art (replace "button_md_release"
// with a custom sprite) and every button on every editor surface
// updates uniformly.
//
// The role catalog flows from the server in the editor snapshot
// (Phase 4): one ThemeEntry per ClassUI entity_type the operator
// has imported. The server picks the role names from a fixed table
// keyed off entity_type names — see server/internal/ws/editor_theme.go.
//
// Theme is stateless toward Pixi: it holds Promise<Texture> handles
// from a shared TextureCache, so multiple widgets requesting the
// same role share one GPU texture. The cache lifetime is tied to
// the BoxlandApp; tearing the editor down releases everything.

import { Assets, Texture } from "pixi.js";

import type { TextureCache } from "../textures";

/** One role → texture mapping. Roles are stable strings the editor
 *  references by name; entries are populated from the server snapshot. */
export interface ThemeEntry {
	/** Role id, e.g. "button_md_release", "frame_standard". */
	role: string;
	/** Entity type id this role binds to (audit / debug). */
	entityTypeId: number;
	/** Same-origin URL the renderer fetches the source PNG from. */
	assetUrl: string;
	/** 9-slice insets in source pixels. */
	nineSlice: NineSliceInsets;
	/** Source PNG dimensions in pixels. Lets the renderer skip
	 *  9-slice stretching when the requested size matches the
	 *  source (no need to stretch a button at its native dims). */
	width: number;
	height: number;
}

export interface NineSliceInsets {
	left: number;
	top: number;
	right: number;
	bottom: number;
}

/** Theme — one shared registry per editor app. */
export class Theme {
	private readonly byRole = new Map<string, ThemeEntry>();
	private readonly textures = new Map<string, Promise<Texture>>();

	/** Build from a snapshot's ui_theme list. The same list shape
	 *  works for both server-pushed snapshots and test fixtures. */
	static fromEntries(entries: readonly ThemeEntry[]): Theme {
		const t = new Theme();
		for (const e of entries) t.byRole.set(e.role, e);
		return t;
	}

	/** Lookup a role; returns null when the role isn't bound yet
	 *  (the editor degrades gracefully — a missing role renders a
	 *  visible-but-untextured panel that the designer can spot
	 *  and fix). */
	get(role: string): ThemeEntry | null {
		return this.byRole.get(role) ?? null;
	}

	/** All known roles. Useful for theme browsers / picker UIs. */
	roles(): readonly string[] {
		return [...this.byRole.keys()];
	}

	/** Lazy-load and cache the texture for a role. Subsequent calls
	 *  collapse to the same Promise, so concurrent widgets
	 *  requesting the same role only fetch the bytes once. */
	textureFor(role: string): Promise<Texture> | null {
		const entry = this.get(role);
		if (!entry || !entry.assetUrl) return null;
		const cached = this.textures.get(entry.assetUrl);
		if (cached) return cached;
		const p = Assets.load<Texture>(entry.assetUrl).then((tex) => {
			// Pixel-art convention: nearest-neighbor + roundPixels.
			// `tex` can be null in jsdom (no PNG decoder); guard so
			// the unhandled-rejection doesn't leak into tests that
			// don't await the load.
			if (tex && tex.source) {
				tex.source.scaleMode = "nearest";
			}
			return tex;
		});
		this.textures.set(entry.assetUrl, p);
		return p;
	}

	/** Pre-warm every texture in the theme. Editors call this on
	 *  boot so the first frame paints with art rather than empty
	 *  panels. Returns a Promise that resolves once every load
	 *  has settled (success or fail; we don't reject the whole
	 *  prefetch on one missing texture). */
	prefetchAll(): Promise<void> {
		const ps: Array<Promise<unknown>> = [];
		for (const role of this.byRole.keys()) {
			const t = this.textureFor(role);
			if (t) ps.push(t.catch(() => undefined));
		}
		return Promise.allSettled(ps).then(() => undefined);
	}

	/** Number of bound roles. Useful for tests + status indicators. */
	size(): number { return this.byRole.size; }
}

/** Roles the editor surfaces reference. Stable identifiers; the
 *  server's role-mapping table (see server/internal/ws/editor_theme.go)
 *  is authoritative for which entity_type_name binds to which role.
 *  Listed here so TS callers get autocomplete + typo-checking. */
export const Roles = {
	// Frames (panel backgrounds).
	FrameStandard:    "frame_standard",
	FrameLite:        "frame_lite",
	FrameInward:      "frame_inward",
	FrameOutward:     "frame_outward",
	FrameHorizontal:  "frame_horizontal",
	FrameVertical:    "frame_vertical",

	// Buttons. Three sizes × three states (release / press / lock).
	// We use frame "01a1" (the first animation frame of each sprite)
	// for the static states and reserve the other frames for press
	// animations the renderer plays through.
	ButtonSmReleaseA:  "button_sm_release_a",
	ButtonSmPressA:    "button_sm_press_a",
	ButtonSmLockA:     "button_sm_lock_a",
	ButtonMdReleaseA:  "button_md_release_a",
	ButtonMdPressA:    "button_md_press_a",
	ButtonMdLockA:     "button_md_lock_a",
	ButtonLgReleaseA:  "button_lg_release_a",
	ButtonLgPressA:    "button_lg_press_a",
	ButtonLgLockA:     "button_lg_lock_a",

	// Form inputs.
	Textfield:         "textfield",
	DropdownBar:       "dropdown_bar",
	DropdownHandle:    "dropdown_handle",
	SliderBar:         "slider_bar",
	SliderFiller:      "slider_filler",
	SliderHandle:      "slider_handle",
	ScrollBar:         "scroll_bar",
	ScrollHandle:      "scroll_handle",
	FillBar:           "fill_bar",
	FillFiller:        "fill_filler",

	// Inventory + selection slots.
	SlotAvailable:     "slot_available",
	SlotSelected:      "slot_selected",
	SlotUnavailable:   "slot_unavailable",

	// Decorative + indicators.
	Banner:            "banner",
	ArrowSm:           "arrow_sm",
	ArrowMd:           "arrow_md",
	ArrowLg:           "arrow_lg",
	CheckmarkSm:       "checkmark_sm",
	CheckmarkMd:       "checkmark_md",
	CheckmarkLg:       "checkmark_lg",
	CrossSm:           "cross_sm",
	CrossMd:           "cross_md",
	CrossLg:           "cross_lg",
} as const;

/** All role values as a union type. Use this for typed role params. */
export type Role = (typeof Roles)[keyof typeof Roles];

/** Bridge for code that wants to load textures via a TextureCache
 *  instance instead of through Assets.load directly. Useful when an
 *  editor wants to participate in the cache's lifecycle (textures
 *  auto-disposed when the cache resets). Pass the editor's existing
 *  TextureCache; we delegate to its base() method using a synthetic
 *  asset id (the role's entityTypeId). */
export function bindThemeToTextureCache(theme: Theme, cache: TextureCache): void {
	// `Theme.textureFor` already does Promise-coalescing on URL.
	// This bridge is reserved for future cache integration; for
	// v1 the Theme owns its own cache. Kept as a no-op so callers
	// can wire it now and have it gain behaviour later without an
	// API change.
	void theme;
	void cache;
}
