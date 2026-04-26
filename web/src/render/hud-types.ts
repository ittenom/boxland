// Boxland — render/hud-types.ts
//
// Pure types for the in-world HUD. Mirrors the Go server's
// internal/hud package (layout.go + widgets.go + binding.go). Kept
// dependency-free so the layout walker + binding parser can be unit
// tested without standing up Pixi.
//
// Wire path: the play-game page fetches GET /play/maps/{id}/hud as JSON
// and feeds it to the Hud renderer (see hud.ts). Future: the layout +
// initial binding values will arrive in a HudLayoutFrame on the player
// WS instead of HTTP, but the JSON shape is identical so the renderer
// won't change.

/** The nine fixed positions a widget stack may pin to. */
export type Anchor =
	| "top-left" | "top-center" | "top-right"
	| "mid-left" | "mid-center" | "mid-right"
	| "bottom-left" | "bottom-center" | "bottom-right";

export const ALL_ANCHORS: readonly Anchor[] = [
	"top-left", "top-center", "top-right",
	"mid-left", "mid-center", "mid-right",
	"bottom-left", "bottom-center", "bottom-right",
];

/** Stack direction. */
export type StackDir = "vertical" | "horizontal";

/** Widget envelope shared by every widget kind. */
export interface Widget {
	type: WidgetKind;
	order: number;
	visible_when?: ConditionNode;
	skin?: number;            // ui_panel asset id; 0/undefined = no frame
	tint?: number;            // 0xRRGGBBAA
	size?: WidgetSize;
	config: unknown;          // typed per widget kind below
}

export type WidgetSize = "1x" | "2x" | "3x" | "";

/** Catalog of widget kinds. Mirrored in server/internal/hud/widgets.go. */
export type WidgetKind =
	| "resource_bar"
	| "text_label"
	| "mini_clock"
	| "icon_counter"
	| "portrait"
	| "button"
	| "dialog_frame";

/** Anchor stack contents. */
export interface Stack {
	dir: StackDir;
	gap: number;       // px between widgets
	offsetX: number;   // px from anchor edge
	offsetY: number;
	widgets: Widget[];
}

/** Full per-realm layout. */
export interface Layout {
	v: number;
	anchors: Partial<Record<Anchor, Stack>>;
}

/** Empty layout matching the migration's DEFAULT. */
export function emptyLayout(): Layout {
	return { v: 1, anchors: {} };
}

// ---- Per-widget config types ----

export interface ResourceBarConfig {
	binding: string;       // e.g. "entity:host:hp_pct" or "flag:gold"
	max?: number;
	fill_color?: number;
	bg_color?: number;
	label?: string;
	show_value?: boolean;
	segmented?: boolean;
	width_px?: number;
	height_px?: number;
}

export interface TextLabelConfig {
	template: string;      // "Gold: {flag:gold}"
	color?: number;
	font_size?: 12 | 16 | 24 | 0;
	align?: "left" | "center" | "right" | "";
}

export interface MiniClockConfig {
	channel: "realm_clock" | "wall";
	format?: "HH:MM" | "Day N" | "tick" | "";
	color?: number;
}

export interface IconCounterConfig {
	icon: number;          // asset id
	binding: string;
	color?: number;
	prefix?: string;
	suffix?: string;
	pad_digits?: number;
}

export interface PortraitConfig {
	asset: number;
	frame: number;
	binding?: string;
}

export interface ButtonConfig {
	label: string;
	hotkey?: string;
	action_group: string;
	hit_padding_px?: number;
}

export interface DialogFrameConfig {
	width_px: number;
	height_px: number;
}

// ---- Bindings ----

export type BindingKind = "entity" | "flag" | "time";

export interface BindingRef {
	kind: BindingKind;
	key: string;       // entity:<who> or flag:<key> or time:<channel>
	sub?: string;      // entity-only: resource name
}

/** Canonical "kind:key[:sub]" string, stable across client + server.
 *  Used as a Map key and as the on-the-wire binding name. */
export function bindingString(b: BindingRef): string {
	return b.sub ? `${b.kind}:${b.key}:${b.sub}` : `${b.kind}:${b.key}`;
}

const VALID_ENTITY_WHO = new Set(["host", "self"]);
const VALID_ENTITY_RESOURCE = new Set([
	"hp_pct", "nameplate", "variant_id", "facing",
	"anim_id", "anim_frame", "tint", "x", "y",
]);
const VALID_TIME = new Set(["realm_clock", "wall"]);

export class BindingParseError extends Error {
	constructor(msg: string) { super(`binding: ${msg}`); }
}

/** Parse "kind:key[:sub]" into a BindingRef. Throws BindingParseError
 *  on any malformed input — callers that expect possibly-empty bindings
 *  should check the string first. */
