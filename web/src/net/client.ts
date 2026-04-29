// Boxland — net/client.ts
//
// WebSocket client. Owns a single live connection; auto-reconnects with
// exponential backoff + jitter on transport drops. Higher layers (game
// loop, mapmaker) push intents through `send*` methods + subscribe to
// state + diff events.
//
// PLAN.md §1 protocol: first frame after upgrade is FlatBuffers `Auth`,
// every later frame is a `ClientMessage` envelope. The client sends
// Auth automatically on each (re)connect using the `AuthParams` factory
// the host passes in.
//
// Transport injection: tests pass a fake WebSocket constructor + a fake
// scheduler so the backoff schedule is deterministic and we don't need
// jsdom's WebSocket mock. Production callers leave both undefined.

import {
	encodeAuth,
	envelopeJoinMap,
	envelopeLeaveMap,
	envelopeMove,
	envelopeHeartbeat,
	envelopeAckTick,
	envelopeSpectate,
	decodeDiff,
} from "./codec";
import type {
	AckTick,
	AppliedDiff,
	AuthParams,
	BackoffConfig,
	ConnState,
	DiffListener,
	ErrorListener,
	JoinMapIntent,
	LeaveMapIntent,
	MoveIntent,
	SpectateIntent,
	StateListener,
} from "./types";
import { Mailbox } from "./mailbox";

// Subset of the WebSocket interface the client uses. Tests implement
// just these members; production binds to the global WebSocket.
export interface WSLike {
	binaryType: string;
	readyState: number;
	onopen: ((ev: Event) => void) | null;
	onmessage: ((ev: { data: ArrayBuffer | Uint8Array | Blob | string }) => void) | null;
	onerror: ((ev: Event) => void) | null;
	onclose: ((ev: { code: number; reason: string; wasClean?: boolean }) => void) | null;
	send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void;
	close(code?: number, reason?: string): void;
}

export interface WSConstructor {
	(url: string): WSLike;
}

export interface Scheduler {
	setTimeout(cb: () => void, ms: number): unknown;
	clearTimeout(handle: unknown): void;
	now(): number;
	random(): number;
}

const defaultScheduler: Scheduler = {
	setTimeout: (cb, ms) => globalThis.setTimeout(cb, ms),
	clearTimeout: (h) => globalThis.clearTimeout(h as ReturnType<typeof setTimeout>),
	now: () => Date.now(),
	random: () => Math.random(),
};

export interface NetClientOptions {
	/** Resolves to a fresh AuthParams each time we (re)connect. */
	auth: () => AuthParams | Promise<AuthParams>;
	/** Backoff knobs; defaults are sensible for v1. */
	backoff?: BackoffConfig;
	/** Optional WS factory injection (tests). Defaults to native WebSocket. */
	wsFactory?: WSConstructor;
	/** Optional clock injection (tests). */
	scheduler?: Scheduler;
	/** Optional shared mailbox; one is created if omitted. */
	mailbox?: Mailbox;
	/**
	 * Optional pre-mailbox hook. Called for every binary frame
	 * BEFORE the default diff-decode path. Return `true` to claim
	 * the frame (the client skips diff decoding). Used by the
	 * editor surfaces to route EditorSnapshot / EditorDiff frames
	 * (which share the wire with game Diffs but carry a different
	 * file_identifier).
	 */
	onRawFrame?: (bytes: Uint8Array) => boolean;
}

const DEFAULT_BACKOFF: Required<BackoffConfig> = {
	baseMs: 250,
	factor: 2,
	maxMs: 30_000,
	jitter: 0.25,
	maxAttempts: 0,
};

/**
 * NetClient: one logical connection to the gateway, transparently
 * reconnecting on drops. Surface area:
 *
 *   const c = new NetClient({ url: ..., auth: () => ({...}) });
 *   c.onState((s) => ...);
 *   c.onDiff((d) => ...);
 *   c.connect();
 *   c.sendMove(...);
 *
 * The mailbox is exposed so callers can read entity caches between diffs.
 */
export class NetClient {
	readonly url: string;
	readonly mailbox: Mailbox;

	private readonly opts: NetClientOptions;
	private readonly backoff: Required<BackoffConfig>;
	private readonly scheduler: Scheduler;
	private readonly wsFactory: WSConstructor;

	private ws: WSLike | null = null;
	private state: ConnState = "idle";
	private attempts = 0;
	private retryHandle: unknown = null;
	private explicitlyStopped = false;

	private readonly stateListeners = new Set<StateListener>();
	private readonly diffListeners = new Set<DiffListener>();
	private readonly errorListeners = new Set<ErrorListener>();

