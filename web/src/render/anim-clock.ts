// Boxland — client-side animation frame clock.
//
// The server picks the animation (anim_id) from movement; the server
// does NOT step `anim_frame` per tick. The wall-clock frame index is
// the renderer's responsibility — it has the FPS info and runs at
// monitor refresh, so it can interpolate smoothly between server ticks
// without burning bandwidth on per-frame updates.
//
// The clock keeps a tiny per-(entity, anim) phase counter. Switching
// animations resets phase to 0, so a freshly-issued `walk_east` clip
// starts at frame_from rather than picking up wherever the previous
// clip happened to be. Entities that drop out of the renderable list
// have their phase entries garbage-collected on the next tick.
//
// Pure / headless / test-friendly. Scene.update calls `tick()` right
// before the texture lookup.

import type { AnimId, AssetId, EntityId, Renderable } from "./types";

/** Animation row shape the clock needs to step a clip. Mirrors
 *  the catalog's CatalogAnimation but kept local so this module
 *  doesn't import the catalog (Mapmaker preview surfaces use it
 *  with a simpler resolver). */
export interface ClockAnim {
	frame_from: number;
	frame_to: number;
	fps: number;
	direction: "forward" | "reverse" | "pingpong";
}

/** Resolver maps (asset_id, anim_id) → ClockAnim, or undefined when
 *  the animation isn't known. RemoteAssetCatalog implements this
 *  signature directly via animationByID. */
export type AnimResolver = (assetId: AssetId, animId: AnimId) => ClockAnim | undefined;

interface PhaseEntry {
	animId: AnimId;
	startedAtMs: number;
}

/**
 * AnimClock advances the per-frame index for every renderable based
 * on wall-clock time. Stateless from the renderer's POV — feed the
 * current time and the renderable list, get back the same list with
 * `anim_frame` rewritten to the current cycle position.
 *
 * Mutates the input renderables in place. The renderer treats them
 * as ephemeral per-frame snapshots so this is fine; if you need
 * immutable inputs, clone before passing in.
 */
export class AnimClock {
	/** EntityId → current animation phase. */
	private readonly phases = new Map<EntityId, PhaseEntry>();
	/** Set rebuilt on every tick — anything missing gets dropped from
	 *  `phases` so a sprite that left AOI doesn't leak its phase entry. */
	private readonly seen = new Set<EntityId>();

	tick(nowMs: number, renderables: Renderable[], resolve: AnimResolver): void {
		this.seen.clear();
		for (const r of renderables) {
			this.seen.add(r.id);
			const anim = resolve(r.asset_id, r.anim_id);
			if (!anim) {
				// No clip data — leave whatever frame index the wire
				// carried alone. (Renderable.anim_frame typically 0
				// for fresh joins.)
				continue;
			}
			let phase = this.phases.get(r.id);
			if (!phase || phase.animId !== r.anim_id) {
				phase = { animId: r.anim_id, startedAtMs: nowMs };
				this.phases.set(r.id, phase);
			}
			r.anim_frame = computeFrame(anim, nowMs - phase.startedAtMs);
		}
		// Drop phases for entities that disappeared this tick.
		if (this.phases.size > this.seen.size) {
			for (const id of this.phases.keys()) {
				if (!this.seen.has(id)) this.phases.delete(id);
			}
		}
	}

	/** Test helper: number of tracked phases. */
	size(): number {
		return this.phases.size;
	}

	/** Test helper: forget everything. Useful between scenarios. */
	reset(): void {
		this.phases.clear();
		this.seen.clear();
	}
}

/**
 * computeFrame returns the absolute frame index inside [frame_from,
 * frame_to] for the given clip after `elapsedMs` since the cycle
 * started. Branches on direction:
 *
 *   forward  → wraps through frame_from..frame_to
 *   reverse  → wraps through frame_to..frame_from
 *   pingpong → bounces frame_from..frame_to..frame_from
 *
 * A 1-frame clip (frame_from == frame_to) trivially returns
 * frame_from regardless of fps; division-by-zero guarded for
 * malformed catalog entries with fps <= 0.
 *
 * Returned index is the **animation-local** frame number — i.e.
 * 0..(frame_to - frame_from) — which is what the renderer's
 * AssetCatalog.frame() expects (it adds frame_from internally).
 */
export function computeFrame(anim: ClockAnim, elapsedMs: number): number {
	const len = anim.frame_to - anim.frame_from + 1;
	if (len <= 1 || anim.fps <= 0) return 0;
	const frameMs = 1000 / anim.fps;
	const step = Math.floor(Math.max(0, elapsedMs) / frameMs);
	switch (anim.direction) {
		case "reverse": {
			// Walks the cycle backwards. step % len gives the offset
			// from frame_to; we return as a 0-based offset from
			// frame_from for symmetry with `forward`.
			const off = step % len;
			return len - 1 - off;
		}
		case "pingpong": {
			// Period covers forward then backward, sharing the endpoints
			// (so a 4-frame clip's period is 2*(4-1) = 6 steps:
			// 0,1,2,3,2,1, then repeat). Endpoint sharing prevents the
			// "bounce" from looking like a stutter.
			const period = 2 * (len - 1);
			const phase = step % period;
			return phase < len ? phase : period - phase;
		}
		case "forward":
		default:
			return step % len;
	}
}
