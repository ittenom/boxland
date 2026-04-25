import { describe, it, expect, beforeEach } from "vitest";

import { NetClient, type WSLike, type Scheduler } from "./client";
import type { ConnState } from "./types";
import { Realm, ClientKind } from "./types";

// ---- Fake transport ----

class FakeWS implements WSLike {
	binaryType = "blob";
	readyState = 0; // 0=CONNECTING, 1=OPEN, 2=CLOSING, 3=CLOSED
	onopen: ((ev: Event) => void) | null = null;
	onmessage: ((ev: { data: ArrayBuffer | Uint8Array | Blob | string }) => void) | null = null;
	onerror: ((ev: Event) => void) | null = null;
	onclose: ((ev: { code: number; reason: string; wasClean?: boolean }) => void) | null = null;
	sent: ArrayBuffer[] = [];

	url: string;
	constructor(url: string) { this.url = url; }

	send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
		if (data instanceof ArrayBuffer) this.sent.push(data);
		else if (ArrayBuffer.isView(data)) {
			const view = data as ArrayBufferView;
			const u8 = new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
			const copy = new ArrayBuffer(u8.byteLength);
			new Uint8Array(copy).set(u8);
			this.sent.push(copy);
		}
	}
	close(code = 1000, reason = ""): void {
		this.readyState = 3;
		this.onclose?.({ code, reason, wasClean: true });
	}
	// Test-driver helpers.
	simulateOpen(): void {
		this.readyState = 1;
		this.onopen?.(new Event("open"));
	}
	simulateClose(code = 1006, reason = "transport"): void {
		this.readyState = 3;
		this.onclose?.({ code, reason });
	}
}

// ---- Fake scheduler ----

interface FakeSchedulerHandle { id: number; cb: () => void; due: number; }

class FakeScheduler implements Scheduler {
	private nextId = 1;
	private clock = 0;
	private rng = 0.5; // deterministic; .5 means jitter -> 0
	pending: FakeSchedulerHandle[] = [];

	setTimeout(cb: () => void, ms: number): unknown {
		const h: FakeSchedulerHandle = { id: this.nextId++, cb, due: this.clock + ms };
		this.pending.push(h);
		return h;
	}
	clearTimeout(h: unknown): void {
		this.pending = this.pending.filter((p) => p !== h);
	}
	now(): number { return this.clock; }
	random(): number { return this.rng; }
	setRandom(v: number): void { this.rng = v; }

	advance(ms: number): void {
		this.clock += ms;
		const due = this.pending.filter((p) => p.due <= this.clock).sort((a, b) => a.due - b.due);
		this.pending = this.pending.filter((p) => p.due > this.clock);
		for (const h of due) h.cb();
	}
	flushDue(): void {
		const due = this.pending.filter((p) => p.due <= this.clock).sort((a, b) => a.due - b.due);
		this.pending = this.pending.filter((p) => p.due > this.clock);
		for (const h of due) h.cb();
	}
}

// ---- Helpers ----

function makeClient(opts?: {
	authFails?: boolean;
	authParams?: () => Promise<{ realm: Realm; token: string; clientKind: ClientKind; clientVersion: string }>;
	maxAttempts?: number;
	wsRef?: { current: FakeWS | null };
}): { client: NetClient; sched: FakeScheduler; wsRef: { current: FakeWS | null } } {
	const sched = new FakeScheduler();
	const wsRef = opts?.wsRef ?? { current: null as FakeWS | null };
	const wsFactory = (url: string): WSLike => {
		const ws = new FakeWS(url);
		wsRef.current = ws;
		return ws;
	};
	const baseAuth = () => ({
		realm: Realm.Player,
		token: "tok",
		clientKind: ClientKind.Web,
		clientVersion: "test",
	});
	const auth = opts?.authParams
		?? (opts?.authFails
			? () => Promise.reject(new Error("bad token"))
			: () => Promise.resolve(baseAuth()));
	const client = new NetClient("ws://example/ws", {
		auth,
		wsFactory,
		scheduler: sched,
		backoff: { baseMs: 100, factor: 2, maxMs: 5000, jitter: 0, maxAttempts: opts?.maxAttempts ?? 0 },
	});
	return { client, sched, wsRef };
}

// ---- State machine ----

describe("NetClient state machine", () => {
	it("idle -> connecting -> authenticating -> open", async () => {
		const { client, wsRef } = makeClient();
		const states: ConnState[] = [];
		client.onState((s) => states.push(s));
		client.connect();
		expect(client.getState()).toBe("connecting");
		// Simulate the WS opening.
		wsRef.current!.simulateOpen();
		// handleOpen is async; wait a microtask.
		await Promise.resolve();
		await Promise.resolve();
		expect(client.getState()).toBe("open");
		// Auth blob was sent.
		expect(wsRef.current!.sent).toHaveLength(1);
		expect(states).toContain("connecting");
		expect(states).toContain("authenticating");
		expect(states).toContain("open");
	});

	it("connect() is idempotent while connecting", () => {
		const { client, wsRef } = makeClient();
		client.connect();
		const first = wsRef.current;
		client.connect();
		expect(wsRef.current).toBe(first);
	});

	it("disconnect closes + stops reconnect", async () => {
		const { client, sched, wsRef } = makeClient();
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve();
		await Promise.resolve();
		client.disconnect();
		expect(client.getState()).toBe("idle");
		// No pending retry.
		expect(sched.pending).toHaveLength(0);
	});
});

// ---- Backoff ----

