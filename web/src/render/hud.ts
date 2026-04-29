// Boxland — render/hud.ts
//
// In-world player-facing HUD. One Container mounted above the entity /
// lighting / nameplate / debug stack so it never gets occluded. Renders
// at *viewport* coordinates, NOT world coordinates — the container is
// kept un-scaled by the camera transform but tracks the viewport's
// integer scale so widgets stay pixel-perfect at 1x / 2x / 3x zoom.
//
// Data flow:
//
//   1. Page boot fetches GET /play/maps/{id}/hud → Layout JSON.
//   2. Hud.mount(layout) builds widget handles in their anchor stacks.
//   3. Per-tick, the host pumps Mailbox HUD listeners → Hud.update(id, value).
//   4. Each widget keyed to that binding-id re-renders only its bound bits.
//
// The widget catalog lives in widgets/ (one file per kind) so adding a
// kind is a one-file change. Anchor + stack math is dependency-free
// (hud-types.ts) so it's testable headless without standing up Pixi.

import { Container, Graphics, NineSliceSprite, Sprite, Text, Texture } from "pixi.js";

import {
	ALL_ANCHORS,
	type Anchor,
	type Layout,
	type Stack,
	type Widget,
	type ResourceBarConfig,
	type TextLabelConfig,
	type MiniClockConfig,
	type IconCounterConfig,
	type PortraitConfig,
	type ButtonConfig,
	type DialogFrameConfig,
	type BindingRef,
	type HudValue,
	type ConditionNode,
	anchorOrigin,
	bindingString,
	makeLookup,
	parseBinding,
	parseTemplateBindings,
	renderTemplate,
} from "./hud-types";
import type { TextureCache } from "./textures";

/** One mounted widget — opaque to callers, owned by the Hud. */
interface WidgetHandle {
	root: Container;
	bindings: BindingRef[];
	visibleWhen: ConditionNode | undefined;
	/** Intrinsic size in world px; the stack layout uses this. */
	width: number;
	height: number;
	/** Refresh against the current value bag. */
	render(): void;
	/** Tear down — destroy children + remove listeners. */
	destroy(): void;
}

/** Click handler invoked when a button widget fires. */
export type HudButtonHandler = (actionGroup: string) => void;

export interface HudOptions {
	/** Viewport size in world pixels (logical, before integer scale). */
	worldViewW: number;
	worldViewH: number;
	textures: TextureCache;
	/** Asset URL resolver for ui_panel skins (Todo 1's KindUIPanel) and
	 *  flat sprite icons (icon_counter, portrait). */
	urlFor: (assetId: number) => string;
	/** Called when a button widget is clicked or its hotkey fires. */
	onButton?: HudButtonHandler;
	/** Optional: override the default font family ("C64esque"). */
	font?: string;
	/**
	 * Optional resolver: returns the 9-slice insets for an
	 * entity_type id (the `skin` field on a widget). When omitted
	 * or returning null, the renderer falls back to symmetric 8-px
	 * insets (the historical default that worked for the previous
	 * generation of skin frames).
	 *
	 * Production wiring: the page boot reads the `nine_slice`
	 * component from the entity_type's components row and passes
	 * a resolver here so designer-uploaded UI panels can declare
	 * arbitrary insets and the HUD honors them. The same resolver
	 * is used in the editor harness's NineSlice helper, closing
	 * the dogfood loop: the same entity_type renders identically
	 * in editor chrome and in-game HUDs.
	 */
	sliceInsetsFor?: (entityTypeId: number) => { left: number; top: number; right: number; bottom: number } | null;
}

/**
 * Hud is a self-contained Pixi layer. The Scene mounts it above the
 * debug overlay; the host calls update(bindingId, value) whenever the
 * Mailbox emits a HUD delta.
 *
 * Construction is two-step: `new Hud()` then `mount(layout, bindings)`.
 * That lets the renderer attach the root before the layout has loaded
 * (the page boot does HTTP fetch in parallel with Pixi init).
 */
export class Hud {
	readonly root = new Container();

