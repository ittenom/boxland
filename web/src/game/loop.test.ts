import { describe, it, expect } from "vitest";
import * as flatbuffers from "flatbuffers";

import {
	Mailbox,
	NetClient,
	Realm,
	ClientKind,
	type AuthParams,
	type Scheduler,
	type WSLike,
} from "@net";
import { Diff } from "@proto/diff.js";
import { ProtocolVersion } from "@proto/protocol-version.js";
import { EntityState } from "@proto/entity-state.js";
import { ChunkVersion } from "@proto/chunk-version.js";
import { ClientMessage } from "@proto/client-message.js";
import { Verb } from "@proto/verb.js";

import { GameLoop, type LoopScheduler, type RendererLike } from "./loop";
import type { GameBootConfig } from "./types";
import { SUB_PER_PX } from "@collision";

// ---- Minimal fakes ----

class FakeWS implements WSLike {
	binaryType = "blob";
	readyState = 0;
	onopen: ((ev: Event) => void) | null = null;
	onmessage: ((ev: { data: ArrayBuffer | Uint8Array | Blob | string }) => void) | null = null;
	onerror: ((ev: Event) => void) | null = null;
	onclose: ((ev: { code: number; reason: string; wasClean?: boolean }) => void) | null = null;
	sent: ArrayBuffer[] = [];
	send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
		if (data instanceof ArrayBuffer) this.sent.push(data);
		else if (ArrayBuffer.isView(data)) {
			const v = data as ArrayBufferView;
			const u8 = new Uint8Array(v.buffer, v.byteOffset, v.byteLength);
			const ab = new ArrayBuffer(u8.byteLength);
			new Uint8Array(ab).set(u8);
			this.sent.push(ab);
		}
	}
	close(): void { this.onclose?.({ code: 1000, reason: "" }); }
	open(): void { this.readyState = 1; this.onopen?.(new Event("open")); }
	deliver(blob: Uint8Array): void {
		const ab = blob.buffer.slice(blob.byteOffset, blob.byteOffset + blob.byteLength) as ArrayBuffer;
		this.onmessage?.({ data: ab });
	}
}

class TickClock implements Scheduler, LoopScheduler {
	private clock = 0;
	private nextHandle = 1;
	timeouts: Array<{ id: number; cb: () => void; due: number }> = [];
	frameQueue: Array<{ id: number; cb: (now: number) => void }> = [];

	now(): number { return this.clock; }
	random(): number { return 0.5; }

	setTimeout(cb: () => void, ms: number): unknown {
		const t = { id: this.nextHandle++, cb, due: this.clock + ms };
		this.timeouts.push(t);
		return t;
	}
	clearTimeout(h: unknown): void { this.timeouts = this.timeouts.filter((x) => x !== h); }

	requestFrame(cb: (now: number) => void): unknown {
		const f = { id: this.nextHandle++, cb };
		this.frameQueue.push(f);
		return f;
	}
	cancelFrame(h: unknown): void { this.frameQueue = this.frameQueue.filter((x) => x !== h); }

	advanceTo(ms: number): void { this.clock = ms; }

	/** Run the next queued frame at this.now(). */
	runFrame(): void {
		const f = this.frameQueue.shift();
		if (f) f.cb(this.clock);
	}
}

class StubRenderer implements RendererLike {
	frames: Array<{ entities: number; hostId: bigint; hostX: number; hostY: number }> = [];
	updateFrame(args: { entities: import("@net").CachedEntity[]; hostId: bigint; hostX: number; hostY: number }): void {
		this.frames.push({
			entities: args.entities.length, hostId: args.hostId, hostX: args.hostX, hostY: args.hostY,
		});
	}
}

// ---- Diff builder (mirrors mailbox.test) ----

