import { describe, expect, it } from "vitest";
import { computeLayout, worldToScreen } from "./viewport";

describe("computeLayout", () => {
	it("picks the largest integer scale that fits in both dimensions", () => {
		const l = computeLayout({ canvasW: 1280, canvasH: 720, worldViewW: 320, worldViewH: 200 });
		// 1280/320 = 4, 720/200 = 3.6 → scale 3
		expect(l.scale).toBe(3);
		expect(l.scaledW).toBe(960);
		expect(l.scaledH).toBe(600);
		// Letterbox: (1280 - 960)/2 = 160, (720 - 600)/2 = 60
		expect(l.offsetX).toBe(160);
		expect(l.offsetY).toBe(60);
	});

	it("falls back to scale 1 when the canvas is smaller than the world view", () => {
		const l = computeLayout({ canvasW: 200, canvasH: 100, worldViewW: 320, worldViewH: 200 });
		expect(l.scale).toBe(1);
		// Letterbox can be negative in this regime; that's fine — the renderer
		// just crops the view.
	});

	it("never produces a fractional scale", () => {
		const l = computeLayout({ canvasW: 1599, canvasH: 899, worldViewW: 320, worldViewH: 200 });
		// 1599/320 = 4.99 → 4; 899/200 = 4.495 → 4; min = 4
		expect(l.scale).toBe(4);
		expect(Number.isInteger(l.scale)).toBe(true);
	});
});

describe("worldToScreen", () => {
	const layout = computeLayout({ canvasW: 1280, canvasH: 720, worldViewW: 320, worldViewH: 200 });
	const SUB = 256;

	it("places the camera-centered point at the layout center", () => {
		const cx = 100 * SUB;
		const cy = 100 * SUB;
		const r = worldToScreen(cx, cy, cx, cy, layout, 320, 200, SUB);
		// Center of the world view = (160, 100) virtual px → (160 * 3, 100 * 3) screen
		// then plus letterbox offset.
		expect(r.x).toBe(layout.offsetX + 160 * layout.scale);
		expect(r.y).toBe(layout.offsetY + 100 * layout.scale);
	});

	it("snaps sub-pixel positions to integer device pixels", () => {
		const cx = 100 * SUB;
		const r1 = worldToScreen(100 * SUB + 1, 100 * SUB + 1, cx, cx, layout, 320, 200, SUB);
		const r2 = worldToScreen(100 * SUB + 200, 100 * SUB + 200, cx, cx, layout, 320, 200, SUB);
		// Both inside the same px → identical output
		expect(r1).toEqual(r2);
		expect(Number.isInteger(r1.x)).toBe(true);
		expect(Number.isInteger(r1.y)).toBe(true);
	});

	it("translates with the camera", () => {
		const r0 = worldToScreen(0, 0, 0, 0, layout, 320, 200, SUB);
		const r1 = worldToScreen(0, 0, 1 * SUB, 0, layout, 320, 200, SUB);
		// Camera moved 1px east → world origin appears 1px west on screen.
		// r1.x < r0.x by exactly one device-pixel scale step.
		expect(r0.x - r1.x).toBe(layout.scale);
	});
});