	private layout: Layout | null = null;
	/** binding-id → BindingRef. Index in the array IS the wire id. */
	private bindings: BindingRef[] = [];
	/** "kind:key[:sub]" → cached value. */
	private readonly values = new Map<string, HudValue>();
	/** Widget handles in mount order. */
	private widgets: WidgetHandle[] = [];
	/** binding-string → widget handles bound to it. */
	private readonly subs = new Map<string, Set<WidgetHandle>>();

	private viewportW: number;
	private viewportH: number;
	private scale = 1;

	/** Mutable button handler; the Scene exposes it via setOnButton so
	 *  the host can wire the command bus AFTER both Scene and bus exist. */
	private onButton: HudButtonHandler | undefined;

	constructor(private readonly opts: HudOptions) {
		this.root.label = "hud";
		this.viewportW = opts.worldViewW;
		this.viewportH = opts.worldViewH;
		this.onButton = opts.onButton;
		// HUD is rendered above the camera-scaled scene; its root is NOT
		// inside Scene.root (which is camera-scaled). Caller adds it as
		// a sibling in app.stage. roundPixels keeps everything crisp.
		(this.root as Container & { roundPixels?: boolean }).roundPixels = true;
	}

	/** Attach a layout. Replaces any previously-mounted widgets.
	 *  `bindings` is the binding-id table (index = wire id); for HTTP
	 *  bootstrapping we synthesize it from layout walking. */
	mount(layout: Layout, bindings?: BindingRef[]): void {
		this.unmountAll();
		this.layout = layout;
		this.bindings = bindings ?? walkLayoutBindings(layout);

		for (const anchor of ALL_ANCHORS) {
			const stack = layout.anchors[anchor];
			if (!stack) continue;
			this.mountStack(anchor, stack);
		}
		this.relayout();
	}

	/** Apply a single binding update. Cheap: O(widgets bound to this id). */
	update(bindingId: number, value: HudValue): void {
		const ref = this.bindings[bindingId];
		if (!ref) return;
		const key = bindingString(ref);
		this.values.set(key, value);
		const subs = this.subs.get(key);
		if (!subs) return;
		for (const w of subs) {
			if (this.shouldRender(w)) {
				if (!w.root.visible) w.root.visible = true;
				w.render();
			} else if (w.root.visible) {
				w.root.visible = false;
			}
		}
	}

	/** Apply a full bag of values (used after the page-boot HTTP load
	 *  when initial values exist, e.g. flag values fetched alongside
	 *  the layout). */
	updateAll(values: Iterable<[string, HudValue]>): void {
		for (const [key, v] of values) this.values.set(key, v);
		for (const w of this.widgets) {
			w.root.visible = this.shouldRender(w);
			if (w.root.visible) w.render();
		}
	}

	/** Inform the HUD that the viewport changed size or scale. */
	resize(viewportW: number, viewportH: number, scale: number): void {
		this.viewportW = viewportW;
		this.viewportH = viewportH;
		this.scale = Math.max(1, Math.floor(scale));
		this.root.scale.set(this.scale);
		this.relayout();
	}

	/** Wire the button click handler (typically GameLoop.bus.dispatch).
	 *  Safe to call before or after mount(); existing button widgets
	 *  pick up the new handler on next click. */
	setOnButton(handler: HudButtonHandler | undefined): void {
		this.onButton = handler;
	}

	/** Tear down everything. Safe to call multiple times. */
	destroy(): void {
		this.unmountAll();
		this.root.destroy({ children: true });
	}

	// ---- Test-only inspection ----

	/** Test hook: number of mounted widgets. */
	widgetCount(): number { return this.widgets.length; }

	/** Test hook: ordered binding-ref strings. */
	bindingKeys(): string[] { return this.bindings.map(bindingString); }

	/** Test hook: visible widget count (after visible_when). */
	visibleWidgetCount(): number {
		return this.widgets.reduce((n, w) => n + (w.root.visible ? 1 : 0), 0);
	}

	// ---- Internals ----

	private unmountAll(): void {
		for (const w of this.widgets) w.destroy();
		this.widgets = [];
		this.subs.clear();
		this.root.removeChildren();
	}

