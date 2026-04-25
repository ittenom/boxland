// Boxland — net/codec.ts
//
// FlatBuffers encode/decode helpers. Wraps the raw flatbuffers builder
// API in small typed functions so callers can build envelopes without
// memorizing the slot ordering. One file per direction would be overkill
// at this verb count.

import * as flatbuffers from "flatbuffers";

import { Auth } from "@proto/auth.js";
import { ProtocolVersion } from "@proto/protocol-version.js";
import { ClientMessage } from "@proto/client-message.js";
import { JoinMapPayload } from "@proto/join-map-payload.js";
import { LeaveMapPayload } from "@proto/leave-map-payload.js";
import { MovePayload } from "@proto/move-payload.js";
import { HeartbeatPayload } from "@proto/heartbeat-payload.js";
import { AckTickPayload } from "@proto/ack-tick-payload.js";
import { SpectatePayload } from "@proto/spectate-payload.js";
import { Verb } from "@proto/verb.js";
import { Realm } from "@proto/realm.js";
import { ClientKind } from "@proto/client-kind.js";
import { Diff } from "@proto/diff.js";
import { ChunkVersion } from "@proto/chunk-version.js";
import type {
	AuthParams,
	JoinMapIntent,
	LeaveMapIntent,
	MoveIntent,
	SpectateIntent,
	AckTick,
} from "./types";

// ---- Auth handshake ----

/** Build the first frame the server reads after WS upgrade. */
export function encodeAuth(p: AuthParams): Uint8Array {
	const b = new flatbuffers.Builder(64);
	const tokenOff = b.createString(p.token);
	const versionOff = b.createString(p.clientVersion);

	ProtocolVersion.startProtocolVersion(b);
	ProtocolVersion.addMajor(b, p.protocolMajor ?? 1);
	ProtocolVersion.addMinor(b, p.protocolMinor ?? 0);
	const pvOff = ProtocolVersion.endProtocolVersion(b);

	Auth.startAuth(b);
	Auth.addProtocolVersion(b, pvOff);
	Auth.addRealm(b, p.realm);
	Auth.addToken(b, tokenOff);
	Auth.addClientKind(b, p.clientKind);
	Auth.addClientVersion(b, versionOff);
	const root = Auth.endAuth(b);
	b.finish(root);
	return b.asUint8Array();
}

// ---- ClientMessage envelope ----

/**
 * Wrap a verb-specific payload in a ClientMessage. `payload` may be empty
 * for verbs that need no data (e.g. LeaveMap with implicit map id can
 * eventually be argument-less, though today we still send the id).
 */
export function encodeClientMessage(verb: Verb, payload: Uint8Array | null): Uint8Array {
	const b = new flatbuffers.Builder(64 + (payload?.length ?? 0));
	let payloadOff: flatbuffers.Offset = 0;
	if (payload && payload.length > 0) {
		payloadOff = ClientMessage.createPayloadVector(b, payload);
	}
	ClientMessage.startClientMessage(b);
	ClientMessage.addVerb(b, verb);
	if (payloadOff) {
		ClientMessage.addPayload(b, payloadOff);
	}
	const root = ClientMessage.endClientMessage(b);
	b.finish(root);
	return b.asUint8Array();
}

// ---- Per-verb payloads ----

export function encodeJoinMap(p: JoinMapIntent): Uint8Array {
	const b = new flatbuffers.Builder(32);
	const hintOff = b.createString(p.instanceHint ?? "");
	JoinMapPayload.startJoinMapPayload(b);
	JoinMapPayload.addMapId(b, p.mapId);
	JoinMapPayload.addInstanceHint(b, hintOff);
	const root = JoinMapPayload.endJoinMapPayload(b);
	b.finish(root);
	return b.asUint8Array();
}

export function encodeLeaveMap(p: LeaveMapIntent): Uint8Array {
	const b = new flatbuffers.Builder(16);
	LeaveMapPayload.startLeaveMapPayload(b);
	LeaveMapPayload.addMapId(b, p.mapId);
	const root = LeaveMapPayload.endLeaveMapPayload(b);
	b.finish(root);
	return b.asUint8Array();
}

