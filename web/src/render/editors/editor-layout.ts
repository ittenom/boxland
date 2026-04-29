// Boxland — editor scene layout.
//
// Builds a flexbox tree using @pixi/layout. The structure mirrors
// the muscle-memory layout designers know from the previous templ
// chrome:
//
//   ┌──────────────────────────────────────────────────────┐
//   │ Toolbar (height 32)                                   │
//   ├──────────┬───────────────────────────┬──────────────┤
//   │ Sidebar  │ Canvas viewport            │ Inspector    │
//   │ (240)    │ (flex 1)                   │ (320)        │
//   │          │                            │              │
//   ├──────────┴───────────────────────────┴──────────────┤
//   │ Statusbar (height 24)                                │
//   └──────────────────────────────────────────────────────┘
//   + Modal overlay (hidden by default)
//
// Per-surface entry scripts get back named slot containers and
// drop their widgets in. The layout module owns the geometry +
// resize behaviour; surfaces own their own contents.

import "./layout-init";
import { Container, Graphics } from "pixi.js";

import type { Theme } from "../ui";
import { NineSlice, Roles } from "../ui";

const SURFACE = 0x0f141f;
const PANEL = 0x121928;
const PANEL_ALT = 0x151d2c;
const STROKE = 0x263653;

/** Public slot container set. Surface entry scripts receive these
 *  and add their widgets directly. */
export interface EditorSlots {
	root: Container;
	toolbar: Container;
	sidebar: Container;
	canvasWrap: Container;
	inspector: Container;
	statusbar: Container;
	modalLayer: Container;
	/** Configured slot dims (px). Stable across resize() calls
	 *  for the fixed-size slots (toolbar / sidebar / inspector /
	 *  statusbar). Updated for the body slots on resize. */
	dims: SlotDims;
}

interface SlotDims {
	sidebarWidth: number;
	inspectorWidth: number;
	toolbarHeight: number;
	statusbarHeight: number;
}

export interface BuildLayoutOptions {
	theme: Theme;
	width: number;
	height: number;
	/** Sidebar width in px. Default 240. */
	sidebarWidth?: number;
	/** Inspector width in px. Default 320. */
	inspectorWidth?: number;
	/** Toolbar height in px. Default 32. */
	toolbarHeight?: number;
	/** Statusbar height in px. Default 24. */
	statusbarHeight?: number;
}

/** Build the editor's scene layout. Returns the root container +
 *  named slot references. Caller is responsible for adding the
 *  root to the BoxlandApp's stage and calling resize() on
 *  viewport changes. */
