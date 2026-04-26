// @vitest-environment jsdom
//
// Pixi-side HUD reconciliation tests. We use a stub TextureCache so the
// tests never touch the GPU; the geometry, anchor math, binding fan-out,
// visible_when toggling, and click hit-testing all run headless.
//
// JSDOM has no real <canvas>, but Pixi's Text widget calls measureText on
// a 2D context to compute width/height. We monkey-patch a minimal stub so
// Text construction succeeds without dragging in node-canvas. The numbers
// are obviously bogus -- we don't assert on them; we only need the lifecycle
// to run.

import { describe, expect, it, vi, beforeAll } from "vitest";

beforeAll(() => {
	const proto = HTMLCanvasElement.prototype as unknown as {
		getContext: (kind: string) => unknown;
	};
	const orig = proto.getContext;
	proto.getContext = function (kind: string) {
		if (kind === "2d" || kind === "2d-ish") {
			return {
				font: "",
				measureText(text: string) {
					return {
						width: text.length * 6,
						actualBoundingBoxAscent: 8,
						actualBoundingBoxDescent: 2,
					};
				},
				fillText() {},
				strokeText() {},
				clearRect() {},
				fillRect() {},
				strokeRect() {},
				save() {}, restore() {},
				translate() {}, scale() {}, rotate() {},
				beginPath() {}, closePath() {},
				moveTo() {}, lineTo() {},
				fill() {}, stroke() {},
				createLinearGradient() { return { addColorStop() {} }; },
				canvas: this,
				getImageData() { return { data: new Uint8ClampedArray(4) }; },
				putImageData() {},
				drawImage() {},
				setTransform() {}, transform() {},
				globalAlpha: 1, globalCompositeOperation: "source-over",
				textBaseline: "alphabetic", textAlign: "left",
				fillStyle: "", strokeStyle: "", lineWidth: 1,
			};
		}
		return orig.call(this, kind);
	};
});

import { Hud } from "./hud";
import { TextureCache } from "./textures";
import type { AssetCatalog } from "./types";
import {
	type Layout,
	type Widget,
	bindingString,
} from "./hud-types";

const stubCatalog: AssetCatalog = {
	urlFor: () => "data:,",
	frame: () => undefined,
};

function makeHud(): Hud {
	return new Hud({
		worldViewW: 480,
		worldViewH: 320,
		textures: new TextureCache(stubCatalog),
		urlFor: stubCatalog.urlFor,
	});
}

function widget(type: Widget["type"], config: unknown, opts: Partial<Widget> = {}): Widget {
	return { type, order: opts.order ?? 0, config, ...opts };
}

describe("Hud.mount", () => {
	it("mounts widgets across multiple anchors", () => {
		const h = makeHud();
		const layout: Layout = {
			v: 1,
			anchors: {
				"top-left":     { dir: "vertical", gap: 2, offsetX: 4, offsetY: 4, widgets: [
					widget("text_label", { template: "Gold: {flag:gold}" }),
				]},
				"bottom-right": { dir: "horizontal", gap: 2, offsetX: 4, offsetY: 4, widgets: [
					widget("resource_bar", { binding: "entity:host:hp_pct" }),
				]},
			},
		};
		h.mount(layout);
		expect(h.widgetCount()).toBe(2);
		expect(h.bindingKeys().sort()).toEqual(["entity:host:hp_pct", "flag:gold"]);
	});

	it("dedupes bindings across widgets", () => {
		const h = makeHud();
		const layout: Layout = {
			v: 1,
			anchors: {
				"top-right": { dir: "vertical", gap: 0, offsetX: 0, offsetY: 0, widgets: [
					widget("icon_counter", { icon: 1, binding: "flag:gold" }),
					widget("text_label",   { template: "{flag:gold}" }, { order: 1 }),
				]},
			},
		};
		h.mount(layout);
		expect(h.bindingKeys()).toEqual(["flag:gold"]);
	});

	it("respects widget `order` for stack placement", () => {
		const h = makeHud();
		const layout: Layout = {
			v: 1,
			anchors: {
				"top-left": { dir: "vertical", gap: 0, offsetX: 0, offsetY: 0, widgets: [
					widget("text_label", { template: "B" }, { order: 2 }),
					widget("text_label", { template: "A" }, { order: 1 }),
				]},
			},
		};
		h.mount(layout);
		// First child of the anchor container is the lowest-order widget.
		const stack = h.root.children[0]!;
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		const firstLabel = (stack as any).children[0].children[0];
		expect(firstLabel.text).toBe("A");
	});
});

