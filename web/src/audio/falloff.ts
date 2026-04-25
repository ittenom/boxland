// Boxland — audio/falloff.ts
//
// Pure math for positional audio. Pulled out of the engine class so
// tests can verify the panning + falloff curves without a live
// AudioContext.

import {
	DEFAULT_FALLOFF_INNER_SUB,
	DEFAULT_FALLOFF_OUTER_SUB,
} from "./types";

export interface FalloffConfig {
	innerSub: number;
	outerSub: number;
}

export const DEFAULT_FALLOFF: FalloffConfig = {
	innerSub: DEFAULT_FALLOFF_INNER_SUB,
	outerSub: DEFAULT_FALLOFF_OUTER_SUB,
};

/** Linear distance falloff. Returns a multiplier in [0..1]:
 *   * 1 if distance <= inner
 *   * 0 if distance >= outer
 *   * lerp otherwise.
 *
 * Linear is intentional — exponential falloff sounds great in a 3D
 * engine but we're a 2D pixel game where sounds need to read as
 * "near-loud, far-quiet" without crunching the dynamic range. */
export function distanceGain(
	listenerX: number, listenerY: number,
	sourceX: number, sourceY: number,
	cfg: FalloffConfig = DEFAULT_FALLOFF,
): number {
	const dx = sourceX - listenerX;
	const dy = sourceY - listenerY;
	const dist = Math.hypot(dx, dy);
	if (dist <= cfg.innerSub) return 1;
	if (dist >= cfg.outerSub) return 0;
	const span = cfg.outerSub - cfg.innerSub;
	if (span <= 0) return 0;
	return 1 - (dist - cfg.innerSub) / span;
}

/** Stereo pan in [-1..1] derived from the source's world-X relative
 *  to the listener. Falls back to centre (0) for non-positional or
 *  vertically-aligned sources within the inner falloff radius (so
 *  ambient on-top sounds don't flicker between channels). */
export function pan(
	listenerX: number,
	sourceX: number,
	innerSub = DEFAULT_FALLOFF_INNER_SUB,
): number {
	const dx = sourceX - listenerX;
	if (Math.abs(dx) <= innerSub) {
		// Within the "headphone null zone" — pan proportionally so we
		// don't get a hard centre then sudden L/R flip.
		return clamp(dx / innerSub, -1, 1);
	}
	return dx > 0 ? 1 : -1;
}

function clamp(n: number, lo: number, hi: number): number {
	if (n < lo) return lo;
	if (n > hi) return hi;
	return n;
}

/** Map "cents" to a Web Audio playbackRate multiplier. 1200 cents per
 *  octave (factor of 2). 0 cents = unmodified (rate 1). */
export function pitchToRate(cents: number): number {
	if (cents === 0) return 1;
	return Math.pow(2, cents / 1200);
}
