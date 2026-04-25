// Boxland — game/prediction.ts
//
// Client-side prediction + reconciliation. The server is authoritative
// at 10 Hz; the client interpolates between ticks and predicts the
// host entity's movement so input feels immediate.
//
// PLAN.md §1 "Movement: Free pixel coords + AABB entity colliders" +
// §6h "Client-side prediction using the shared collision module;
// server reconciles". The shared collision module (web/src/collision)
// is a literal port of the Go implementation — same fixed-point math,
// same swept-AABB algorithm — so the predicted position and the
// authoritative one only diverge under packet loss / desync, not
// implementation drift.
//
// This module is pure. The orchestrator passes (LocalState, intent,
// dtMs, world); the module returns the next LocalState. Reconciliation
// is a separate function that takes the freshly-applied server position
// and adjusts LocalState toward truth without snapping unless the gap
// exceeds RECONCILE_HARD_SNAP_SUB.

import { move, type Entity, type World } from "@collision";
import { SUB_PER_PX } from "@collision";

import type { HostEntity, LocalState, ReconcileResult } from "./types";

/** Default host AABB extent in sub-pixels (28x28 px sprite, like the
 *  initial entity tier). Real games override per type. */
const DEFAULT_HALF_W = 14 * SUB_PER_PX;
const DEFAULT_HALF_H = 14 * SUB_PER_PX;

/** Default collision mask — "land" matches schemas/collision.md. */
const DEFAULT_MASK = 0x1;

/** Sub-pixels of drift the reconciliation tolerates before snapping
 *  the local position to the server position. 4 px at 256 sub/px = 1024.
 *  Below that, we let prediction stand and recover via natural drift. */
export const RECONCILE_HARD_SNAP_SUB = 4 * SUB_PER_PX;

/** Speed in sub-pixels per millisecond at intent magnitude 1000.
 *  6 px/tick at 10 Hz = 60 px/sec = ~1.7 tiles/sec. */
export const HOST_SPEED_SUB_PER_MS = (60 * SUB_PER_PX) / 1000;

/**
 * Step the host entity forward by dtMs given its current intent. The
 * collision world is the renderer's view of map tiles (same module the
 * server uses, so the resolved position matches). Returns a new
 * LocalState; does not mutate the input.
 */
export function predictStep(
	prev: LocalState,
	dtMs: number,
	world: World,
	halfW: number = DEFAULT_HALF_W,
	halfH: number = DEFAULT_HALF_H,
	mask: number = DEFAULT_MASK,
): LocalState {
	if (prev.hostId === 0n) {
		// Server hasn't told us our id yet -> can't predict.
		return prev;
	}
	if (prev.intentVx === 0 && prev.intentVy === 0) {
		return prev;
	}
	const dx = ((prev.intentVx * HOST_SPEED_SUB_PER_MS * dtMs) / 1000) | 0;
	const dy = ((prev.intentVy * HOST_SPEED_SUB_PER_MS * dtMs) / 1000) | 0;
	if (dx === 0 && dy === 0) return prev;

	// AABB centred on the host; collision.move mutates it in place, so
	// we build a fresh one each step.
	const aabb = {
		left:   prev.hostX - halfW,
		top:    prev.hostY - halfH,
		right:  prev.hostX + halfW,
		bottom: prev.hostY + halfH,
	};
	const ent: Entity = { aabb, mask };
	const result = move(ent, dx, dy, world);

	return {
		...prev,
		hostX: prev.hostX + result.resolvedDx,
		hostY: prev.hostY + result.resolvedDy,
	};
}

/**
 * Reconcile the local prediction against a freshly-received server
 * position for the host entity. Small drifts are tolerated (the next
 * predict step naturally pulls us back); large gaps force a hard snap.
 */
export function reconcile(
	prev: LocalState,
	server: HostEntity,
): { state: LocalState; result: ReconcileResult } {
	const deltaX = server.x - prev.hostX;
	const deltaY = server.y - prev.hostY;
	const distSq = deltaX * deltaX + deltaY * deltaY;
	const hardSnap = distSq > RECONCILE_HARD_SNAP_SUB * RECONCILE_HARD_SNAP_SUB;

	if (hardSnap) {
		return {
			state: { ...prev, hostX: server.x, hostY: server.y },
			result: { deltaX, deltaY, hardSnap: true },
		};
	}
	// Soft reconciliation: blend halfway each tick. This drains drift
	// without visible teleport even under packet jitter.
	return {
		state: {
			...prev,
			hostX: prev.hostX + (deltaX >> 1),
			hostY: prev.hostY + (deltaY >> 1),
		},
		result: { deltaX, deltaY, hardSnap: false },
	};
}

/** Initial state before the server has confirmed the host id. */
export function freshLocalState(): LocalState {
	return {
		serverTick: 0n,
		hostId: 0n,
		hostX: 0,
		hostY: 0,
		intentVx: 0,
		intentVy: 0,
	};
}

/**
 * Find the host entity in the entity cache. Multiple heuristics:
 *
 *   1. Server-assigned host id, if known (set when the first JoinMap
 *      response includes our entity).
 *   2. Otherwise, the entity matching `playerSubject` if the server
 *      embedded a player_id field on EntityState (future).
 *
 * Returns the resolved host entity or undefined if the server hasn't
 * spawned us yet.
 */
export function resolveHost(
	state: LocalState,
	getEntity: (id: bigint) => HostEntity | undefined,
): HostEntity | undefined {
	if (state.hostId !== 0n) return getEntity(state.hostId);
	return undefined;
}
