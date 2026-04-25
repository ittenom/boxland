// Boxland — game/entry-game.ts
//
// Page-level boot for /play/game/{id}. Reads the GameBootConfig from
// data-attributes on #bx-game-host (set by the Templ page), constructs
// the Pixi BoxlandApp + GameLoop, mounts the app inside the host
// element, and wires up the HUD badges.
//
// This file touches the DOM and Pixi. Logic lives in loop.ts so it can
// be unit-tested headlessly.

import { BoxlandApp } from "@render";
import { attachInput } from "@input";
import { SoundEngine, type SoundCatalog, type AudioCameraReader } from "@audio";
import { applyAudio, loadLocal } from "@settings";

import { GameLoop, type RendererLike, type HudLike } from "./loop";
import { PlaceholderCatalog } from "./catalog";
import type { CachedEntity } from "@net";
import type { ConnState } from "@net";
import type { GameBootConfig } from "./types";
import type { Renderable } from "@render";

/** Read GameBootConfig off the host element's data-attributes. Throws
 *  if any required attribute is missing — the Templ page guarantees
 *  them, but a developer mistake should fail loudly. */
function readBootConfig(host: HTMLElement): GameBootConfig {
	const ds = host.dataset;
	const need = (k: string): string => {
		const v = ds[k];
		if (!v) throw new Error(`game boot: missing data-bx-${k.replace(/[A-Z]/g, (c) => "-" + c.toLowerCase())}`);
		return v;
	};
	return {
		mapId:       Number(need("bxMapId")),
		mapName:     ds.bxMapName ?? "",
		mapWidth:    Number(need("bxMapWidth")),
		mapHeight:   Number(need("bxMapHeight")),
		wsURL:       need("bxWsUrl"),
		accessToken: need("bxAccessToken"),
	};
}

/** Build a RendererLike adapter over BoxlandApp.update. Maps the
 *  game's CachedEntity list to the renderer's Renderable shape. */
function adaptRenderer(app: BoxlandApp): RendererLike {
	return {
		updateFrame({ entities, hostId, hostX, hostY, cameraX, cameraY }) {
			const renderables: Renderable[] = entities.map((e: CachedEntity) => {
				// Override the host's position with the predicted one so
				// our own movement feels immediate; everyone else uses
				// the server position straight from the cache.
				const isHost = e.id === hostId;
				return {
					id: Number(e.id), // Pixi sprite map keyed by Number is fine for v1
					asset_id: e.typeId,
					anim_id: e.animId,
					anim_frame: e.animFrame,
					x: isHost ? hostX : e.x,
					y: isHost ? hostY : e.y,
					variant_id: e.variantId,
					tint: e.tint,
					layer: 0,
					// Forward EntityState's nameplate + hp_pct through so the
					// NameplateLayer can render them. Empty / 255 sentinels
					// hide the overlays (PLAN.md §6h, world.fbs).
					nameplate: e.nameplate,
					hpPct: e.hpPct,
				};
			});
			void app.update(renderables, { cx: cameraX, cy: cameraY });
		},
	};
}

function buildHud(): HudLike {
	const badge  = document.querySelector("[data-bx-state-badge]") as HTMLElement | null;
	const tickEl = document.querySelector("[data-bx-tick]") as HTMLElement | null;
	const camEl  = document.querySelector("[data-bx-camera-mode]") as HTMLElement | null;
	return {
		setState(s: ConnState) {
			if (badge) {
				badge.textContent = s;
				badge.dataset.bxState = s;
			}
		},
		setTick(tick) {
			if (tickEl) tickEl.textContent = `tick ${tick.toString()}`;
		},
		setCameraMode(mode) {
			if (camEl) {
				camEl.textContent = mode === "free-cam" ? "free-cam" : "follow";
				camEl.dataset.bxCameraMode = mode;
			}
		},
	};
}

/** Public entry. The /static/js boot dispatcher calls this when the
 *  body data-surface is "play-game". */
