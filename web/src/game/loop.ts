// Boxland — game/loop.ts
//
// The orchestrator. Wires the four shared modules together:
//
//   1. NetClient  — opens the WS, applies Diffs into a Mailbox.
//   2. Mailbox    — entity + tile + lighting cache (PLAN.md §4h).
//   3. CommandBus — movement intent commands (PLAN.md §6h).
//   4. Collision  — shared swept-AABB module for client-side prediction.
//
// The loop runs at the browser's animation-frame cadence; every frame:
//   a. apply local intent via predictStep
//   b. on net Diff -> reconcile host position
//   c. emit a Move intent to the server (10 Hz tick gate)
//   d. assemble Renderables from Mailbox + predicted host pos
//   e. hand them to the renderer (Pixi or test stub)
//
// The renderer is dependency-injected so headless tests can drive the
// loop without standing up Pixi. PLAN.md §6f "shared PixiJS renderer"
// is the production implementation; tests use the StubRenderer in
// loop.test.ts.

import {
	NetClient,
	Mailbox,
	Realm,
	ClientKind,
	type AppliedDiff,
	type AuthParams,
	type ConnState,
	type CachedEntity,
} from "@net";
import { CommandBus } from "@command-bus";
import type { SoundEngine, PositionalSound } from "@audio";

import type { GameBootConfig, LocalState } from "./types";
import {
	freshLocalState,
	predictStep,
	reconcile,
	resolveHost,
} from "./prediction";
import { installMovementBindings, type MovementIntent } from "./intents";
import { mailboxAsWorld } from "./world";
import {
	GameCamera,
	buildCameraToggleCommand,
	CAMERA_TOGGLE_COMMAND_ID,
	type CameraMode,
} from "./camera";

/** Cadence the client emits MovePayloads at. Server runs 10 Hz so
 *  matching the cadence keeps inputs responsive without flooding. */
const MOVE_INTENT_INTERVAL_MS = 100;

/** Renderer surface the loop expects. Production: a thin wrapper over
 *  render/BoxlandApp.update. Tests: stub that records frames.
 *
 * `cameraX`/`cameraY` are the world-space sub-pixel centre of the
 * viewport (follows host in normal play; user-controlled in spectator
 * free-cam). The host fields stay separate so the renderer can apply
 * client-side prediction to its own sprite.
 */
export interface RendererLike {
	updateFrame(args: {
		entities: CachedEntity[];
		hostId: bigint;
		hostX: number;
		hostY: number;
		cameraX: number;
		cameraY: number;
	}): void;
}

/** HUD surface: state + tick badge updates. Optional. */
export interface HudLike {
	setState(s: ConnState): void;
	setTick(tick: bigint): void;
	/** Optional: render the spectator camera mode in the HUD chrome.
	 *  Ignored when the surface isn't in a spectator session. */
	setCameraMode?(mode: CameraMode): void;
}

/** Tiny scheduler abstraction so tests drive the loop deterministically. */
export interface LoopScheduler {
	requestFrame(cb: (now: number) => void): unknown;
	cancelFrame(handle: unknown): void;
	now(): number;
}

const defaultScheduler: LoopScheduler = {
	requestFrame: (cb) =>
		typeof globalThis.requestAnimationFrame === "function"
			? globalThis.requestAnimationFrame(cb)
			: globalThis.setTimeout(() => cb(Date.now()), 16),
	cancelFrame: (h) => {
		if (typeof globalThis.cancelAnimationFrame === "function") {
			globalThis.cancelAnimationFrame(h as number);
		} else {
			globalThis.clearTimeout(h as ReturnType<typeof setTimeout>);
		}
	},
	now: () => (typeof performance !== "undefined" ? performance.now() : Date.now()),
};

export interface GameLoopOptions {
	config: GameBootConfig;
	renderer: RendererLike;
	hud?: HudLike;
	bus?: CommandBus;
	mailbox?: Mailbox;
	netClient?: NetClient;
	scheduler?: LoopScheduler;
	/** Optional Web Audio engine. The loop drains audio events from
	 *  the mailbox each frame and forwards them. Omit for tests. */
	audio?: SoundEngine;
	/** Spectator session? Skips Move emissions and lets the camera
	 *  free-cam vs follow under the user's control. PLAN.md §6h
	 *  "spectator UI affordances". */
	spectator?: boolean;
	/** Initial camera mode for spectator sessions. Defaults to
	 *  "follow"; the Settings.spectator.freeCam preference flips this
	 *  to "free-cam" when boot picks the player up. */
	initialCameraMode?: CameraMode;
	/** Optional hook fired with every freshly-seen asset id. The
	 *  game/entry-game wiring forwards these to RemoteAssetCatalog.ensure()
	 *  so the renderer's TextureCache has the URL + frame metadata
	 *  ready before the next paint. Tests omit this. */
	onAssetIds?: (ids: number[]) => void;
}

