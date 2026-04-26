import { describe, expect, it } from "vitest";
import { AnimClock, computeFrame, type ClockAnim } from "./anim-clock";
import type { Renderable } from "./types";

const fwd = (over: Partial<ClockAnim> = {}): ClockAnim => ({
	frame_from: 0,
	frame_to: 3,
	fps: 8,
	direction: "forward",
	...over,
});

function r(over: Partial<Renderable>): Renderable {
	return {
		id: 1,
		asset_id: 10,
		anim_id: 100,
		anim_frame: 0,
		x: 0,
		y: 0,
		layer: 0,
		...over,
	};
}

describe("computeFrame", () => {
	it("forward wraps within the clip length", () => {
		const a = fwd({ frame_from: 0, frame_to: 3, fps: 8 });
		// 1000/8 = 125ms per frame
		expect(computeFrame(a, 0)).toBe(0);
		expect(computeFrame(a, 124)).toBe(0);
		expect(computeFrame(a, 125)).toBe(1);
		expect(computeFrame(a, 250)).toBe(2);
		expect(computeFrame(a, 375)).toBe(3);
		expect(computeFrame(a, 500)).toBe(0); // wrap
	});
	it("reverse walks backwards from the end", () => {
		const a = fwd({ direction: "reverse" });
		expect(computeFrame(a, 0)).toBe(3);
		expect(computeFrame(a, 125)).toBe(2);
		expect(computeFrame(a, 250)).toBe(1);
		expect(computeFrame(a, 375)).toBe(0);
		expect(computeFrame(a, 500)).toBe(3); // wrap
	});
	it("pingpong bounces with shared endpoints", () => {
		const a = fwd({ direction: "pingpong" }); // 4 frames
		// Period = 2*(4-1) = 6 steps: 0,1,2,3,2,1
		const seq = [0, 1, 2, 3, 2, 1, 0, 1, 2, 3];
		for (let i = 0; i < seq.length; i++) {
			expect(computeFrame(a, i * 125)).toBe(seq[i]);
		}
	});
	it("single-frame clip is always 0", () => {
		const a = fwd({ frame_from: 5, frame_to: 5 });
		expect(computeFrame(a, 0)).toBe(0);
		expect(computeFrame(a, 9999)).toBe(0);
	});
	it("zero / negative fps is treated as 0", () => {
		const a = fwd({ fps: 0 });
		expect(computeFrame(a, 9999)).toBe(0);
	});
	it("negative elapsed is clamped to 0", () => {
		const a = fwd();
		expect(computeFrame(a, -500)).toBe(0);
	});
});

describe("AnimClock", () => {
	it("rewrites anim_frame in place using the resolver", () => {
		const clock = new AnimClock();
		const renderables = [r({ id: 1, anim_id: 100 })];
		const resolve = () => fwd();
		clock.tick(0, renderables, resolve);
		expect(renderables[0]!.anim_frame).toBe(0);
		clock.tick(125, renderables, resolve);
		expect(renderables[0]!.anim_frame).toBe(1);
	});

	it("resets phase when an entity's anim_id changes", () => {
		const clock = new AnimClock();
		const r1 = r({ id: 1, anim_id: 100 });
		clock.tick(0, [r1], () => fwd());
		// Run for several frames worth of time on anim 100.
		clock.tick(500, [r1], () => fwd());
		// Switch to anim 200 — should start fresh at frame 0 even
		// though wall-clock elapsed is large.
		const r2 = r({ id: 1, anim_id: 200 });
		clock.tick(550, [r2], () => fwd());
		expect(r2.anim_frame).toBe(0);
	});

	it("garbage-collects phases when entities drop out", () => {
		const clock = new AnimClock();
		clock.tick(0, [r({ id: 1 }), r({ id: 2 }), r({ id: 3 })], () => fwd());
		expect(clock.size()).toBe(3);
		clock.tick(125, [r({ id: 2 })], () => fwd()); // 1 and 3 gone
		expect(clock.size()).toBe(1);
	});

	it("leaves anim_frame alone when the resolver returns undefined", () => {
		const clock = new AnimClock();
		const renderables = [r({ id: 1, anim_id: 0, anim_frame: 7 })];
		clock.tick(500, renderables, () => undefined);
		expect(renderables[0]!.anim_frame).toBe(7);
	});

	it("keeps phase across same-anim ticks (no reset on identical anim_id)", () => {
		const clock = new AnimClock();
		const r1 = r({ id: 1, anim_id: 100 });
		clock.tick(0, [r1], () => fwd());
		clock.tick(125, [r1], () => fwd());
		// Frame should match the time-since-start, not "since last tick".
		expect(r1.anim_frame).toBe(1);
	});
});