function buildDiff(opts: {
	tick: bigint;
	added?: Array<{ id: bigint; x: number; y: number }>;
	moved?: Array<{ id: bigint; x: number; y: number }>;
	chunks?: Array<{ chunkId: bigint; version: bigint }>;
}): Uint8Array {
	const b = new flatbuffers.Builder(256);
	ProtocolVersion.startProtocolVersion(b);
	ProtocolVersion.addMajor(b, 1);
	ProtocolVersion.addMinor(b, 0);
	const pv = ProtocolVersion.endProtocolVersion(b);

	const ent = (e: { id: bigint; x: number; y: number }): number => {
		const n = b.createString("");
		EntityState.startEntityState(b);
		EntityState.addId(b, e.id);
		EntityState.addX(b, e.x);
		EntityState.addY(b, e.y);
		EntityState.addNameplate(b, n);
		return EntityState.endEntityState(b);
	};
	const addOffs = (opts.added ?? []).map(ent);
	const movOffs = (opts.moved ?? []).map(ent);
	const chOffs = (opts.chunks ?? []).map((c) => {
		ChunkVersion.startChunkVersion(b);
		ChunkVersion.addChunkId(b, c.chunkId);
		ChunkVersion.addVersion(b, c.version);
		return ChunkVersion.endChunkVersion(b);
	});

	const addV = addOffs.length ? Diff.createAddedVector(b, addOffs) : 0;
	const movV = movOffs.length ? Diff.createMovedVector(b, movOffs) : 0;
	const chV  = chOffs.length  ? Diff.createChunksVector(b, chOffs) : 0;

	Diff.startDiff(b);
	Diff.addProtocolVersion(b, pv);
	Diff.addTick(b, opts.tick);
	if (addV) Diff.addAdded(b, addV);
	if (movV) Diff.addMoved(b, movV);
	if (chV)  Diff.addChunks(b, chV);
	const root = Diff.endDiff(b);
	b.finish(root);
	return b.asUint8Array();
}

// ---- Helpers ----

function makeLoop(): { loop: GameLoop; ws: { current: FakeWS | null }; clock: TickClock; renderer: StubRenderer; net: NetClient } {
	const clock = new TickClock();
	const wsRef = { current: null as FakeWS | null };
	const renderer = new StubRenderer();
	const config: GameBootConfig = {
		mapId: 1, mapName: "test", mapWidth: 64, mapHeight: 64,
		wsURL: "ws://localhost/ws", accessToken: "tok",
	};
	const auth = (): AuthParams => ({
		realm: Realm.Player, token: "tok", clientKind: ClientKind.Web, clientVersion: "test",
	});
	const mailbox = new Mailbox();
	const net = new NetClient(config.wsURL, {
		auth,
		wsFactory: (u) => { const ws = new FakeWS(); ws.binaryType = "arraybuffer"; wsRef.current = ws; return ws; },
		scheduler: clock,
		mailbox,
		backoff: { baseMs: 100, factor: 2, maxMs: 1000, jitter: 0, maxAttempts: 0 },
	});
	const loop = new GameLoop({ config, renderer, mailbox, netClient: net, scheduler: clock });
	return { loop, ws: wsRef, clock, renderer, net };
}

function decodeClientMsg(ab: ArrayBuffer): { verb: Verb; payload: Uint8Array | null } {
	const u8 = new Uint8Array(ab);
	const cm = ClientMessage.getRootAsClientMessage(new flatbuffers.ByteBuffer(u8));
	return { verb: cm.verb(), payload: cm.payloadArray() };
}

// ---- Tests ----