/**
 * GameLoop is the per-page singleton. Construct it after the renderer
 * has been mounted; call start() to open the WS + begin the frame
 * loop. Idempotent stop() tears everything down.
 */
export class GameLoop {
	readonly bus: CommandBus;
	readonly mailbox: Mailbox;
	readonly net: NetClient;
	readonly intent: MovementIntent;
	readonly camera: GameCamera;
	readonly spectator: boolean;

	private readonly renderer: RendererLike;
	private readonly hud: HudLike | undefined;
	private readonly scheduler: LoopScheduler;
	private readonly config: GameBootConfig;
	private readonly audio: SoundEngine | undefined;
	private readonly onAssetIds: ((ids: number[]) => void) | undefined;
	private readonly seenAssetIds = new Set<number>();

	private state: LocalState = freshLocalState();
	private rafHandle: unknown = null;
	private running = false;
	private lastFrameMs = 0;
	private lastIntentSentMs = 0;
	private lastSentVx = 0;
	private lastSentVy = 0;
	private hostHinted = false; // true once we've set hostId from a Diff
	private offState: (() => void) | null = null;
	private offDiff: (() => void) | null = null;

	constructor(opts: GameLoopOptions) {
		this.config = opts.config;
		this.renderer = opts.renderer;
		this.hud = opts.hud;
		this.scheduler = opts.scheduler ?? defaultScheduler;
		this.bus = opts.bus ?? new CommandBus();
		this.mailbox = opts.mailbox ?? new Mailbox();
		this.audio = opts.audio;
		this.onAssetIds = opts.onAssetIds;
		this.spectator = opts.spectator ?? false;
		this.camera = new GameCamera();
		if (opts.initialCameraMode) this.camera.setMode(opts.initialCameraMode);

		this.intent = installMovementBindings(this.bus);

		// Spectators get a camera-toggle command they can rebind in
		// Settings; default combo is "C" so it doesn't conflict with
		// the WASD pan keys.
		if (this.spectator) {
			const toggle = buildCameraToggleCommand({
				camera: this.camera,
				onToggle: (mode) => this.hud?.setCameraMode?.(mode),
			});
			this.bus.register(toggle);
			this.bus.bindHotkey("C", CAMERA_TOGGLE_COMMAND_ID);
		}

		const ticketURL = opts.config.ticketURL ?? "/play/ws-ticket";
		const authFactory: () => Promise<AuthParams> = async () => {
			// First connect uses the JWT the server baked into the page;
			// subsequent reconnects mint a fresh one via the ticket
			// endpoint so we never present an expired access token.
			let token = opts.config.accessToken;
			if (this.hostHinted) {
				try {
					const r = await fetch(ticketURL, { method: "POST", credentials: "same-origin" });
					if (r.ok) {
						const body = await r.json();
						if (typeof body.token === "string") token = body.token;
					}
				} catch {
					// Fall back to the page-baked token; the WS will reject
					// it if expired and the gateway closes 4xxx -> fatal.
				}
			}
			return {
				realm: Realm.Player,
				token,
				clientKind: ClientKind.Web,
				clientVersion: "0.0.0-dev",
			};
		};

		this.net = opts.netClient ?? new NetClient(opts.config.wsURL, {
			auth: authFactory,
			mailbox: this.mailbox,
		});
	}

	start(): void {
		if (this.running) return;
		this.running = true;

		// HUD wiring.
		this.offState = this.net.onState((s) => {
			this.hud?.setState(s);
			if (s === "open") {
				// Ask the server for our entity by joining the map.
				this.net.sendJoinMap({ mapId: this.config.mapId });
			}
			if (s === "closed" || s === "fatal") {
				// Don't keep the player walking forever during a drop.
				this.intent.clear();
			}
		});

		// Diff-driven reconciliation.
		this.offDiff = this.mailbox.onDiff((d) => this.onDiff(d));

		this.net.connect();
		this.queueFrame();
	}

	stop(): void {
		if (!this.running) return;
		this.running = false;
		if (this.rafHandle != null) {
			this.scheduler.cancelFrame(this.rafHandle);
			this.rafHandle = null;
		}
		this.offState?.(); this.offState = null;
		this.offDiff?.();  this.offDiff = null;
		this.net.disconnect(1000, "page leave");
	}

	/** Snapshot the loop's current LocalState. Tests + HUD use it. */
	snapshot(): LocalState { return { ...this.state }; }

	// ---- internals ----

	private queueFrame(): void {
		if (!this.running) return;
		this.rafHandle = this.scheduler.requestFrame((now) => {
			this.rafHandle = null;
			this.tick(now);
			this.queueFrame();
		});
	}

