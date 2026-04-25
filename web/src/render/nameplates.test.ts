// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { Graphics } from "pixi.js";

import {
	NameplateLayer,
	shouldShow,
	barWidth,
	drawHpBar,
	HP_BAR_WIDTH_PX,
	NO_HP_BAR,
} from "./nameplates";
import type { Camera, Renderable } from "./types";

const cam: Camera = { cx: 0, cy: 0 };

function ent(over: Partial<Renderable> = {}): Renderable {
	return {
		id: 1,
		asset_id: 0, anim_id: 0, anim_frame: 0,
		x: 0, y: 0,
		layer: 0,
		...over,
	};
}

describe("shouldShow", () => {
	it("hides when neither name nor HP bar is requested", () => {
		expect(shouldShow(ent())).toBe(false);
		expect(shouldShow(ent({ nameplate: "" }))).toBe(false);
		expect(shouldShow(ent({ hpPct: NO_HP_BAR }))).toBe(false);
	});

	it("shows when nameplate is non-empty", () => {
		expect(shouldShow(ent({ nameplate: "Alice" }))).toBe(true);
	});

	it("shows when hp_pct is in [0..100]", () => {
		expect(shouldShow(ent({ hpPct: 0 }))).toBe(true);
		expect(shouldShow(ent({ hpPct: 50 }))).toBe(true);
		expect(shouldShow(ent({ hpPct: 100 }))).toBe(true);
	});
});

describe("barWidth", () => {
	it("0% -> 0 px and 100% -> full width", () => {
		expect(barWidth(0)).toBe(0);
		expect(barWidth(100)).toBe(HP_BAR_WIDTH_PX);
	});

	it("50% is half width (rounded)", () => {
		expect(barWidth(50)).toBe(Math.round(HP_BAR_WIDTH_PX * 0.5));
	});

	it("clamps negatives to 0 and >100 to full", () => {
		expect(barWidth(-5)).toBe(0);
		expect(barWidth(150)).toBe(HP_BAR_WIDTH_PX);
	});

	it("treats non-finite inputs as zero (fail-safe)", () => {
		expect(barWidth(NaN)).toBe(0);
		expect(barWidth(Infinity)).toBe(0);
		expect(barWidth(-Infinity)).toBe(0);
	});
});

describe("drawHpBar", () => {
	it("does not throw for any well-formed pct", () => {
		const g = new Graphics();
		expect(() => drawHpBar(g, 0)).not.toThrow();
		expect(() => drawHpBar(g, 50)).not.toThrow();
		expect(() => drawHpBar(g, 100)).not.toThrow();
	});
});

describe("NameplateLayer", () => {
	const opts = { worldViewW: 480, worldViewH: 320 };

	it("creates an overlay only for entities with name or HP", () => {
		const layer = new NameplateLayer(opts);
		layer.update([
			ent({ id: 1 }),                                  // hidden
			ent({ id: 2, nameplate: "Bob" }),                // shown
			ent({ id: 3, hpPct: 30 }),                       // shown
			ent({ id: 4, nameplate: "C", hpPct: 80 }),       // shown
		], cam);
		expect(layer.size()).toBe(3);
		expect(layer.getOverlay(1)).toBeUndefined();
		expect(layer.getOverlay(2)?.text).toBeTruthy();
		expect(layer.getOverlay(2)?.bar).toBeNull();
		expect(layer.getOverlay(3)?.text).toBeNull();
		expect(layer.getOverlay(3)?.bar).toBeTruthy();
		expect(layer.getOverlay(4)?.text).toBeTruthy();
		expect(layer.getOverlay(4)?.bar).toBeTruthy();
	});

	it("updates an existing overlay rather than recreating it", () => {
		const layer = new NameplateLayer(opts);
		layer.update([ent({ id: 1, nameplate: "A" })], cam);
		const before = layer.getOverlay(1)!;
		const textBefore = before.text;
		layer.update([ent({ id: 1, nameplate: "B" })], cam);
		const after = layer.getOverlay(1)!;
		expect(after.text).toBe(textBefore); // same Text node reused
		expect(after.text?.text).toBe("B");
	});

	it("tears down overlays for entities that dropped out", () => {
		const layer = new NameplateLayer(opts);
		layer.update([ent({ id: 1, nameplate: "A" }), ent({ id: 2, nameplate: "B" })], cam);
		expect(layer.size()).toBe(2);
		layer.update([ent({ id: 1, nameplate: "A" })], cam);
		expect(layer.size()).toBe(1);
		expect(layer.getOverlay(2)).toBeUndefined();
	});

	it("removes only the bar (not the text) when HP transitions to NO_HP_BAR", () => {
		const layer = new NameplateLayer(opts);
		layer.update([ent({ id: 1, nameplate: "A", hpPct: 50 })], cam);
		expect(layer.getOverlay(1)?.bar).toBeTruthy();
		layer.update([ent({ id: 1, nameplate: "A", hpPct: NO_HP_BAR })], cam);
		expect(layer.getOverlay(1)?.bar).toBeNull();
		expect(layer.getOverlay(1)?.text).toBeTruthy();
	});

	it("removes only the text (not the bar) when nameplate clears", () => {
		const layer = new NameplateLayer(opts);
		layer.update([ent({ id: 1, nameplate: "A", hpPct: 50 })], cam);
		expect(layer.getOverlay(1)?.text).toBeTruthy();
		layer.update([ent({ id: 1, hpPct: 50 })], cam);
		expect(layer.getOverlay(1)?.text).toBeNull();
		expect(layer.getOverlay(1)?.bar).toBeTruthy();
	});

	it("positions overlays via worldToScreen", () => {
		const layer = new NameplateLayer(opts);
		layer.update([ent({ id: 1, nameplate: "X", x: 0, y: 0 })], cam);
		const o = layer.getOverlay(1)!;
		// Camera at (0,0), source at (0,0) -> centre of view.
		expect(o.root.position.x).toBeCloseTo(opts.worldViewW / 2, 0);
		expect(o.root.position.y).toBeCloseTo(opts.worldViewH / 2, 0);
	});
});
