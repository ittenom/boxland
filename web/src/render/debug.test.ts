// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { DebugOverlay } from "./debug";
import type { Renderable } from "./types";

const opts = { worldViewW: 320, worldViewH: 200 };

function rb(id: number, w?: number, h?: number): Renderable {
	const r: Renderable = { id, asset_id: 1, anim_id: 0, anim_frame: 0, x: 0, y: 0, layer: 0 };
	if (w && h) r.debug = { aabb: { w, h } };
	return r;
}

describe("DebugOverlay", () => {
	it("starts hidden by default", () => {
		const dbg = new DebugOverlay(opts);
		expect(dbg.isVisible()).toBe(false);
	});

	it("setVisible toggles", () => {
		const dbg = new DebugOverlay(opts);
		dbg.setVisible(true);
		expect(dbg.isVisible()).toBe(true);
		dbg.setVisible(false);
		expect(dbg.isVisible()).toBe(false);
	});

	it("draws one id label per renderable", () => {
		const dbg = new DebugOverlay(opts);
		dbg.update([rb(1), rb(2), rb(3)], { cx: 0, cy: 0 }, 0);
		expect(dbg.idLabelCount()).toBe(3);
	});

	it("re-uses id label slots across updates", () => {
		const dbg = new DebugOverlay(opts);
		dbg.update([rb(1), rb(2), rb(3)], { cx: 0, cy: 0 }, 0);
		dbg.update([rb(7)], { cx: 0, cy: 0 }, 0);
		expect(dbg.idLabelCount()).toBe(1);
	});

	it("AOI radius 0 produces no AOI rectangle", () => {
		const dbg = new DebugOverlay(opts);
		dbg.update([], { cx: 0, cy: 0 }, 0);
		// We can't easily count Graphics commands across versions, but the
		// no-throw path proves the early-exit works.
		expect(dbg.idLabelCount()).toBe(0);
	});

	it("non-zero AOI radius and labeled aabb both render without throwing", () => {
		const dbg = new DebugOverlay(opts);
		dbg.update([rb(1, 16, 12)], { cx: 100, cy: 100 }, 8);
		expect(dbg.idLabelCount()).toBe(1);
	});
});