describe("GameLoop wiring", () => {
	it("on net open, sends a JoinMap envelope", async () => {
		const { loop, ws } = makeLoop();
		loop.start();
		ws.current!.open();
		await Promise.resolve(); await Promise.resolve();

		// First frame after Auth blob should be JoinMap (Auth + JoinMap = 2 sends so far).
		const msgs = ws.current!.sent.map(decodeClientMsg);
		// First send is Auth (raw, not ClientMessage). Filter out anything
		// whose verb decodes nonsensically.
		const verbs = msgs.map((m) => m.verb);
		expect(verbs).toContain(Verb.JoinMap);

		loop.stop();
	});

	it("HUD receives state transitions", async () => {
		const states: string[] = [];
		const { loop, ws } = makeLoop();
		// Patch HUD onto a fresh loop -- easiest by reassigning hud after construction.
		(loop as unknown as { hud: { setState: (s: string) => void; setTick: () => void } }).hud =
			{ setState: (s) => states.push(s), setTick: () => undefined };
		loop.start();
		ws.current!.open();
		await Promise.resolve(); await Promise.resolve();
		expect(states).toContain("connecting");
		expect(states).toContain("authenticating");
		expect(states).toContain("open");
		loop.stop();
	});
});

describe("GameLoop frame tick", () => {
	it("renders entities from the mailbox", () => {
		const { loop, renderer, net } = makeLoop();
		// Inject an entity directly through the mailbox so we don't need
		// to drive the WS handshake.
		loop.mailbox.applyDiff(Diff.getRootAsDiff(new flatbuffers.ByteBuffer(buildDiff({
			tick: 1n, added: [{ id: 7n, x: 16 * SUB_PER_PX, y: 16 * SUB_PER_PX }],
		}))));
		// Force a tick at t=16.
		loop.tick(16);
		expect(renderer.frames).toHaveLength(1);
		expect(renderer.frames[0]?.entities).toBe(1);
		// Loop hasn't connected, so net.sendMove() returns false; no MovePayload.
		expect(net.getState()).toBe("idle");
	});

	it("predicted host position drives the rendered hostX/hostY", async () => {
		const { loop, renderer, ws, clock } = makeLoop();
		loop.start();
		ws.current!.open();
		await Promise.resolve(); await Promise.resolve();
		// Hand the loop an "added" diff containing the host entity.
		ws.current!.deliver(buildDiff({
			tick: 1n, added: [{ id: 42n, x: 100 * SUB_PER_PX, y: 100 * SUB_PER_PX }],
			chunks: [{ chunkId: 0n, version: 1n }],
		}));
		// First frame after the diff: prediction sees host, no intent ->
		// reports server position.
		clock.advanceTo(16);
		loop.tick(16);
		const f1 = renderer.frames[renderer.frames.length - 1]!;
		expect(f1.hostId).toBe(42n);
		expect(f1.hostX).toBe(100 * SUB_PER_PX);

		// Press right and tick again 100ms later -> hostX should advance.
		loop.intent.setRight(true);
		clock.advanceTo(116);
		loop.tick(116);
		const f2 = renderer.frames[renderer.frames.length - 1]!;
		expect(f2.hostX).toBeGreaterThan(f1.hostX);

		loop.stop();
	});

	it("emits a Move envelope (rate-limited) when intent is non-zero", async () => {
		const { loop, ws, clock } = makeLoop();
		loop.start();
		ws.current!.open();
		await Promise.resolve(); await Promise.resolve();
		const start = ws.current!.sent.length;
		loop.intent.setRight(true);
		clock.advanceTo(16);
		loop.tick(16);
		const after = ws.current!.sent.slice(start).map(decodeClientMsg);
		// Intent changed => at least one Move went out.
		expect(after.some((m) => m.verb === Verb.Move)).toBe(true);
		loop.stop();
	});

	it("acks tick on every Diff", async () => {
		const { loop, ws } = makeLoop();
		loop.start();
		ws.current!.open();
		await Promise.resolve(); await Promise.resolve();
		const start = ws.current!.sent.length;
		ws.current!.deliver(buildDiff({ tick: 5n, added: [{ id: 1n, x: 0, y: 0 }] }));
		// Mailbox.onDiff is sync; ack should already have been sent.
		const after = ws.current!.sent.slice(start).map(decodeClientMsg);
		expect(after.some((m) => m.verb === Verb.AckTick)).toBe(true);
		loop.stop();
	});
});