	constructor(url: string, opts: NetClientOptions) {
		this.url = url;
		this.opts = opts;
		this.backoff = { ...DEFAULT_BACKOFF, ...(opts.backoff ?? {}) };
		this.scheduler = opts.scheduler ?? defaultScheduler;
		this.wsFactory =
			opts.wsFactory ??
			((u: string) => new (globalThis as unknown as { WebSocket: { new (u: string): WSLike } }).WebSocket(u));
		this.mailbox = opts.mailbox ?? new Mailbox();
		// Mailbox -> client diff pipe: re-emit applied diffs to subscribers.
		this.mailbox.onDiff((d) => this.emitDiff(d));
	}

	// ---- Public lifecycle ----

	/** Open + auth. No-op if already connecting/open. Idempotent. */
	connect(): void {
		this.explicitlyStopped = false;
		if (this.state === "open" || this.state === "connecting" || this.state === "authenticating") {
			return;
		}
		this.openSocket();
	}

	/**
	 * Close + stop reconnecting. Future `connect()` calls re-open.
	 *
	 * `code`/`reason` mirror the WebSocket Close API.
	 */
	disconnect(code = 1000, reason = ""): void {
		this.explicitlyStopped = true;
		this.cancelRetry();
		if (this.ws) {
			try { this.ws.close(code, reason); } catch { /* ignore */ }
		}
		this.transition("idle");
	}

	/** Current connection state (synchronous). */
	getState(): ConnState { return this.state; }

	/** Current backoff attempt count (post-failure). 0 if connected. */
	getAttempt(): number { return this.attempts; }

	// ---- Listener registration ----

	onState(l: StateListener): () => void {
		this.stateListeners.add(l);
		return () => this.stateListeners.delete(l);
	}
	onDiff(l: DiffListener): () => void {
		this.diffListeners.add(l);
		return () => this.diffListeners.delete(l);
	}
	onError(l: ErrorListener): () => void {
		this.errorListeners.add(l);
		return () => this.errorListeners.delete(l);
	}

	// ---- Outbound intents (no-op if not open) ----

	sendJoinMap(p: JoinMapIntent): boolean    { return this.sendBlob(envelopeJoinMap(p)); }
	sendLeaveMap(p: LeaveMapIntent): boolean  { return this.sendBlob(envelopeLeaveMap(p)); }
	sendMove(p: MoveIntent): boolean          { return this.sendBlob(envelopeMove(p)); }
	sendSpectate(p: SpectateIntent): boolean  { return this.sendBlob(envelopeSpectate(p)); }
	sendHeartbeat(now?: bigint): boolean      {
		const t = now ?? BigInt(this.scheduler.now());
		return this.sendBlob(envelopeHeartbeat(t));
	}
	sendAckTick(p: AckTick): boolean          { return this.sendBlob(envelopeAckTick(p)); }

	/** Escape hatch: send a pre-built blob (DesignerCommand, etc.). */
	sendRaw(blob: Uint8Array): boolean { return this.sendBlob(blob); }

	// ---- Internals: socket lifecycle ----

	private openSocket(): void {
		this.cancelRetry();
		this.transition("connecting");
		let ws: WSLike;
		try {
			ws = this.wsFactory(this.url);
		} catch (err) {
			this.emitError(asError(err, "ws factory threw"));
			this.scheduleRetry();
			return;
		}
		ws.binaryType = "arraybuffer";
		this.ws = ws;

		ws.onopen = () => { void this.handleOpen(); };
		ws.onmessage = (ev) => this.handleMessage(ev.data);
		ws.onerror = (ev) => {
			const msg = (ev as unknown as { message?: string }).message ?? "";
			this.emitError(new Error("ws error: " + msg));
		};
		ws.onclose = (ev) => this.handleClose(ev.code, ev.reason);
	}

	private async handleOpen(): Promise<void> {
		this.transition("authenticating");
		let blob: Uint8Array;
		try {
			const params = await this.opts.auth();
			blob = encodeAuth(params);
		} catch (err) {
			this.emitError(asError(err, "auth params"));
			// Authoring failed before we could even handshake; treat as
			// fatal so callers don't loop on a bad token / config.
			if (this.ws) { try { this.ws.close(4000, "auth params failed"); } catch { /* ignore */ } }
			this.transition("fatal");
			return;
		}
		try {
			this.ws?.send(blob);
		} catch (err) {
			this.emitError(asError(err, "ws send Auth"));
			return;
		}
		// We optimistically transition to "open" once Auth has been sent.
		// The server's first downstream frame (Snapshot or Diff) is the
		// real "auth accepted" signal; if it never arrives the read
		// timeout drops the connection and the close handler retries.
		this.transition("open");
		this.attempts = 0;
	}