export function parseBinding(s: string): BindingRef {
	if (!s) throw new BindingParseError("empty");
	const parts = s.split(":");
	if (parts.length < 2 || parts.length > 3) {
		throw new BindingParseError(`${s} wrong shape (want kind:key[:sub])`);
	}
	const kind = parts[0] as BindingKind;
	const a = parts[1] ?? "";
	const b = parts[2] ?? "";
	if (kind === "entity") {
		if (parts.length !== 3) throw new BindingParseError(`${s} entity needs sub`);
		if (!VALID_ENTITY_WHO.has(a)) {
			throw new BindingParseError(`${s} entity who ${a} (want host|self)`);
		}
		if (!VALID_ENTITY_RESOURCE.has(b) && !b.startsWith("resource:")) {
			throw new BindingParseError(`${s} entity resource ${b} not recognized`);
		}
		return { kind, key: a, sub: b };
	}
	if (kind === "flag") {
		if (parts.length !== 2) throw new BindingParseError(`${s} flag uses kind:key`);
		validateBindingKey(a);
		return { kind, key: a };
	}
	if (kind === "time") {
		if (parts.length !== 2) throw new BindingParseError(`${s} time uses kind:channel`);
		if (!VALID_TIME.has(a)) {
			throw new BindingParseError(`${s} time channel ${a}`);
		}
		return { kind, key: a };
	}
	throw new BindingParseError(`${s} unknown kind ${kind}`);
}

function validateBindingKey(k: string): void {
	if (!k) throw new BindingParseError("empty key");
	if (k.length > 64) throw new BindingParseError(`key ${k} > 64 chars`);
	for (let i = 0; i < k.length; i++) {
		const c = k.charCodeAt(i);
		const ok = (c >= 97 && c <= 122) || (c >= 48 && c <= 57) || c === 95; // a-z, 0-9, _
		if (!ok) throw new BindingParseError(`key ${k} invalid char at ${i}`);
	}
}

/** Walk a "{kind:key[:sub]}" template and extract every binding ref.
 *  Throws on unterminated braces. */
export function parseTemplateBindings(tmpl: string): BindingRef[] {
	const out: BindingRef[] = [];
	let i = 0;
	while (i < tmpl.length) {
		const open = tmpl.indexOf("{", i);
		if (open < 0) break;
		const close = tmpl.indexOf("}", open);
		if (close < 0) throw new BindingParseError(`template: unterminated '{' at ${open}`);
		out.push(parseBinding(tmpl.substring(open + 1, close)));
		i = close + 1;
	}
	return out;
}

/** Render a "{kind:key[:sub]}" template against a value bag. Missing
 *  values render as the empty string; the renderer treats undefined
 *  bindings as "not yet known" and shows the surrounding chrome
 *  unchanged. */
export function renderTemplate(tmpl: string, lookup: (b: BindingRef) => string): string {
	let out = "";
	let i = 0;
	while (i < tmpl.length) {
		const open = tmpl.indexOf("{", i);
		if (open < 0) { out += tmpl.substring(i); break; }
		out += tmpl.substring(i, open);
		const close = tmpl.indexOf("}", open);
		if (close < 0) { out += tmpl.substring(open); break; }
		try {
			const ref = parseBinding(tmpl.substring(open + 1, close));
			out += lookup(ref);
		} catch {
			// Round-trip the literal so designers see what they typed.
			out += tmpl.substring(open, close + 1);
		}
		i = close + 1;
	}
	return out;
}

// ---- Conditions (reuse the automations Condition AST shape) ----

export type ConditionOp =
	| "and" | "or" | "not"
	| "count_gt" | "count_lt" | "range_within";

export interface ConditionNode {
	op: ConditionOp;
	children?: ConditionNode[];
	subject?: string;
	value?: number;
	min?: number;
	max?: number;
}

// ---- HUD value cache ----

/** Latest known value for a binding. Mirrors HudValueKind on the wire. */
export type HudValue =
	| { kind: "int"; value: number }
	| { kind: "string"; value: string };

/** Lookup function the template renderer uses. Returns "" for unknown. */
export type HudLookup = (ref: BindingRef) => string;

/** Build a HudLookup over a Map<bindingString, HudValue>. */
export function makeLookup(values: Map<string, HudValue>): HudLookup {
	return (ref) => {
		const v = values.get(bindingString(ref));
		if (!v) return "";
		return v.kind === "int" ? String(v.value) : v.value;
	};
}

// ---- Anchor math ----

export interface AnchorOrigin {
	/** Pivot x in viewport pixels. */
	x: number;
	/** Pivot y in viewport pixels. */
	y: number;
	/** Sign in x: +1 = grow right from pivot, -1 = grow left. */
	signX: 1 | -1;
	/** Sign in y. */
	signY: 1 | -1;
}

/** Compute the pivot point + grow-direction for an anchor at the given
 *  viewport size. Output is in *world pixels* (logical resolution before
 *  integer scale); the Hud's container scale handles the rest. */
export function anchorOrigin(anchor: Anchor, viewportW: number, viewportH: number): AnchorOrigin {
	let x = 0, y = 0;
	let signX: 1 | -1 = 1, signY: 1 | -1 = 1;
	if (anchor.includes("center")) {
		x = Math.floor(viewportW / 2);
	} else if (anchor.includes("right")) {
		x = viewportW;
		signX = -1;
	}
	if (anchor.startsWith("mid")) {
		y = Math.floor(viewportH / 2);
	} else if (anchor.startsWith("bottom")) {
		y = viewportH;
		signY = -1;
	}
	return { x, y, signX, signY };
}