	private mountStack(anchor: Anchor, stack: Stack): void {
		// Sort widgets by `order` so server-side reorder ops are honored
		// without depending on JS Array.sort being stable (Node 22 is,
		// but explicit is friendlier to test).
		const ordered = [...stack.widgets].sort((a, b) => a.order - b.order);
		const stackContainer = new Container();
		stackContainer.label = `hud-anchor-${anchor}`;
		this.root.addChild(stackContainer);
		// We tag the container with anchor + stack so relayout() can
		// replay it without touching the layout JSON.
		(stackContainer as Container & { __bxAnchor?: Anchor; __bxStack?: Stack }).__bxAnchor = anchor;
		(stackContainer as Container & { __bxAnchor?: Anchor; __bxStack?: Stack }).__bxStack = stack;
		for (const widgetDef of ordered) {
			const handle = this.buildWidget(widgetDef);
			if (!handle) continue;
			handle.root.visible = this.shouldRender(handle);
			if (handle.root.visible) handle.render();
			stackContainer.addChild(handle.root);
			this.widgets.push(handle);
			// Subscribe to every binding the widget depends on for VALUE,
			// AND every binding its visible_when condition depends on
			// (so a flag flip shows/hides the widget without waiting for
			// its value bindings to also change).
			const visBindings = widgetDef.visible_when ? collectConditionBindings(widgetDef.visible_when) : [];
			const all = [...handle.bindings, ...visBindings];
			for (const ref of all) {
				const key = bindingString(ref);
				let set = this.subs.get(key);
				if (!set) { set = new Set(); this.subs.set(key, set); }
				set.add(handle);
			}
		}
	}

	private buildWidget(w: Widget): WidgetHandle | null {
		switch (w.type) {
			case "resource_bar": return buildResourceBar(w, this.opts, () => makeLookup(this.values));
			case "text_label":   return buildTextLabel(w, this.opts, () => makeLookup(this.values));
			case "mini_clock":   return buildMiniClock(w, this.opts, () => makeLookup(this.values));
			case "icon_counter": return buildIconCounter(w, this.opts, () => makeLookup(this.values));
			case "portrait":     return buildPortrait(w, this.opts, () => makeLookup(this.values));
			case "button":       return buildButton(w, this.opts, () => this.onButton);
			case "dialog_frame": return buildDialogFrame(w, this.opts);
			default:             return null;
		}
	}

	/** Walk every stack and re-position its container at the current
	 *  viewport size + scale. Idempotent; safe to call from resize() or
	 *  after a mount(). */
	private relayout(): void {
		// Anchor pivots are computed in pre-scale (world) px; the
		// container's scale (set in resize) does the integer-scale step.
		// We position each stack's container at its anchor pivot and lay
		// out children inside it.
		const w = this.viewportW / this.scale;
		const h = this.viewportH / this.scale;
		for (const child of this.root.children) {
			const tagged = child as Container & { __bxAnchor?: Anchor; __bxStack?: Stack };
			if (!tagged.__bxAnchor || !tagged.__bxStack) continue;
			layoutStackInto(tagged, tagged.__bxAnchor, tagged.__bxStack, w, h);
		}
	}

	private shouldRender(w: WidgetHandle): boolean {
		if (!w.visibleWhen) return true;
		return evalCondition(w.visibleWhen, makeLookup(this.values));
	}
}

/** Walk a layout collecting unique bindings in stable sorted order.
 *  Includes both value bindings and visible_when subjects so the wire-side
 *  binding-id table covers everything the renderer subscribes to. */
export function walkLayoutBindings(layout: Layout): BindingRef[] {
	const seen = new Map<string, BindingRef>();
	const add = (ref: BindingRef): void => {
		const key = bindingString(ref);
		if (!seen.has(key)) seen.set(key, ref);
	};
	for (const anchor of ALL_ANCHORS) {
		const stack = layout.anchors[anchor];
		if (!stack) continue;
		for (const w of stack.widgets) {
			for (const ref of bindingsOf(w)) add(ref);
			if (w.visible_when) {
				for (const ref of collectConditionBindings(w.visible_when)) add(ref);
			}
		}
	}
	const out = [...seen.values()];
	out.sort((a, b) => bindingString(a).localeCompare(bindingString(b)));
	return out;
}