describe("NetClient backoff", () => {
	it("nextBackoff grows exponentially up to max", () => {
		const { client } = makeClient();
		expect(client.nextBackoff(0)).toBe(100);
		expect(client.nextBackoff(1)).toBe(200);
		expect(client.nextBackoff(2)).toBe(400);
		expect(client.nextBackoff(3)).toBe(800);
		// Should saturate at maxMs=5000.
		expect(client.nextBackoff(20)).toBe(5000);
	});

	it("applies symmetric jitter", () => {
		const sched = new FakeScheduler();
		const wsFactory = () => new FakeWS("ws://x");
		const c = new NetClient("ws://x", {
			auth: () => ({ realm: Realm.Player, token: "t", clientKind: ClientKind.Web, clientVersion: "" }),
			wsFactory,
			scheduler: sched,
			backoff: { baseMs: 1000, factor: 1, maxMs: 1000, jitter: 0.5, maxAttempts: 0 },
		});
		sched.setRandom(0); // ratio*2 - 1 = -1 -> -50% delta = 500
		expect(c.nextBackoff(0)).toBe(500);
		sched.setRandom(1); // +50%
		expect(c.nextBackoff(0)).toBe(1500);
		sched.setRandom(0.5); // 0%
		expect(c.nextBackoff(0)).toBe(1000);
	});

	it("retries after a transport drop with the scheduled delay", async () => {
		const { client, sched, wsRef } = makeClient();
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();
		expect(client.getState()).toBe("open");

		const firstWS = wsRef.current!;
		firstWS.simulateClose(1006);
		expect(client.getState()).toBe("closed");
		expect(sched.pending).toHaveLength(1);
		// Attempt 1 -> 100ms.
		expect(sched.pending[0]!.due).toBe(100);

		sched.advance(100);
		// Reconnect kicked a fresh socket.
		expect(wsRef.current).not.toBe(firstWS);
		expect(client.getState()).toBe("connecting");
	});

	it("attempts grow on repeated failures + reset on successful open", async () => {
		const { client, sched, wsRef } = makeClient();
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();
		expect(client.getAttempt()).toBe(0);

		// Fail 3 times in a row.
		for (let i = 0; i < 3; i++) {
			wsRef.current!.simulateClose(1006);
			expect(client.getAttempt()).toBe(i + 1);
			sched.advance(60_000); // generously past the backoff
		}
		// After the 3rd advance the 4th socket is open-pending.
		expect(client.getState()).toBe("connecting");
		// Now a successful open resets the counter.
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();
		expect(client.getAttempt()).toBe(0);
	});

	it("maxAttempts trips fatal", () => {
		const { client, sched, wsRef } = makeClient({ maxAttempts: 2 });
		client.connect();
		wsRef.current!.simulateClose(1006); // attempt 1 scheduled
		sched.advance(1000);
		wsRef.current!.simulateClose(1006); // attempt 2 scheduled
		sched.advance(1000);
		// Third failure should land in fatal.
		wsRef.current!.simulateClose(1006);
		expect(client.getState()).toBe("fatal");
	});
});

// ---- Fatal close codes ----

describe("NetClient fatal close codes", () => {
	it("close code 4xxx goes fatal + does not reconnect", async () => {
		const { client, sched, wsRef } = makeClient();
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();

		wsRef.current!.simulateClose(4001, "bad token");
		expect(client.getState()).toBe("fatal");
		expect(sched.pending).toHaveLength(0);
	});

	it("auth params throwing -> fatal, no retry", async () => {
		const { client, sched, wsRef } = makeClient({ authFails: true });
		const errs: Error[] = [];
		client.onError((e) => errs.push(e));
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();
		// auth() rejected -> fatal close 4000.
		expect(client.getState()).toBe("fatal");
		expect(sched.pending).toHaveLength(0);
		expect(errs.some((e) => /auth params/.test(e.message))).toBe(true);
	});
});

// ---- Send paths ----

describe("NetClient send paths", () => {
	it("send* before open returns false + drops the blob", () => {
		const { client } = makeClient();
		expect(client.sendMove({ vx: 1, vy: 0 })).toBe(false);
	});

	it("send* after open writes to the socket", async () => {
		const { client, wsRef } = makeClient();
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();
		const before = wsRef.current!.sent.length;
		expect(client.sendMove({ vx: 100, vy: 100 })).toBe(true);
		expect(wsRef.current!.sent.length).toBe(before + 1);
	});

	it("sendJoinMap + sendSpectate succeed once open", async () => {
		const { client, wsRef } = makeClient();
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();
		const start = wsRef.current!.sent.length;
		client.sendJoinMap({ mapId: 1 });
		client.sendSpectate({ mapId: 1, mode: 0 });
		expect(wsRef.current!.sent.length).toBe(start + 2);
	});
});

// ---- Listener cleanup ----

describe("NetClient listener cleanup", () => {
	let states: ConnState[];
	beforeEach(() => { states = []; });

	it("returned unsubscribe stops further events", () => {
		const { client } = makeClient();
		const stop = client.onState((s) => states.push(s));
		client.connect();
		expect(states).toContain("connecting");
		stop();
		client.disconnect();
		// 'idle' transition won't be observed.
		const after = states.length;
		client.connect();
		expect(states.length).toBe(after);
	});

	it("isolates listener exceptions", async () => {
		const { client, wsRef } = makeClient();
		client.onState(() => { throw new Error("boom"); });
		const seen: ConnState[] = [];
		client.onState((s) => seen.push(s));
		// Should not throw.
		client.connect();
		wsRef.current!.simulateOpen();
		await Promise.resolve(); await Promise.resolve();
		expect(seen).toContain("open");
	});
});
