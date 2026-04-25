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
		updateFrame({ entities, hostId, hostX, hostY }) {
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
				};
			});
			void app.update(renderables, { cx: hostX, cy: hostY });
		},
	};
}

function buildHud(): HudLike {
	const badge = document.querySelector("[data-bx-state-badge]") as HTMLElement | null;
	const tickEl = document.querySelector("[data-bx-tick]") as HTMLElement | null;
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
	const loop = new GameLoop({
		config,
		renderer: adaptRenderer(app),
		hud: buildHud(),
	});
	loop.start();
	// Pause input when the tab loses focus so the player doesn't run
	// into a wall while reading something else.
	window.addEventListener("blur", () => loop.intent.clear());
	return loop;
}

// Auto-run when the page surface matches. Other surfaces import nothing
// from this file, so the bundler tree-shakes it out of their entries.
if (typeof document !== "undefined" && document.body?.dataset.surface === "play-game") {
	void bootGame();
}