/** Walk a Condition AST collecting every subject parsed as a binding ref.
 *  Malformed subjects are silently dropped — they'll surface as a publish-time
 *  validate error elsewhere. */
function collectConditionBindings(node: ConditionNode): BindingRef[] {
	const out: BindingRef[] = [];
	const visit = (n: ConditionNode): void => {
		if (n.subject) {
			try { out.push(parseBinding(n.subject)); } catch { /* skip malformed */ }
		}
		for (const c of n.children ?? []) visit(c);
	};
	visit(node);
	return out;
}

function bindingsOf(w: Widget): BindingRef[] {
	switch (w.type) {
		case "resource_bar": {
			const c = w.config as ResourceBarConfig;
			try { return c.binding ? [parseBinding(c.binding)] : []; } catch { return []; }
		}
		case "text_label": {
			const c = w.config as TextLabelConfig;
			try { return parseTemplateBindings(c.template); } catch { return []; }
		}
		case "mini_clock": {
			const c = w.config as MiniClockConfig;
			return [{ kind: "time", key: c.channel }];
		}
		case "icon_counter": {
			const c = w.config as IconCounterConfig;
			try { return [parseBinding(c.binding)]; } catch { return []; }
		}
		case "portrait": {
			const c = w.config as PortraitConfig;
			try { return c.binding ? [parseBinding(c.binding)] : []; } catch { return []; }
		}
		case "button":       return [];
		case "dialog_frame": return [];
	}
}

// ---- Stack layout ----

/** Position a mounted stack container at its anchor + lay out children. */
export function layoutStackInto(
	root: Container,
	anchor: Anchor,
	stack: Stack,
	viewportW: number,
	viewportH: number,
): void {
	const origin = anchorOrigin(anchor, viewportW, viewportH);
	// Origin is the anchor edge; offset moves the stack inward.
	root.position.set(origin.x + origin.signX * stack.offsetX, origin.y + origin.signY * stack.offsetY);

	const dir = stack.dir;
	let cursor = 0;
	for (const child of root.children) {
		const c = child as Container;
		if (dir === "vertical") {
			// Vertical stack: y advances by child.height + gap.
			// signY-aware placement for bottom anchors.
			const h = (c as Container & { __bxH?: number }).__bxH ?? c.height;
			c.position.set(origin.signX === -1 ? -c.width : 0, origin.signY === -1 ? -(cursor + h) : cursor);
			cursor += h + stack.gap;
		} else {
			const w = (c as Container & { __bxW?: number }).__bxW ?? c.width;
			c.position.set(origin.signX === -1 ? -(cursor + w) : cursor, origin.signY === -1 ? -c.height : 0);
			cursor += w + stack.gap;
		}
	}
}

// ---- Widget builders ----

const DEFAULT_BG_COLOR = 0x141028;

function colorRGB(c: number | undefined, fallback: number): number {
	if (c === undefined || c === 0) return fallback;
	// 0xRRGGBBAA wire encoding → drop alpha (Pixi tint is RGB).
	return (c >>> 8) & 0xffffff;
}

function widgetSizePx(size: string | undefined): number {
	switch (size) {
		case "2x": return 2;
		case "3x": return 3;
		default:   return 1;
	}
}