export function buildEditorLayout(opts: BuildLayoutOptions): EditorSlots {
	const sidebarW = opts.sidebarWidth ?? 240;
	const inspectorW = opts.inspectorWidth ?? 320;
	const toolbarH = opts.toolbarHeight ?? 40;
	const statusbarH = opts.statusbarHeight ?? 28;

	const root = new Container();
	root.layout = {
		width: opts.width,
		height: opts.height,
		flexDirection: "column",
	};

	// Toolbar — flex 0, fixed height.
	const toolbar = new Container();
	toolbar.layout = {
		width: "100%",
		height: toolbarH,
		flexShrink: 0,
		flexDirection: "row",
		alignItems: "center",
		paddingLeft: 10,
		paddingRight: 10,
		paddingTop: 5,
		paddingBottom: 5,
		gap: 8,
	};
	attachChromeBg(toolbar, opts.theme, Roles.FrameHorizontal, opts.width, toolbarH, PANEL_ALT, STROKE);
	root.addChild(toolbar);

	// Body — fills remaining vertical space between toolbar and
	// statusbar via flexGrow: 1. The `flex` shorthand is undocumented
	// in @pixi/layout v3 (only flexGrow/flexShrink/flexBasis are
	// listed in the styles guide); we use the explicit forms.
	const body = new Container();
	body.layout = {
		width: "100%",
		flexGrow: 1,
		flexShrink: 1,
		flexBasis: 0,
		flexDirection: "row",
		padding: 10,
		gap: 10,
		minHeight: 0, // critical: lets the body shrink to its share
	};
	attachSolidBg(body, opts.width, opts.height - toolbarH - statusbarH, SURFACE, SURFACE);

	const sidebar = new Container();
	sidebar.layout = {
		width: sidebarW,
		height: "100%",
		flexShrink: 0,
		flexDirection: "column",
		padding: 10,
		gap: 8,
	};
	attachChromeBg(sidebar, opts.theme, Roles.FrameVertical, sidebarW, opts.height, PANEL, STROKE);
	body.addChild(sidebar);

	const canvasWrap = new Container();
	canvasWrap.layout = {
		flexGrow: 1,
		flexShrink: 1,
		flexBasis: 0,
		height: "100%",
		minWidth: 0,
		minHeight: 0,
	};
	attachSolidBg(canvasWrap, 1, 1, SURFACE, SURFACE);
	body.addChild(canvasWrap);

	const inspector = new Container();
	inspector.layout = {
		width: inspectorW,
		height: "100%",
		flexShrink: 0,
		flexDirection: "column",
		padding: 10,
		gap: 8,
	};
	attachChromeBg(inspector, opts.theme, Roles.FrameVertical, inspectorW, opts.height, PANEL, STROKE);
	body.addChild(inspector);

	root.addChild(body);

	// Statusbar — flex 0.
	const statusbar = new Container();
	statusbar.layout = {
		width: "100%",
		height: statusbarH,
		flexShrink: 0,
		flexDirection: "row",
		alignItems: "center",
		paddingLeft: 10,
		paddingRight: 10,
		gap: 12,
	};
	attachChromeBg(statusbar, opts.theme, Roles.FrameHorizontal, opts.width, statusbarH, PANEL_ALT, STROKE);
	root.addChild(statusbar);

	// Modal overlay layer — sits on top of everything else,
	// hidden by default. The Modal helper handles its own
	// fullscreen scrim + child layout.
	const modalLayer = new Container();
	modalLayer.visible = false;
	modalLayer.layout = {
		// Absolute positioning of an overlay inside a flex
		// container needs `position: "absolute"`. The modal
		// layer is sized by the parent root.
		position: "absolute",
		top: 0,
		left: 0,
		width: "100%",
		height: "100%",
	};
	root.addChild(modalLayer);

	return {
		root, toolbar, sidebar, canvasWrap, inspector, statusbar, modalLayer,
		dims: {
			sidebarWidth: sidebarW,
			inspectorWidth: inspectorW,
			toolbarHeight: toolbarH,
			statusbarHeight: statusbarH,
		},
	};
}

/** Resize the layout root. Re-runs the flexbox pass via
 *  `@pixi/layout`'s reactive update path. The panel backgrounds
 *  (Toolbar, Sidebar, Inspector, Statusbar) are NineSlice
 *  containers that need their resize() method called explicitly;
 *  we walk the slot references and update each. */
export function resizeEditorLayout(slots: EditorSlots, width: number, height: number): void {
	// Re-style the root with new dims; @pixi/layout will recalc.
	slots.root.layout = {
		width, height, flexDirection: "column",
	};

	// Background panels sit at child index 0 of each slot. They
	// were sized at build time; resize them now using the
	// configured fixed dims + the new viewport size for variable
	// dims (sidebar/inspector height = viewport - chrome).
	const { sidebarWidth, inspectorWidth, toolbarHeight, statusbarHeight } = slots.dims;
	const bodyHeight = Math.max(0, height - toolbarHeight - statusbarHeight);
	resizeBg(slots.toolbar, width, toolbarHeight);
	resizeBg(slots.sidebar, sidebarWidth, bodyHeight);
	resizeBg(slots.inspector, inspectorWidth, bodyHeight);
	resizeBg(slots.statusbar, width, statusbarHeight);
}

