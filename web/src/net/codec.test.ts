import { describe, it, expect } from "vitest";
import * as flatbuffers from "flatbuffers";

import {
	encodeAuth,
	encodeClientMessage,
	encodeJoinMap,
	encodeLeaveMap,
	encodeMove,
	encodeHeartbeat,
	encodeAckTick,
	encodeSpectate,
	envelopeJoinMap,
	envelopeMove,
	envelopeSpectate,
	decodeDiff,
	diffChunkAcks,
} from "./codec";
import { Realm, ClientKind, Verb, SpectateMode } from "./types";

import { Auth } from "@proto/auth.js";
import { ClientMessage } from "@proto/client-message.js";
import { JoinMapPayload } from "@proto/join-map-payload.js";
import { MovePayload } from "@proto/move-payload.js";
import { SpectatePayload } from "@proto/spectate-payload.js";
import { HeartbeatPayload } from "@proto/heartbeat-payload.js";
import { AckTickPayload } from "@proto/ack-tick-payload.js";
import { LeaveMapPayload } from "@proto/leave-map-payload.js";
import { Diff } from "@proto/diff.js";
import { ChunkVersion } from "@proto/chunk-version.js";
import { ProtocolVersion } from "@proto/protocol-version.js";
import { EntityState } from "@proto/entity-state.js";

function bb(blob: Uint8Array): flatbuffers.ByteBuffer {
	return new flatbuffers.ByteBuffer(blob);
}

describe("codec encodeAuth", () => {
	it("roundtrips realm + token + client metadata", () => {
		const blob = encodeAuth({
			realm: Realm.Player,
			token: "tok-123",
			clientKind: ClientKind.Web,
			clientVersion: "0.1.0",
		});
		const a = Auth.getRootAsAuth(bb(blob));
		expect(a.realm()).toBe(Realm.Player);
		expect(a.token()).toBe("tok-123");
		expect(a.clientKind()).toBe(ClientKind.Web);
		expect(a.clientVersion()).toBe("0.1.0");
		const pv = a.protocolVersion();
		expect(pv?.major()).toBe(1);
		expect(pv?.minor()).toBe(0);
	});

	it("respects explicit protocol version", () => {
		const blob = encodeAuth({
			realm: Realm.Designer,
			token: "tk",
			clientKind: ClientKind.Web,
			clientVersion: "x",
			protocolMajor: 2,
			protocolMinor: 5,
		});
		const a = Auth.getRootAsAuth(bb(blob));
		expect(a.realm()).toBe(Realm.Designer);
		expect(a.protocolVersion()?.major()).toBe(2);
		expect(a.protocolVersion()?.minor()).toBe(5);
	});
});

describe("codec ClientMessage envelope", () => {
	it("wraps verb + payload bytes intact", () => {
		const inner = encodeMove({ vx: 100, vy: -200 });
		const env = encodeClientMessage(Verb.Move, inner);
		const cm = ClientMessage.getRootAsClientMessage(bb(env));
		expect(cm.verb()).toBe(Verb.Move);
		expect(cm.payloadLength()).toBe(inner.length);
		const out = cm.payloadArray();
		expect(out).not.toBeNull();
		expect(Array.from(out!)).toEqual(Array.from(inner));
	});

	it("supports verbs with no payload", () => {
		const env = encodeClientMessage(Verb.LeaveMap, null);
		const cm = ClientMessage.getRootAsClientMessage(bb(env));
		expect(cm.verb()).toBe(Verb.LeaveMap);
		expect(cm.payloadLength()).toBe(0);
	});
});

describe("codec per-verb payloads", () => {
	it("encodeJoinMap roundtrips map_id + instance_hint", () => {
		const blob = encodeJoinMap({ mapId: 42, instanceHint: "live:42:0" });
		const p = JoinMapPayload.getRootAsJoinMapPayload(bb(blob));
		expect(p.mapId()).toBe(42);
		expect(p.instanceHint()).toBe("live:42:0");
	});

	it("encodeJoinMap defaults instance_hint to empty string", () => {
		const blob = encodeJoinMap({ mapId: 7 });
		const p = JoinMapPayload.getRootAsJoinMapPayload(bb(blob));
		expect(p.mapId()).toBe(7);
		expect(p.instanceHint()).toBe("");
	});

	it("encodeLeaveMap roundtrips", () => {
		const blob = encodeLeaveMap({ mapId: 3 });
		const p = LeaveMapPayload.getRootAsLeaveMapPayload(bb(blob));
		expect(p.mapId()).toBe(3);
	});

	it("encodeMove clamps to int16 range", () => {
		const blob = encodeMove({ vx: 999_999, vy: -999_999 });
		const p = MovePayload.getRootAsMovePayload(bb(blob));
		expect(p.vx()).toBe(0x7fff);
		expect(p.vy()).toBe(-0x8000);
	});

	it("encodeMove truncates fractional inputs", () => {
		const blob = encodeMove({ vx: 12.7, vy: -3.9 });
		const p = MovePayload.getRootAsMovePayload(bb(blob));
		expect(p.vx()).toBe(12);
		expect(p.vy()).toBe(-3);
	});

	it("encodeHeartbeat preserves bigint timestamp", () => {
		const blob = encodeHeartbeat(123_456_789_012n);
		const p = HeartbeatPayload.getRootAsHeartbeatPayload(bb(blob));
		expect(p.clientNowMs()).toBe(123_456_789_012n);
	});

	it("encodeAckTick preserves bigint tick", () => {
		const blob = encodeAckTick({ lastAppliedTick: 9_999_999n });
		const p = AckTickPayload.getRootAsAckTickPayload(bb(blob));
		expect(p.lastAppliedTick()).toBe(9_999_999n);
	});

	it("encodeSpectate roundtrips map_id + mode + target", () => {
		const blob = encodeSpectate({
			mapId: 5,
			mode: SpectateMode.FollowPlayer,
			instanceHint: "live:5:0",
			targetPlayerId: 1234n,
		});
		const p = SpectatePayload.getRootAsSpectatePayload(bb(blob));
		expect(p.mapId()).toBe(5);
		expect(p.mode()).toBe(SpectateMode.FollowPlayer);
		expect(p.instanceHint()).toBe("live:5:0");
		expect(p.targetPlayerId()).toBe(1234n);
	});

	it("encodeSpectate defaults targetPlayerId to 0 when omitted", () => {
		const blob = encodeSpectate({ mapId: 5, mode: SpectateMode.FreeCam });
		const p = SpectatePayload.getRootAsSpectatePayload(bb(blob));
		expect(p.targetPlayerId()).toBe(0n);
	});
});