function buildResourceBar(w: Widget, opts: HudOptions, lookup: () => (b: BindingRef) => string): WidgetHandle {
	const cfg = w.config as ResourceBarConfig;
	const ref = parseBinding(cfg.binding);
	const root = new Container();
	const widthPx = (cfg.width_px || 64) * widgetSizePx(w.size);
	const heightPx = (cfg.height_px || 8) * widgetSizePx(w.size);
	const bg = new Graphics();
	const fill = new Graphics();
	root.addChild(bg);
	root.addChild(fill);
	let valueText: Text | null = null;
	let labelText: Text | null = null;
	if (cfg.label) {
		labelText = new Text({
			text: cfg.label,
			style: { fontFamily: opts.font ?? "C64esque", fontSize: 12, fill: 0xffffff },
		});
		labelText.position.set(0, -14);
		root.addChild(labelText);
	}
	if (cfg.show_value) {
		valueText = new Text({
			text: "",
			style: { fontFamily: opts.font ?? "C64esque", fontSize: 12, fill: 0xffffff },
		});
		valueText.position.set(0, heightPx + 2);
		root.addChild(valueText);
	}
	const fg = colorRGB(cfg.fill_color, 0x4caf50);
	const bgc = colorRGB(cfg.bg_color, DEFAULT_BG_COLOR);
	const tagged = root as Container & { __bxW?: number; __bxH?: number };
	tagged.__bxW = widthPx;
	tagged.__bxH = heightPx + (labelText ? 14 : 0) + (valueText ? 14 : 0);
	return {
		root,
		bindings: [ref],
		visibleWhen: w.visible_when,
		width: widthPx,
		height: tagged.__bxH,
		render: () => {
			bg.clear();
			bg.rect(0, 0, widthPx, heightPx).fill(bgc);
			const v = lookup()(ref);
			let pct = 0;
			if (v) {
				const num = Number(v);
				if (!Number.isNaN(num)) {
					const max = ref.kind === "entity" && ref.sub === "hp_pct" ? 255 : (cfg.max ?? 1);
					pct = Math.max(0, Math.min(1, max > 0 ? num / max : 0));
				}
			}
			fill.clear();
			if (cfg.segmented) {
				const segs = 10;
				const filled = Math.round(pct * segs);
				const segW = Math.max(1, Math.floor((widthPx - (segs - 1)) / segs));
				for (let i = 0; i < filled; i++) {
					fill.rect(i * (segW + 1), 0, segW, heightPx).fill(fg);
				}
			} else {
				fill.rect(0, 0, Math.round(widthPx * pct), heightPx).fill(fg);
			}
			if (valueText) valueText.text = v;
		},
		destroy: () => root.destroy({ children: true }),
	};
}

function buildTextLabel(w: Widget, opts: HudOptions, lookup: () => (b: BindingRef) => string): WidgetHandle {
	const cfg = w.config as TextLabelConfig;
	const fontSize = (cfg.font_size && cfg.font_size > 0 ? cfg.font_size : 16) * widgetSizePx(w.size);
	const align: "left" | "center" | "right" = cfg.align && cfg.align.length > 0 ? cfg.align : "left";
	const text = new Text({
		text: cfg.template,
		style: { fontFamily: opts.font ?? "C64esque", fontSize, fill: colorRGB(cfg.color, 0xffffff), align },
	});
	const root = new Container();
	root.addChild(text);
	const refs: BindingRef[] = (() => {
		try { return parseTemplateBindings(cfg.template); } catch { return []; }
	})();
	return {
		root,
		bindings: refs,
		visibleWhen: w.visible_when,
		width: 0, height: 0,
		render: () => {
			text.text = renderTemplate(cfg.template, lookup());
		},
		destroy: () => root.destroy({ children: true }),
	};
}

function buildMiniClock(w: Widget, opts: HudOptions, lookup: () => (b: BindingRef) => string): WidgetHandle {
	const cfg = w.config as MiniClockConfig;
	const ref: BindingRef = { kind: "time", key: cfg.channel };
	const text = new Text({
		text: "",
		style: { fontFamily: opts.font ?? "C64esque", fontSize: 14 * widgetSizePx(w.size), fill: colorRGB(cfg.color, 0xffffff) },
	});
	const root = new Container();
	root.addChild(text);
	return {
		root,
		bindings: [ref],
		visibleWhen: w.visible_when,
		width: 0, height: 0,
		render: () => {
			const v = lookup()(ref);
			text.text = formatClock(v, cfg.format ?? "");
		},
		destroy: () => root.destroy({ children: true }),
	};
}