export async function bootGame(host: HTMLElement = document.getElementById("bx-game-host") as HTMLElement): Promise<GameLoop | null> {
	if (!host) return null;
	const config = readBootConfig(host);
	const app = await BoxlandApp.create({
		host,
		worldViewW: 480,
		worldViewH: 320,
		catalog: new PlaceholderCatalog(),
	});
	// Lazy SoundEngine -- the AudioContext only constructs on the
	// first user gesture (Chrome autoplay rule). Hand the loop the
	// engine; it drains AudioEvents from the Mailbox each frame.
	const soundCatalog: SoundCatalog = {
		// v1 has no audio assets baked yet; the engine no-ops missing URLs.
		// Real catalog wiring lands alongside the asset-pipeline integration.
		urlFor: () => undefined,
	};
	// Forward-reference the camera via a holder; the loop fills it in
	// after we construct it below.
	let cachedLoop: GameLoop | null = null;
	const audioCam: AudioCameraReader = {
		cx: () => {
			const l = cachedLoop;
			if (!l) return 0;
			const target = { cx: l.snapshot().hostX, cy: l.snapshot().hostY };
			return l.camera.snapshot(target).cx;
		},
		cy: () => {
			const l = cachedLoop;
			if (!l) return 0;
			const target = { cx: l.snapshot().hostX, cy: l.snapshot().hostY };
			return l.camera.snapshot(target).cy;
		},
	};
	const audio = new SoundEngine({
		catalog: soundCatalog,
		camera: audioCam,
	});
	// Hydrate volume buses from local settings (server fetch races).
	const localSettings = loadLocal("player");
	applyAudio(localSettings, audio);

	// Spectator mode flag: ?spectate=1 (or freeCam preference) flips
	// the loop into spectator mode -- HUD chrome switches, no Move
	// emissions, free-cam toggle is registered.
	const spectator = new URLSearchParams(window.location.search).get("spectate") === "1";
	const loop = new GameLoop({
		config,
		renderer: adaptRenderer(app),
		hud: buildHud(),
		audio,
		spectator,
		initialCameraMode: localSettings.spectator.freeCam ? "free-cam" : "follow",
	});
	cachedLoop = loop;
	// Reflect mode in the HUD on boot.
	if (spectator) {
		const hud = buildHud();
		hud.setCameraMode?.(loop.camera.getMode());
		document.body.dataset.bxSpectator = "1";
	}
	// First user gesture (a click) unblocks the AudioContext.
	host.addEventListener("pointerdown", () => audio.resume(), { once: true });
	// Expose the bus globally so the Settings page can list the
	// rebindable commands. Read-only access; the loop owns it.
	(globalThis as unknown as { boxlandBus?: typeof loop.bus }).boxlandBus = loop.bus;
	// Expose the audio engine so the Settings page can drive its gain
	// buses live as the user drags the volume sliders.
	(globalThis as unknown as { boxlandAudio?: SoundEngine }).boxlandAudio = audio;
	loop.start();

	// Wire the keyboard + mouse + gamepad pumps onto the loop's bus.
	// Movement commands are already registered via installMovementBindings
	// (called from GameLoop's constructor); attachInput just connects
	// the live event sources to the dispatcher.
	const detachInput = attachInput(loop.bus, {
		mouseHost: host,
		// Hold-state tracking: each Move command's setter mutates the
		// MovementIntent; we mirror its current direction set so the
		// blur-safety pass can release whichever combos are still held.
		heldCombos: () => heldMoveCombos(loop),
		// Stick axes feed the same MovementIntent the keyboard does.
		// MovementIntent normally only takes booleans; for stick input
		// we override with the raw vector via setStickVector (added in
		// task #117 alongside the input module).
		onAxes: ({ vx, vy }) => loop.intent.setStickVector(vx, vy),
	});
	const off = (): void => detachInput();
	window.addEventListener("pagehide", off, { once: true });
	return loop;
}

/** Approximate which combos are currently held by reading the
 *  MovementIntent. Used by the blur-safety hook so we release every
 *  axis cleanly when the tab loses focus. */
function heldMoveCombos(loop: GameLoop): string[] {
	const out: string[] = [];
	const v = loop.intent.vector();
	if (v.vy < 0) out.push("ArrowUp");
	if (v.vy > 0) out.push("ArrowDown");
	if (v.vx < 0) out.push("ArrowLeft");
	if (v.vx > 0) out.push("ArrowRight");
	return out;
}

// Auto-run when the page surface matches. Other surfaces import nothing
// from this file, so the bundler tree-shakes it out of their entries.
if (typeof document !== "undefined" && document.body?.dataset.surface === "play-game") {
	void bootGame();
}
