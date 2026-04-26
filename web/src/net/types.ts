// Boxland — net/types.ts
//
// Public types for the WS client + AOI mailbox. Kept minimal and free of
// FlatBuffers internals so callers (game client, mapmaker, sandbox) can
// import these without pulling the proto module graph.

import { Realm } from "@proto/realm.js";
import { ClientKind } from "@proto/client-kind.js";
import { Verb } from "@proto/verb.js";
import { SpectateMode } from "@proto/spectate-mode.js";

// Re-export the few proto enums callers actually need at the call site.
// Saves them the awkward `@proto/...` import path for one-off uses.
export { Realm, ClientKind, Verb, SpectateMode };

/** Connection lifecycle. Matches the standard WebSocket-ish progression. */
export type ConnState =
	| "idle"            // never opened, or closed and not retrying
	| "connecting"      // socket opened, awaiting Auth ack via first inbound
	| "authenticating"  // Auth sent, waiting for first server frame
	| "open"            // first frame received -> live
	| "closed"          // closed; backoff timer may schedule a reconnect
	| "fatal";          // stopped due to non-recoverable error (e.g. bad token)

/** Outbound JoinMap intent. Mirrors JoinMapPayload. */
export interface JoinMapIntent {
	mapId: number;
	instanceHint?: string;
}

/** Outbound LeaveMap intent. Mirrors LeaveMapPayload. */
export interface LeaveMapIntent {
	mapId: number;
}

/** Outbound Move intent. Mirrors MovePayload. */
export interface MoveIntent {
	/** Normalized -1000..1000; server clamps + applies its own speed. */
	vx: number;
	vy: number;
}

/** Outbound Spectate intent. Mirrors SpectatePayload. */
export interface SpectateIntent {
	mapId: number;
	mode: SpectateMode;
	instanceHint?: string;
	targetPlayerId?: bigint;
}

/** Outbound AckTick. Mirrors AckTickPayload. */
export interface AckTick {
	lastAppliedTick: bigint;
}

/** Auth handshake parameters supplied by the host application. */
export interface AuthParams {
	realm: Realm;
	/** JWT (player) or one-shot WS ticket (designer). */
	token: string;
	clientKind: ClientKind;
	clientVersion: string;
	protocolMajor?: number; // default 1
	protocolMinor?: number; // default 0
}

/** Backoff configuration for the auto-reconnect loop. */
export interface BackoffConfig {
	/** Base delay in ms. Default 250. */
	baseMs?: number;
	/** Multiplicative factor per attempt. Default 2. */
	factor?: number;
	/** Hard cap on backoff in ms. Default 30_000. */
	maxMs?: number;
	/** Jitter ratio [0..1]. Default 0.25 (±25% of computed delay). */
	jitter?: number;
	/** Hard cap on consecutive attempts before going fatal. Default 0 = unlimited. */
	maxAttempts?: number;
}

/** Payload an AOI mailbox emits to listeners after applying a Diff. */
export interface AppliedDiff {
	mapId: number;
	tick: bigint;
	addedIds: bigint[];
	removedIds: bigint[];
	movedIds: bigint[];
	tileChangeCount: number;
	lightingChangeCount: number;
	audioCount: number;
	/** HUD binding deltas applied this tick (per-binding listeners are
	 *  fired separately via Mailbox.onHud). */
	hudDataCount: number;
	/** Chunk ids whose acked version was advanced by this diff. */
	advancedChunks: bigint[];
}

/** Listener callback signatures. */
export type StateListener = (s: ConnState, prev: ConnState) => void;
export type DiffListener = (d: AppliedDiff) => void;
export type ErrorListener = (err: Error) => void;