function formatClock(raw: string, fmt: string): string {
	if (!raw) return "";
	switch (fmt) {
		case "Day N": {
			const n = Math.floor(Number(raw) / (60 * 60 * 24)) || 0;
			return `Day ${n + 1}`;
		}
		case "tick":
			return `tick ${raw}`;
		case "HH:MM":
		default: {
			const sec = Number(raw);
			if (Number.isNaN(sec)) return raw;
			const h = Math.floor(sec / 3600) % 24;
			const m = Math.floor(sec / 60) % 60;
			return `${h.toString().padStart(2, "0")}:${m.toString().padStart(2, "0")}`;
		}
	}
}

function buildIconCounter(w: Widget, opts: HudOptions, lookup: () => (b: BindingRef) => string): WidgetHandle {
	const cfg = w.config as IconCounterConfig;
	const ref = parseBinding(cfg.binding);
	const root = new Container();
	const sprite = new Sprite(); sprite.roundPixels = true;
	root.addChild(sprite);
	const text = new Text({
		text: "",
		style: { fontFamily: opts.font ?? "C64esque", fontSize: 14 * widgetSizePx(w.size), fill: colorRGB(cfg.color, 0xffffff) },
	});
	text.position.set(20 * widgetSizePx(w.size), 0);
	root.addChild(text);
	// Lazy-load icon (asset id → URL → Pixi Texture).
	void opts.textures.base(cfg.icon).then((tex) => { sprite.texture = tex as Texture; }).catch(() => {});
	return {
		root,
		bindings: [ref],
		visibleWhen: w.visible_when,
		width: 0, height: 0,
		render: () => {
			let v = lookup()(ref);
			if (cfg.pad_digits && cfg.pad_digits > 0) {
				v = v.padStart(cfg.pad_digits, "0");
			}
			text.text = `${cfg.prefix ?? ""}${v}${cfg.suffix ?? ""}`;
		},
		destroy: () => root.destroy({ children: true }),
	};
}

function buildPortrait(w: Widget, opts: HudOptions, _lookup: () => (b: BindingRef) => string): WidgetHandle {
	const cfg = w.config as PortraitConfig;
	const root = new Container();
	const sprite = new Sprite(); sprite.roundPixels = true;
	sprite.scale.set(widgetSizePx(w.size));
	root.addChild(sprite);
	void opts.textures.base(cfg.asset).then((tex) => { sprite.texture = tex as Texture; }).catch(() => {});
	const refs: BindingRef[] = cfg.binding ? [parseBinding(cfg.binding)] : [];
	return {
		root,
		bindings: refs,
		visibleWhen: w.visible_when,
		width: 0, height: 0,
		render: () => { /* portrait swap on variant change is a TODO; v1 is static */ },
		destroy: () => root.destroy({ children: true }),
	};
}

function buildButton(w: Widget, opts: HudOptions, getHandler: () => HudButtonHandler | undefined): WidgetHandle {
	const cfg = w.config as ButtonConfig;
	const root = new Container();
	const padX = 8, padY = 4;
	const fontSize = 14 * widgetSizePx(w.size);
	const text = new Text({
		text: cfg.label,
		style: { fontFamily: opts.font ?? "C64esque", fontSize, fill: 0xffffff, align: "center" },
	});
	text.position.set(padX, padY);
	const bg = new Graphics();
	root.addChild(bg);
	root.addChild(text);
	const widthPx = Math.ceil(text.width) + padX * 2;
	const heightPx = Math.ceil(text.height) + padY * 2;
	bg.rect(0, 0, widthPx, heightPx).fill(0x222244);
	bg.rect(0, 0, widthPx, heightPx).stroke({ color: 0x4caf50, width: 1, alignment: 1 });
	// Hit area grows by hit_padding_px on every side. Ensures ≥44px tap
	// targets on mobile after the integer scale step.
	const hitPad = cfg.hit_padding_px ?? 4;
	const hit = new Sprite(Texture.EMPTY);
	hit.width = widthPx + hitPad * 2;
	hit.height = heightPx + hitPad * 2;
	hit.position.set(-hitPad, -hitPad);
	hit.alpha = 0;
	hit.eventMode = "static";
	hit.cursor = "pointer";
	hit.on("pointertap", () => {
		const fn = getHandler();
		if (fn) fn(cfg.action_group);
	});
	root.addChild(hit);
	const tagged = root as Container & { __bxW?: number; __bxH?: number };
	tagged.__bxW = widthPx;
	tagged.__bxH = heightPx;
	return {
		root,
		bindings: [],
		visibleWhen: w.visible_when,
		width: widthPx, height: heightPx,
		render: () => { /* static; nothing to refresh */ },
		destroy: () => root.destroy({ children: true }),
	};
}

