// Boxland — game/types.ts
//
// Surface for the player game module (PLAN.md §6h). The types here are
// the public contract between page boot, client orchestrator,
// prediction, and tests.

import type { CachedEntity } from "@net";

/** Inputs the page entry pulls off `#bx-game-host` data-attributes. */
export interface GameBootConfig {
	/** Map id the player is joining. */
	mapId: number;
	mapName: string;
	mapWidth: number;
	mapHeight: number;
	/** Absolute ws://... or wss://... URL for the gateway. */
	wsURL: string;
	/** Short-lived player JWT to present in the Auth handshake. */
	accessToken: string;
	/** Endpoint that mints a fresh JWT on reconnect. POST + cookies. */
	ticketURL?: string; // default "/play/ws-ticket"
}

/**
 * LocalState is the per-tick output of the prediction layer. The host
 * entity is the player we're controlling; everyone else flows straight
 * from the server cache.
 */
export interface LocalState {
	/** server-authoritative tick id of the most recently applied diff */
	serverTick: bigint;
	/** Player entity id once the server tells us who we are. 0 = unknown. */
	hostId: bigint;
	/** Predicted position of the host entity, in world sub-pixels. */
	hostX: number;
	hostY: number;
	/** Current intent vector (-1000..1000), set by command-bus listeners. */
	intentVx: number;
	intentVy: number;
}

/**
 * ReconcileResult describes what happened when prediction met server
 * truth. Surfaced for telemetry + tests; the loop applies it to LocalState.
 */
export interface ReconcileResult {
	/** Sub-pixel delta the server snapped us by on each axis. */
	deltaX: number;
	deltaY: number;
	/** True if the snap exceeded the rubber-band threshold and we
	 *  forced the local position to the server one. */
	hardSnap: boolean;
}

/** Convenient narrowed shape for the entity the host controls. */
export type HostEntity = CachedEntity;