describe("envelope helpers chain payload + envelope", () => {
	it("envelopeMove wraps Move", () => {
		const blob = envelopeMove({ vx: 1, vy: 2 });
		const cm = ClientMessage.getRootAsClientMessage(bb(blob));
		expect(cm.verb()).toBe(Verb.Move);
		const inner = MovePayload.getRootAsMovePayload(bb(cm.payloadArray()!));
		expect(inner.vx()).toBe(1);
		expect(inner.vy()).toBe(2);
	});

	it("envelopeJoinMap wraps JoinMap", () => {
		const blob = envelopeJoinMap({ mapId: 11 });
		const cm = ClientMessage.getRootAsClientMessage(bb(blob));
		expect(cm.verb()).toBe(Verb.JoinMap);
		const inner = JoinMapPayload.getRootAsJoinMapPayload(bb(cm.payloadArray()!));
		expect(inner.mapId()).toBe(11);
	});

	it("envelopeSpectate wraps Spectate", () => {
		const blob = envelopeSpectate({ mapId: 8, mode: SpectateMode.FreeCam });
		const cm = ClientMessage.getRootAsClientMessage(bb(blob));
		expect(cm.verb()).toBe(Verb.Spectate);
		const inner = SpectatePayload.getRootAsSpectatePayload(bb(cm.payloadArray()!));
		expect(inner.mapId()).toBe(8);
		expect(inner.mode()).toBe(SpectateMode.FreeCam);
	});
});

// ---- Diff decoding ----

/** Test helper: build a small Diff with the given chunks + an entity. */
function buildTestDiff(opts: {
	mapId: number;
	tick: bigint;
	chunks: Array<{ chunkId: bigint; version: bigint }>;
	entities?: Array<{ id: bigint; x: number; y: number }>;
}): Uint8Array {
	const b = new flatbuffers.Builder(256);

	ProtocolVersion.startProtocolVersion(b);
	ProtocolVersion.addMajor(b, 1);
	ProtocolVersion.addMinor(b, 0);
	const pvOff = ProtocolVersion.endProtocolVersion(b);

	const entityOffs: number[] = [];
	for (const e of opts.entities ?? []) {
		const nameOff = b.createString("");
		EntityState.startEntityState(b);
		EntityState.addId(b, e.id);
		EntityState.addX(b, e.x);
		EntityState.addY(b, e.y);
		EntityState.addNameplate(b, nameOff);
		entityOffs.push(EntityState.endEntityState(b));
	}
	const addedVec = entityOffs.length > 0
		? Diff.createAddedVector(b, entityOffs)
		: 0;

	const chunkOffs: number[] = [];
	for (const c of opts.chunks) {
		ChunkVersion.startChunkVersion(b);
		ChunkVersion.addChunkId(b, c.chunkId);
		ChunkVersion.addVersion(b, c.version);
		chunkOffs.push(ChunkVersion.endChunkVersion(b));
	}
	const chunksVec = chunkOffs.length > 0
		? Diff.createChunksVector(b, chunkOffs)
		: 0;

	Diff.startDiff(b);
	Diff.addProtocolVersion(b, pvOff);
	Diff.addMapId(b, opts.mapId);
	Diff.addTick(b, opts.tick);
	if (addedVec) Diff.addAdded(b, addedVec);
	if (chunksVec) Diff.addChunks(b, chunksVec);
	const root = Diff.endDiff(b);
	b.finish(root);
	return b.asUint8Array();
}

describe("decodeDiff", () => {
	it("returns null for short blobs", () => {
		expect(decodeDiff(new Uint8Array([1, 2, 3]))).toBeNull();
	});

	it("roundtrips map_id + tick + chunks", () => {
		const blob = buildTestDiff({
			mapId: 7,
			tick: 42n,
			chunks: [
				{ chunkId: 100n, version: 5n },
				{ chunkId: 101n, version: 9n },
			],
		});
		const d = decodeDiff(blob);
		expect(d).not.toBeNull();
		expect(d!.mapId()).toBe(7);
		expect(d!.tick()).toBe(42n);
		expect(d!.chunksLength()).toBe(2);
	});

	it("diffChunkAcks extracts (chunkId, version) pairs", () => {
		const blob = buildTestDiff({
			mapId: 1,
			tick: 1n,
			chunks: [
				{ chunkId: 5n, version: 1n },
				{ chunkId: 9n, version: 4n },
				{ chunkId: 17n, version: 2n },
			],
		});
		const d = decodeDiff(blob)!;
		const acks = diffChunkAcks(d);
		expect(acks).toHaveLength(3);
		expect(acks).toContainEqual({ chunkId: 5n, version: 1n });
		expect(acks).toContainEqual({ chunkId: 9n, version: 4n });
		expect(acks).toContainEqual({ chunkId: 17n, version: 2n });
	});
});

// Re-exported for the mailbox tests so we don't duplicate the builder.
export { buildTestDiff };