function buildDialogFrame(w: Widget, opts: HudOptions): WidgetHandle {
	const cfg = w.config as DialogFrameConfig;
	const widthPx = (cfg.width_px || 64) * widgetSizePx(w.size);
	const heightPx = (cfg.height_px || 32) * widgetSizePx(w.size);
	const root = new Container();
	if (w.skin && w.skin > 0) {
		const skinID = w.skin;
		// Pixi 8 NineSliceSprite: load the texture lazily, default to a
		// flat fill until it arrives so the frame doesn't pop in mid-tick.
		const flat = new Graphics();
		flat.rect(0, 0, widthPx, heightPx).fill(0x222244);
		root.addChild(flat);
		void opts.textures.base(skinID).then((tex) => {
			// Resolve insets from the entity_type's nine_slice
			// component. Falls back to symmetric 8 px when no
			// resolver was wired or the entity has no component.
			const insets = opts.sliceInsetsFor?.(skinID) ?? null;
			const left = insets?.left ?? 8;
			const top = insets?.top ?? 8;
			const right = insets?.right ?? 8;
			const bottom = insets?.bottom ?? 8;
			const nine = new NineSliceSprite({
				texture: tex as Texture,
				leftWidth: left, topHeight: top, rightWidth: right, bottomHeight: bottom,
				width: widthPx, height: heightPx,
			});
			nine.roundPixels = true;
			root.removeChild(flat);
			flat.destroy();
			root.addChild(nine);
		}).catch(() => { /* keep the flat fallback */ });
	} else {
		const flat = new Graphics();
		flat.rect(0, 0, widthPx, heightPx).fill(0x222244);
		flat.rect(0, 0, widthPx, heightPx).stroke({ color: 0x4caf50, width: 1, alignment: 1 });
		root.addChild(flat);
	}
	const tagged = root as Container & { __bxW?: number; __bxH?: number };
	tagged.__bxW = widthPx;
	tagged.__bxH = heightPx;
	return {
		root,
		bindings: [],
		visibleWhen: w.visible_when,
		width: widthPx, height: heightPx,
		render: () => { /* passive */ },
		destroy: () => root.destroy({ children: true }),
	};
}

// ---- Conditions ----

/**
 * Evaluate a condition tree against the current binding cache. The DSL
 * mirrors the server's `automations` ConditionNode (PLAN.md's no-code AST)
 * so designers learn one condition language for triggers + visible_when.
 *
 * Subjects on count_gt/count_lt/range_within are interpreted as binding
 * refs (e.g. "flag:gold > 100"). Numeric coercion uses Number(); a missing
 * binding evaluates as 0 so designers can lean on the natural "if not
 * yet set" default.
 */
export function evalCondition(node: ConditionNode, lookup: (b: BindingRef) => string): boolean {
	switch (node.op) {
		case "and": return (node.children ?? []).every((c) => evalCondition(c, lookup));
		case "or":  return (node.children ?? []).some((c) => evalCondition(c, lookup));
		case "not": return node.children && node.children[0] ? !evalCondition(node.children[0], lookup) : true;
		case "count_gt": {
			if (!node.subject || node.value === undefined) return false;
			try { return Number(lookup(parseBinding(node.subject))) > node.value; } catch { return false; }
		}
		case "count_lt": {
			if (!node.subject || node.value === undefined) return false;
			try { return Number(lookup(parseBinding(node.subject))) < node.value; } catch { return false; }
		}
		case "range_within": {
			if (!node.subject || node.min === undefined || node.max === undefined) return false;
			try {
				const v = Number(lookup(parseBinding(node.subject)));
				return v >= node.min && v <= node.max;
			} catch { return false; }
		}
	}
}
