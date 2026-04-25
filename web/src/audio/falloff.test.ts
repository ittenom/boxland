import { describe, it, expect } from "vitest";

import {
	distanceGain, pan, pitchToRate,
	DEFAULT_FALLOFF,
} from "./falloff";

describe("distanceGain", () => {
	it("returns 1 inside the inner radius", () => {
		expect(distanceGain(0, 0, 0, 0)).toBe(1);
		expect(distanceGain(0, 0, DEFAULT_FALLOFF.innerSub - 1, 0)).toBe(1);
	});

	it("returns 0 at or beyond the outer radius", () => {
		expect(distanceGain(0, 0, DEFAULT_FALLOFF.outerSub, 0)).toBe(0);
		expect(distanceGain(0, 0, DEFAULT_FALLOFF.outerSub * 2, 0)).toBe(0);
	});

	it("lerps linearly between inner and outer", () => {
		const mid = (DEFAULT_FALLOFF.innerSub + DEFAULT_FALLOFF.outerSub) / 2;
		const g = distanceGain(0, 0, mid, 0);
		expect(g).toBeGreaterThan(0.45);
		expect(g).toBeLessThan(0.55);
	});

	it("respects custom inner/outer config", () => {
		const cfg = { innerSub: 100, outerSub: 200 };
		expect(distanceGain(0, 0, 100, 0, cfg)).toBe(1);
		expect(distanceGain(0, 0, 150, 0, cfg)).toBeCloseTo(0.5, 5);
		expect(distanceGain(0, 0, 200, 0, cfg)).toBe(0);
	});
});

describe("pan", () => {
	it("centres a source at the listener", () => {
		expect(pan(100, 100)).toBe(0);
	});
	it("returns +1 at far right, -1 at far left", () => {
		expect(pan(0, 1_000_000)).toBe(1);
		expect(pan(0, -1_000_000)).toBe(-1);
	});
	it("ramps linearly within the inner radius", () => {
		const inner = 100;
		expect(pan(0, 50, inner)).toBeCloseTo(0.5, 5);
		expect(pan(0, -25, inner)).toBeCloseTo(-0.25, 5);
	});
});

describe("pitchToRate", () => {
	it("returns 1 for 0 cents", () => {
		expect(pitchToRate(0)).toBe(1);
	});
	it("returns 2 for +1200 cents (one octave up)", () => {
		expect(pitchToRate(1200)).toBeCloseTo(2, 5);
	});
	it("returns 0.5 for -1200 cents (one octave down)", () => {
		expect(pitchToRate(-1200)).toBeCloseTo(0.5, 5);
	});
});