	/** One frame: read intent -> predict -> emit Move (rate-limited)
	 *  -> render. Separated from queueFrame so tests drive it directly. */
	tick(nowMs: number): void {
		const dtMs = this.lastFrameMs === 0 ? 16 : Math.max(0, nowMs - this.lastFrameMs);
		this.lastFrameMs = nowMs;

		// Pull intent into LocalState.
		const v = this.intent.vector();
		this.state.intentVx = v.vx;
		this.state.intentVy = v.vy;

		if (this.spectator && this.camera.getMode() === "free-cam") {
			// In free-cam, the input vector pans the camera instead of
			// moving the entity. Spectators don't own an entity, so
			// nothing else needs to happen here.
			this.camera.pan(v.vx, v.vy, dtMs);
		} else {
			// Predict the host's local position.
			const world = mailboxAsWorld({ values: () => this.mailbox.allTiles() });
			this.state = predictStep(this.state, dtMs, world);

			// Rate-limit MovePayload emissions to the tick rate. We
			// always send if the intent changed since the last emit so
			// quick taps don't get coalesced away. Spectator sessions
			// don't own an entity, so we never emit Move from them.
			const intentChanged = v.vx !== this.lastSentVx || v.vy !== this.lastSentVy;
			const dueByCadence  = nowMs - this.lastIntentSentMs >= MOVE_INTENT_INTERVAL_MS;
			if (!this.spectator && (intentChanged || dueByCadence)) {
				if (this.net.getState() === "open") {
					this.net.sendMove({ vx: v.vx, vy: v.vy });
					this.lastIntentSentMs = nowMs;
					this.lastSentVx = v.vx;
					this.lastSentVy = v.vy;
				}
			}
		}

		// Render against the camera. Spectator + follow tracks the host
		// (or whatever the server sets as the spectate target later);
		// player + follow tracks the host's predicted position.
		const target = { cx: this.state.hostX, cy: this.state.hostY };
		const cam = this.camera.snapshot(target);
		const entities: CachedEntity[] = [...this.mailbox.allEntities()];
		this.renderer.updateFrame({
			entities,
			hostId: this.state.hostId,
			hostX: this.state.hostX,
			hostY: this.state.hostY,
			cameraX: cam.cx,
			cameraY: cam.cy,
		});

		// Drain queued AudioEvents into the Web Audio engine. Empty
		// when no events fired this tick; cheap enough to call every frame.
		if (this.audio) {
			const audio = this.mailbox.drainAudio();
			if (audio.length > 0) {
				const events: PositionalSound[] = audio.map((a) => ({
					soundId: a.soundId,
					hasPosition: a.hasPosition,
					x: a.x, y: a.y,
					volume: a.volume,
					pitch: a.pitch,
				}));
				this.audio.playMany(events);
			}
		}
	}

	private onDiff(applied: AppliedDiff): void {
		this.state.serverTick = applied.tick;
		this.hud?.setTick(applied.tick);
		this.net.sendAckTick({ lastAppliedTick: applied.tick });

		// First-host-hint heuristic: the very first added id we see is
		// our entity. Real servers will eventually echo a "your entity
		// is N" frame on JoinMap; until then this approximation works
		// because the server spawns the joining player before broadcasting.
		if (!this.hostHinted && applied.addedIds.length > 0 && this.state.hostId === 0n) {
			this.state.hostId = applied.addedIds[0]!;
			this.hostHinted = true;
		}

		// Collect asset ids the catalog hasn't seen yet so the texture
		// pipeline can prefetch them before the next paint. We look at
		// added (newly entered AOI) AND moved (in case an entity's
		// type/asset changed via SetSprite automation) — `seenAssetIds`
		// dedups so it costs O(1) per id.
		if (this.onAssetIds && (applied.addedIds.length > 0 || applied.movedIds.length > 0)) {
			const fresh: number[] = [];
			const collect = (id: bigint) => {
				const ent = this.mailbox.getEntity(id);
				if (!ent) return;
				// Today the renderer adapter uses `typeId` as the
				// asset_id (legacy from before the catalog wiring);
				// fold that here so the collected ids match what the
				// catalog will be asked for. Once typeId/assetId are
				// untangled this becomes ent.assetId only.
				const assetID = ent.typeId;
				if (assetID > 0 && !this.seenAssetIds.has(assetID)) {
					this.seenAssetIds.add(assetID);
					fresh.push(assetID);
				}
			};
			for (const id of applied.addedIds) collect(id);
			for (const id of applied.movedIds) collect(id);
			if (fresh.length > 0) this.onAssetIds(fresh);
		}

		// Reconcile if the server moved our host.
		if (this.state.hostId !== 0n) {
			const server = resolveHost(this.state, (id) => this.mailbox.getEntity(id));
			if (server) {
				if (this.state.hostX === 0 && this.state.hostY === 0) {
					// First time we see our host -> teleport to its position.
					this.state.hostX = server.x;
					this.state.hostY = server.y;
				} else if (applied.movedIds.includes(this.state.hostId)) {
					const out = reconcile(this.state, server);
					this.state = out.state;
				}
			}
		}
	}

}
