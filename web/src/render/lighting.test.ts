// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { LightingLayer } from "./lighting";

describe("LightingLayer", () => {
	it("uses multiply blend mode on its container", () => {
		const lite = new LightingLayer({ worldViewW: 320, worldViewH: 200 });
		expect(lite.root.blendMode).toBe("multiply");
	});

	it("renders one rect per cell (Pixi Graphics queues at least one draw call when cells exist)", () => {
		const lite = new LightingLayer({ worldViewW: 320, worldViewH: 200 });
		expect(lite.geometryCount()).toBe(0);
		lite.update(
			[
				{ gx: 0, gy: 0, color: 0x000000ff, intensity: 200 },
				{ gx: 1, gy: 0, color: 0xffd34aff, intensity: 100 },
			],
			{ cx: 0, cy: 0 },
		);
		expect(lite.geometryCount()).toBeGreaterThan(0);
	});

	it("clears geometry when given an empty list", () => {
		const lite = new LightingLayer({ worldViewW: 320, worldViewH: 200 });
		lite.update([{ gx: 0, gy: 0, color: 0x000000ff, intensity: 255 }], { cx: 0, cy: 0 });
		lite.update([], { cx: 0, cy: 0 });
		expect(lite.geometryCount()).toBe(0);
	});
});