	private handleMessage(data: ArrayBuffer | Uint8Array | Blob | string): void {
		// String frames are debug only; real game frames are binary.
		if (typeof data === "string") return;
		// Blob: requires async; treat as fatal-protocol-violation for v1
		// (browsers send ArrayBuffer when binaryType='arraybuffer').
		if (data instanceof Blob) {
			this.emitError(new Error("net: unexpected Blob frame; binaryType not honored"));
			return;
		}
		const u8 = data instanceof Uint8Array ? data : new Uint8Array(data);
		// Pre-mailbox hook: surfaces that ride the same WS but
		// produce non-Diff frames (editor snapshots / diffs)
		// claim those here. When the hook returns true, we
		// don't try to decode the bytes as a Diff.
		if (this.opts.onRawFrame && this.opts.onRawFrame(u8)) return;
		const diff = decodeDiff(u8);
		if (!diff) {
			this.emitError(new Error("net: short/invalid Diff frame"));
			return;
		}
		try {
			this.mailbox.applyDiff(diff);
		} catch (err) {
			this.emitError(asError(err, "mailbox apply"));
		}
	}

	private handleClose(code: number, reason: string): void {
		this.ws = null;
		// 1008 is the standard WebSocket policy-violation close code;
		// the Boxland gateway uses it for failed auth handshakes. Codes
		// 4000+ are app-level fatal (auth bad, realm violation). Treat
		// both as terminal so one-shot designer tickets don't retry-spam.
		if (code === 1008 || (code >= 4000 && code < 5000)) {
			this.transition("fatal");
			this.emitError(new Error(`net: fatal close ${code} ${reason}`));
			return;
		}
		this.transition("closed");
		if (!this.explicitlyStopped) this.scheduleRetry();
	}

	private sendBlob(blob: Uint8Array): boolean {
		if (!this.ws || this.state !== "open") return false;
		try {
			// Many WebSocket implementations want the underlying
			// ArrayBuffer, not a Uint8Array view.
			const buf = blob.buffer.slice(blob.byteOffset, blob.byteOffset + blob.byteLength) as ArrayBuffer;
			this.ws.send(buf);
			return true;
		} catch (err) {
			this.emitError(asError(err, "ws send"));
			return false;
		}
	}

	// ---- Backoff ----

	/** Compute the next backoff in ms. Exposed for tests + introspection. */
	nextBackoff(attempt = this.attempts): number {
		const { baseMs, factor, maxMs, jitter } = this.backoff;
		const expo = Math.min(maxMs, baseMs * Math.pow(factor, Math.max(0, attempt)));
		// Jitter is symmetric: ± `jitter` ratio.
		const jitterAmt = expo * jitter;
		const delta = (this.scheduler.random() * 2 - 1) * jitterAmt;
		return Math.max(0, Math.round(expo + delta));
	}

	private scheduleRetry(): void {
		if (this.explicitlyStopped) return;
		const { maxAttempts } = this.backoff;
		if (maxAttempts > 0 && this.attempts >= maxAttempts) {
			this.transition("fatal");
			this.emitError(new Error(`net: gave up after ${this.attempts} attempts`));
			return;
		}
		const delay = this.nextBackoff();
		this.attempts++;
		this.retryHandle = this.scheduler.setTimeout(() => {
			this.retryHandle = null;
			this.openSocket();
		}, delay);
	}

	private cancelRetry(): void {
		if (this.retryHandle != null) {
			this.scheduler.clearTimeout(this.retryHandle);
			this.retryHandle = null;
		}
	}

	// ---- Emit helpers ----

	private transition(next: ConnState): void {
		const prev = this.state;
		if (prev === next) return;
		this.state = next;
		for (const l of this.stateListeners) {
			try { l(next, prev); } catch { /* listeners are isolated */ }
		}
	}

	private emitDiff(d: AppliedDiff): void {
		for (const l of this.diffListeners) {
			try { l(d); } catch { /* isolate */ }
		}
	}

	private emitError(err: Error): void {
		for (const l of this.errorListeners) {
			try { l(err); } catch { /* isolate */ }
		}
	}
}

function asError(e: unknown, ctx: string): Error {
	if (e instanceof Error) return new Error(`${ctx}: ${e.message}`);
	return new Error(`${ctx}: ${String(e)}`);
}