// ---- helpers ----

/** attachPanelBg adds a NineSlice background as the parent's first
 *  child (so it draws behind every other child) and wires the
 *  parent's `'layout'` event so the bg auto-resizes whenever Yoga
 *  recomputes the parent's box. This is the documented pattern for
 *  intrinsic-sized art tracking a flex container's resolved box —
 *  see https://layout.pixijs.io/docs/guides/core/layout/. */
function attachPanelBg(parent: Container, theme: Theme, role: string, seedW: number, seedH: number): NineSlice {
	const bg = new NineSlice({ theme, role, width: seedW, height: seedH, fallbackColor: 0x1a2030 });
	parent.addChildAt(bg, 0);
	// `event.computedLayout` carries the resolved box. The bg sits
	// at the parent's content origin (the parent's own padding
	// already inset the content area for siblings), so we draw the
	// bg at (0,0) covering the full padded box.
	parent.on("layout", (event: { computedLayout: { width: number; height: number } }) => {
		const w = Math.max(1, Math.floor(event.computedLayout.width));
		const h = Math.max(1, Math.floor(event.computedLayout.height));
		bg.resize(w, h);
	});
	return bg;
}

function resizeBg(parent: Container, w: number, h: number): void {
	const bg = parent.children[0] as (Container & { __bxResizeBg?: (w: number, h: number) => void }) | Graphics | undefined;
	if (bg && "__bxResizeBg" in bg && typeof bg.__bxResizeBg === "function") {
		bg.__bxResizeBg(Math.max(1, Math.floor(w)), Math.max(1, Math.floor(h)));
		return;
	}
	if (bg && bg instanceof NineSlice) {
		bg.resize(Math.max(1, Math.floor(w)), Math.max(1, Math.floor(h)));
	} else if (bg && bg instanceof Graphics) {
		bg.clear().rect(0, 0, w, h).fill(0x1a2030);
	}
}

function attachChromeBg(
	parent: Container,
	theme: Theme,
	role: string,
	seedW: number,
	seedH: number,
	fill: number,
	stroke: number,
): Container {
	const bg = new Container() as Container & { __bxResizeBg?: (w: number, h: number) => void };
	const base = new Graphics();
	const frame = new NineSlice({ theme, role, width: seedW, height: seedH, fallbackColor: fill });
	frame.alpha = 0.28;
	bg.addChild(base);
	bg.addChild(frame);
	bg.__bxResizeBg = (w, h) => {
		drawSolidBg(base, w, h, fill, stroke);
		frame.resize(w, h);
	};
	bg.__bxResizeBg(seedW, seedH);
	parent.addChildAt(bg, 0);
	parent.on("layout", (event: { computedLayout: { width: number; height: number } }) => {
		bg.__bxResizeBg?.(
			Math.max(1, Math.floor(event.computedLayout.width)),
			Math.max(1, Math.floor(event.computedLayout.height)),
		);
	});
	return bg;
}

function attachSolidBg(parent: Container, seedW: number, seedH: number, fill: number, stroke: number): Graphics {
	const bg = new Graphics();
	drawSolidBg(bg, seedW, seedH, fill, stroke);
	parent.addChildAt(bg, 0);
	parent.on("layout", (event: { computedLayout: { width: number; height: number } }) => {
		drawSolidBg(
			bg,
			Math.max(1, Math.floor(event.computedLayout.width)),
			Math.max(1, Math.floor(event.computedLayout.height)),
			fill,
			stroke,
		);
	});
	return bg;
}

function drawSolidBg(g: Graphics, w: number, h: number, fill: number, stroke: number): void {
	g.clear().rect(0, 0, w, h).fill(fill);
	if (stroke !== fill) {
		g.rect(0, 0, w, h).stroke({ color: stroke, width: 1, alignment: 1 });
	}
}