export function encodeMove(p: MoveIntent): Uint8Array {
	const b = new flatbuffers.Builder(16);
	MovePayload.startMovePayload(b);
	MovePayload.addVx(b, clampInt16(p.vx));
	MovePayload.addVy(b, clampInt16(p.vy));
	const root = MovePayload.endMovePayload(b);
	b.finish(root);
	return b.asUint8Array();
}

export function encodeHeartbeat(clientNowMs: bigint): Uint8Array {
	const b = new flatbuffers.Builder(16);
	HeartbeatPayload.startHeartbeatPayload(b);
	HeartbeatPayload.addClientNowMs(b, clientNowMs);
	const root = HeartbeatPayload.endHeartbeatPayload(b);
	b.finish(root);
	return b.asUint8Array();
}

export function encodeAckTick(p: AckTick): Uint8Array {
	const b = new flatbuffers.Builder(16);
	AckTickPayload.startAckTickPayload(b);
	AckTickPayload.addLastAppliedTick(b, p.lastAppliedTick);
	const root = AckTickPayload.endAckTickPayload(b);
	b.finish(root);
	return b.asUint8Array();
}

export function encodeSpectate(p: SpectateIntent): Uint8Array {
	const b = new flatbuffers.Builder(32);
	const hintOff = b.createString(p.instanceHint ?? "");
	SpectatePayload.startSpectatePayload(b);
	SpectatePayload.addMapId(b, p.mapId);
	SpectatePayload.addInstanceHint(b, hintOff);
	SpectatePayload.addMode(b, p.mode);
	if (p.targetPlayerId !== undefined) {
		SpectatePayload.addTargetPlayerId(b, p.targetPlayerId);
	}
	const root = SpectatePayload.endSpectatePayload(b);
	b.finish(root);
	return b.asUint8Array();
}

// ---- Convenience wrappers (verb + payload in one call) ----

export function envelopeJoinMap(p: JoinMapIntent): Uint8Array {
	return encodeClientMessage(Verb.JoinMap, encodeJoinMap(p));
}
export function envelopeLeaveMap(p: LeaveMapIntent): Uint8Array {
	return encodeClientMessage(Verb.LeaveMap, encodeLeaveMap(p));
}
export function envelopeMove(p: MoveIntent): Uint8Array {
	return encodeClientMessage(Verb.Move, encodeMove(p));
}
export function envelopeHeartbeat(clientNowMs: bigint): Uint8Array {
	return encodeClientMessage(Verb.Heartbeat, encodeHeartbeat(clientNowMs));
}
export function envelopeAckTick(p: AckTick): Uint8Array {
	return encodeClientMessage(Verb.AckTick, encodeAckTick(p));
}
export function envelopeSpectate(p: SpectateIntent): Uint8Array {
	return encodeClientMessage(Verb.Spectate, encodeSpectate(p));
}

// ---- Inbound decoding ----

/** Wrap a Uint8Array in a ByteBuffer the proto APIs expect. */
function asByteBuffer(blob: Uint8Array): flatbuffers.ByteBuffer {
	return new flatbuffers.ByteBuffer(blob);
}

/** Decode a server -> client Diff frame. Returns null on a too-short blob. */
export function decodeDiff(blob: Uint8Array): Diff | null {
	if (blob.length < 8) return null;
	return Diff.getRootAsDiff(asByteBuffer(blob));
}

/** Convenience: extract per-chunk versions as a typed array of pairs. */
export interface ChunkAck {
	chunkId: bigint;
	version: bigint;
}
export function diffChunkAcks(d: Diff): ChunkAck[] {
	const n = d.chunksLength();
	const out: ChunkAck[] = new Array(n);
	const tmp = new ChunkVersion();
	for (let i = 0; i < n; i++) {
		const cv = d.chunks(i, tmp);
		if (cv) out[i] = { chunkId: cv.chunkId(), version: cv.version() };
	}
	return out;
}

// ---- Helpers ----

function clampInt16(n: number): number {
	if (n > 0x7fff) return 0x7fff;
	if (n < -0x8000) return -0x8000;
	return Math.trunc(n);
}