describe("Hud.update", () => {
	it("re-renders only widgets bound to the changed binding", () => {
		const h = makeHud();
		const layout: Layout = {
			v: 1,
			anchors: {
				"top-left": { dir: "vertical", gap: 0, offsetX: 0, offsetY: 0, widgets: [
					widget("text_label", { template: "Gold: {flag:gold}" }),
					widget("text_label", { template: "HP: {entity:host:hp_pct}" }, { order: 1 }),
				]},
			},
		};
		h.mount(layout);
		// binding ids: alphabetical -> 0=entity:host:hp_pct, 1=flag:gold
		h.update(1, { kind: "int", value: 42 });
		const stack = h.root.children[0]!;
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		const stackChildren = (stack as any).children;
		expect(stackChildren[0].children[0].text).toBe("Gold: 42");
		// HP widget unchanged (still empty substitution).
		expect(stackChildren[1].children[0].text).toBe("HP: ");
	});

	it("ignores updates for unknown binding ids", () => {
		const h = makeHud();
		h.mount({ v: 1, anchors: {} });
		expect(() => h.update(999, { kind: "int", value: 0 })).not.toThrow();
	});
});

describe("Hud visible_when", () => {
	it("hides widgets whose condition evaluates false and shows them when true", () => {
		const h = makeHud();
		const layout: Layout = {
			v: 1,
			anchors: {
				"top-left": { dir: "vertical", gap: 0, offsetX: 0, offsetY: 0, widgets: [
					{
						type: "text_label",
						order: 0,
						visible_when: { op: "count_gt", subject: "flag:gold", value: 100 },
						config: { template: "Rich!" },
					},
				]},
			},
		};
		h.mount(layout);
		expect(h.visibleWidgetCount()).toBe(0);
		// alphabetical: only one binding
		h.update(0, { kind: "int", value: 200 });
		expect(h.visibleWidgetCount()).toBe(1);
		h.update(0, { kind: "int", value: 50 });
		expect(h.visibleWidgetCount()).toBe(0);
	});
});

describe("Hud button click", () => {
	it("invokes the wired handler with the action_group name", () => {
		const h = makeHud();
		const handler = vi.fn();
		h.setOnButton(handler);
		h.mount({
			v: 1,
			anchors: {
				"bottom-center": { dir: "horizontal", gap: 0, offsetX: 0, offsetY: 0, widgets: [
					widget("button", { label: "Save", action_group: "save_game" }),
				]},
			},
		});
		// Dive into the mounted widget to fire the hit-area's pointertap.
		const stack = h.root.children[0]!;
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		const button = (stack as any).children[0];
		const hit = button.children[2]; // [bg, text, hit]
		hit.emit("pointertap");
		expect(handler).toHaveBeenCalledWith("save_game");
	});
});

describe("Hud.resize", () => {
	it("updates root scale and re-runs anchor math", () => {
		const h = makeHud();
		h.mount({
			v: 1,
			anchors: {
				"bottom-right": { dir: "vertical", gap: 0, offsetX: 8, offsetY: 8, widgets: [
					widget("text_label", { template: "x" }),
				]},
			},
		});
		h.resize(960, 640, 2);
		expect(h.root.scale.x).toBe(2);
		// Stack root should be near (viewportW/scale - offsetX, viewportH/scale - offsetY)
		// = (480 - 8, 320 - 8) = (472, 312).
		const stack = h.root.children[0]!;
		expect(stack.position.x).toBe(472);
		expect(stack.position.y).toBe(312);
	});
});

describe("Hud bindingKeys", () => {
	it("returns an empty list for an empty layout", () => {
		const h = makeHud();
		h.mount({ v: 1, anchors: {} });
		expect(h.bindingKeys()).toEqual([]);
	});

	it("string ids are stable across mounts", () => {
		const h = makeHud();
		const w1 = widget("icon_counter", { icon: 1, binding: "flag:apples" });
		const w2 = widget("icon_counter", { icon: 1, binding: "flag:bananas" });
		h.mount({ v: 1, anchors: { "top-left": { dir: "vertical", gap: 0, offsetX: 0, offsetY: 0, widgets: [w1, w2] } } });
		expect(h.bindingKeys()).toEqual(["flag:apples", "flag:bananas"]);
		h.mount({ v: 1, anchors: { "top-left": { dir: "vertical", gap: 0, offsetX: 0, offsetY: 0, widgets: [w2, w1] } } });
		// Sorted, so order is the same.
		expect(h.bindingKeys()).toEqual(["flag:apples", "flag:bananas"]);
	});
});

describe("bindingString", () => {
	it("matches the canonical wire string", () => {
		expect(bindingString({ kind: "flag", key: "gold" })).toBe("flag:gold");
		expect(bindingString({ kind: "entity", key: "host", sub: "hp_pct" })).toBe("entity:host:hp_pct");
	});
});
