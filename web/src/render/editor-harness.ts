// Boxland — editor render harness.
//
// `BoxlandApp` is fully pull-driven: caller hands it Renderables +
// camera, renderer draws. The live game / sandbox surfaces use
// `GameLoop` to drive that on a per-frame `requestAnimationFrame` tick
// because they're consuming a stream of WS diffs.
//
// The design-tool editors are different: state is mutated by user
// gestures, not by a server stream. There's no per-frame work to do
// 99% of the time — the right cadence is "redraw on dirty bit", not
// "redraw at 60 Hz forever". Doing per-frame redraws on an idle
// editor would burn battery and CPU for nothing.
//
// `EditorHarness` is the small adapter that pattern needs:
//
//   • wraps a BoxlandApp + the caller's Renderables[] + Camera
//   • coalesces multiple `markDirty()` calls per frame into one
//     `app.update()` on the next animation frame
//   • exposes mutation helpers (`setRenderables`, `setCamera`,
//     `mutate`) that all dirty-mark behind the scenes
//   • cleans up the rAF subscription on `destroy()` so editors that
//     unmount (e.g. tab switch) don't leak
//
// Verified non-coupling: this module imports nothing from @net,
// @command-bus, or @game. Pure render-side. See PLAN.md §6b "Shared
// PixiJS renderer" for why these surfaces all share BoxlandApp.

import type { BoxlandApp } from "./app";
import type { Camera, Renderable } from "./types";

/** rAF-style scheduler. Tests inject `setImmediate`-flavored fakes. */
export interface FrameScheduler {
	/** Schedule `cb` to run on the next frame. Returns a cancel handle. */
	request(cb: () => void): number;
	/** Cancel a pending frame request. No-op if already fired. */
	cancel(handle: number): void;
}

const realRAFScheduler: FrameScheduler = {
	request: (cb) => {
		// rAF doesn't exist in Node; falling back to a microtask keeps
		// jsdom tests usable without a fake. The harness production
		// path always lives in a real browser.
		if (typeof globalThis.requestAnimationFrame === "function") {
			return globalThis.requestAnimationFrame(cb) as unknown as number;
		}
		return setTimeout(cb, 16) as unknown as number;
	},
	cancel: (h) => {
		if (typeof globalThis.cancelAnimationFrame === "function") {
			globalThis.cancelAnimationFrame(h);
			return;
		}
		clearTimeout(h as unknown as ReturnType<typeof setTimeout>);
	},
};

export interface EditorHarnessOptions {
	app: BoxlandApp;
	/** Initial renderable set. Defaults to []. */
	renderables?: Renderable[];
	/** Initial camera position. Defaults to (0, 0). */
	camera?: Camera;
	/** Frame scheduler. Real rAF in production, fake in tests. */
	scheduler?: FrameScheduler;
}

/**
 * Mutable, dirty-coalescing wrapper around BoxlandApp. The editor
 * holds one of these for the lifetime of the page; tools mutate via
 * `setRenderables` / `mutate` and the harness flushes one update per
 * animation frame regardless of how many mutations happened.
 *
 * Lifecycle:
 *   const h = EditorHarness.create({ app });
 *   h.setRenderables([...]); h.setCamera({cx, cy});  // each schedules a flush
 *   // ... user interactions ...
 *   h.destroy();   // before unmounting the page
 */
export class EditorHarness {
	private renderables: Renderable[];
	private camera: Camera;
	private dirty = false;
	private pending: number | null = null;
	private destroyed = false;
	private flushing: Promise<void> | null = null;

	private constructor(
		private readonly app: BoxlandApp,
		private readonly scheduler: FrameScheduler,
		initialRenderables: Renderable[],
		initialCamera: Camera,
	) {
		this.renderables = initialRenderables;
		this.camera = initialCamera;
	}

	static create(opts: EditorHarnessOptions): EditorHarness {
		const h = new EditorHarness(
			opts.app,
			opts.scheduler ?? realRAFScheduler,
			opts.renderables ?? [],
			opts.camera ?? { cx: 0, cy: 0 },
		);
		// Schedule an initial flush so the first frame paints even if
		// the caller never calls a mutator before the user looks.
		h.markDirty();
		return h;
	}

	/** Replace the full Renderable set. Triggers one flush per rAF. */
	setRenderables(list: Renderable[]): void {
		this.renderables = list;
		this.markDirty();
	}

	/** Pan / zoom the camera. Triggers one flush per rAF. */
	setCamera(cam: Camera): void {
		this.camera = cam;
		this.markDirty();
	}

	/** Read-only snapshot. Mutating the returned array does NOT mutate
	 *  the harness — call `setRenderables` instead to make the change
	 *  visible to the renderer. */
	snapshot(): { renderables: readonly Renderable[]; camera: Readonly<Camera> } {
		return { renderables: this.renderables, camera: this.camera };
	}

	/**
	 * Coalesced mutate: call `fn` with the current array, optionally
	 * push/splice in place, and a single flush is scheduled. Useful for
	 * tools that touch a handful of placements per gesture.
	 *
	 *   harness.mutate((list) => { list.push(newPlacement); });
	 */
	mutate(fn: (list: Renderable[]) => void): void {
		fn(this.renderables);
		this.markDirty();
	}

	/** Force-schedule a redraw without changing data (useful after
	 *  texture loads complete: the data hasn't changed but the visible
	 *  result has). */
	markDirty(): void {
		if (this.destroyed) return;
		this.dirty = true;
		if (this.pending !== null) return;
		this.pending = this.scheduler.request(() => this.flush());
	}

	/** Synchronous flush — useful in tests and at unmount when we
	 *  want the final state painted immediately. Returns the same
	 *  promise the scheduled flush would have produced. */
	flushNow(): Promise<void> {
		if (this.pending !== null) {
			this.scheduler.cancel(this.pending);
			this.pending = null;
		}
		return this.flush();
	}

	/** Tear-down. Cancels any pending flush; subsequent mutators
	 *  no-op. Does NOT destroy the wrapped BoxlandApp — the caller
	 *  owns that lifecycle (the editor templ may swap tabs without
	 *  unmounting the Pixi canvas). */
	destroy(): void {
		this.destroyed = true;
		if (this.pending !== null) {
			this.scheduler.cancel(this.pending);
			this.pending = null;
		}
	}

	private flush(): Promise<void> {
		this.pending = null;
		if (this.destroyed || !this.dirty) {
			return this.flushing ?? Promise.resolve();
		}
		this.dirty = false;
		// Snapshot the inputs so a mutation that lands during the
		// in-flight `app.update` doesn't get half-applied. Setting
		// `flushing` lets concurrent callers chain on the same
		// promise rather than triggering another paint mid-update.
		const snap = this.renderables.slice();
		const cam = { cx: this.camera.cx, cy: this.camera.cy };
		this.flushing = this.app.update(snap, cam).finally(() => {
			this.flushing = null;
			// If something dirtied us while we were drawing, schedule
			// another frame. This is the only place re-entrant draws
			// originate.
			if (this.dirty && !this.destroyed) {
				this.markDirty();
			}
		});
		return this.flushing;
	}
}
