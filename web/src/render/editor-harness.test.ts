import { describe, expect, it, vi } from "vitest";
import { EditorHarness, type FrameScheduler } from "./editor-harness";
import type { BoxlandApp } from "./app";
import type { Camera, Renderable } from "./types";

/**
 * Minimal fake BoxlandApp surface. EditorHarness only ever calls
 * `app.update(renderables, camera)`, so this stub captures the
 * sequence of update calls for assertion. Casting through `unknown`
 * keeps us off the real Pixi types in node tests.
 */
function makeFakeApp(): {
	app: BoxlandApp;
	calls: Array<{ renderables: readonly Renderable[]; camera: Camera }>;
	resolveNext: () => void;
	rejectNext: (err: Error) => void;
} {
	const calls: Array<{ renderables: readonly Renderable[]; camera: Camera }> = [];
	let pending: { resolve: () => void; reject: (err: Error) => void } | null = null;
	const app = {
		async update(rs: Renderable[], cam: Camera): Promise<void> {
			calls.push({ renderables: rs, camera: cam });
			return new Promise<void>((resolve, reject) => {
				pending = { resolve, reject };
			});
		},
	} as unknown as BoxlandApp;
	return {
		app,
		calls,
		resolveNext: () => { pending?.resolve(); pending = null; },
		rejectNext: (err) => { pending?.reject(err); pending = null; },
	};
}

/** Manual frame scheduler for deterministic tests. */
function makeScheduler(): { scheduler: FrameScheduler; flush: () => void; pendingCount: () => number } {
	let next = 1;
	const queue = new Map<number, () => void>();
	return {
		scheduler: {
			request: (cb) => { const h = next++; queue.set(h, cb); return h; },
			cancel: (h) => { queue.delete(h); },
		},
		flush: () => {
			const cbs = [...queue.values()];
			queue.clear();
			for (const cb of cbs) cb();
		},
		pendingCount: () => queue.size,
	};
}

const rb = (id: number, x = 0, y = 0): Renderable => ({
	id, asset_id: 1, anim_id: 0, anim_frame: 0, x, y, layer: 0,
});

describe("EditorHarness scheduling", () => {
	it("paints once on first frame even without explicit mutations", () => {
		const { app, calls, resolveNext } = makeFakeApp();
		const { scheduler, flush } = makeScheduler();
		const h = EditorHarness.create({ app, scheduler });
		flush();
		resolveNext();
		expect(calls.length).toBe(1);
		expect(calls[0]?.renderables).toEqual([]);
		expect(calls[0]?.camera).toEqual({ cx: 0, cy: 0 });
		h.destroy();
	});

	it("coalesces multiple mutations within a frame into one update call", () => {
		const { app, calls, resolveNext } = makeFakeApp();
		const { scheduler, flush, pendingCount } = makeScheduler();
		const h = EditorHarness.create({ app, scheduler, renderables: [rb(1)] });
		// Five rapid mutations before the next frame fires.
		h.setRenderables([rb(1), rb(2)]);
		h.setRenderables([rb(1), rb(2), rb(3)]);
		h.setCamera({ cx: 100, cy: 200 });
		h.markDirty();
		h.setRenderables([rb(1)]);
		expect(pendingCount()).toBe(1);  // exactly one rAF queued
		flush();
		resolveNext();
		expect(calls.length).toBe(1);
		// The flush sees the LATEST snapshot, not any in-between state.
		expect(calls[0]?.renderables.map((r) => r.id)).toEqual([1]);
		expect(calls[0]?.camera).toEqual({ cx: 100, cy: 200 });
		h.destroy();
	});

	it("flushes again on a subsequent frame when new mutations land mid-flush", async () => {
		const { app, calls, resolveNext } = makeFakeApp();
		const { scheduler, flush } = makeScheduler();
		const h = EditorHarness.create({ app, scheduler, renderables: [rb(1)] });
		flush();   // schedules first paint
		// Mutate while the in-flight update is still pending.
		h.setRenderables([rb(1), rb(2)]);
		// Resolve the first update -> harness should observe the dirty
		// flag set by the mid-flush mutation and queue another frame.
		resolveNext();
		// Drain: wait a microtask so the .finally() chain runs, then
		// flush the next scheduled frame.
		await Promise.resolve();
		flush();
		resolveNext();
		await Promise.resolve();
		expect(calls.length).toBe(2);
		expect(calls[1]?.renderables.map((r) => r.id)).toEqual([1, 2]);
		h.destroy();
	});

	it("destroy() cancels pending flush and ignores subsequent mutations", () => {
		const { app, calls } = makeFakeApp();
		const { scheduler, flush, pendingCount } = makeScheduler();
		const h = EditorHarness.create({ app, scheduler });
		expect(pendingCount()).toBe(1);
		h.destroy();
		expect(pendingCount()).toBe(0);
		// Mutations after destroy do not schedule new frames.
		h.setRenderables([rb(1)]);
		expect(pendingCount()).toBe(0);
		// And nothing was painted.
		flush();
		expect(calls.length).toBe(0);
	});
});

describe("EditorHarness mutation API", () => {
	it("mutate() exposes the in-place array and schedules a flush", () => {
		const { app, calls, resolveNext } = makeFakeApp();
		const { scheduler, flush } = makeScheduler();
		const h = EditorHarness.create({ app, scheduler, renderables: [rb(1)] });
		h.mutate((list) => { list.push(rb(2)); list.push(rb(3)); });
		flush();
		resolveNext();
		expect(calls[0]?.renderables.map((r) => r.id)).toEqual([1, 2, 3]);
		h.destroy();
	});

	it("snapshot() returns the current renderables and camera by reference (read-only)", () => {
		const { app } = makeFakeApp();
		const { scheduler } = makeScheduler();
		const h = EditorHarness.create({ app, scheduler, renderables: [rb(1)], camera: { cx: 5, cy: 6 } });
		const snap = h.snapshot();
		expect(snap.renderables).toEqual([rb(1)]);
		expect(snap.camera).toEqual({ cx: 5, cy: 6 });
		h.destroy();
	});

	it("flushNow() bypasses the scheduler and resolves on the in-flight update", async () => {
		const { app, calls, resolveNext } = makeFakeApp();
		const { scheduler, pendingCount } = makeScheduler();
		const h = EditorHarness.create({ app, scheduler, renderables: [rb(1)] });
		const p = h.flushNow();
		expect(pendingCount()).toBe(0);   // synchronous bypass cancelled the rAF
		resolveNext();
		await p;
		expect(calls.length).toBe(1);
		h.destroy();
	});
});

describe("EditorHarness real-rAF fallback", () => {
	it("falls back to setTimeout when requestAnimationFrame is undefined (node env)", async () => {
		const { app, calls, resolveNext } = makeFakeApp();
		// No scheduler arg -> uses realRAFScheduler -> falls back to setTimeout in node.
		vi.useFakeTimers();
		const h = EditorHarness.create({ app });
		await vi.advanceTimersByTimeAsync(20);
		resolveNext();
		expect(calls.length).toBe(1);
		h.destroy();
		vi.useRealTimers();
	});
});
