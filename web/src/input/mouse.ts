// Boxland — input/mouse.ts
//
// Mouse pump. Translates pointer events on the game canvas into
// CommandBus dispatches. Two canonical commands the pump emits:
//
//   * game.click-to-move (left click)  -> `Command<ClickToMoveArgs>`
//   * game.interact-at   (right click) -> `Command<InteractArgs>`
//
// Surfaces register their own handlers under those ids; rebinding via
// Settings is a future affordance (mouse buttons live in their own
// registry alongside hotkeys + gamepad).
//
// PLAN.md §6h "click-to-move" is exactly this path. Server pathfinding
// consumes the dispatched command via the game/loop and emits a
// `Move`-style intent vector or a (future) `Path` verb.

import type { CommandBus } from "@command-bus";

import { INPUT_COMMAND_IDS, type CameraReader, type ClickToMoveArgs, type InteractArgs } from "./types";

export interface AttachMouseOptions {
	/** Optional camera reader; if provided, the pump computes worldX /
	 *  worldY from the canvas pixel coords. Without it, only pixel
	 *  coords are populated. */
	camera?: CameraReader;
	/** Drop the right-click context menu so right-click can fire
	 *  `game.interact-at` cleanly. Default true. */
	preventContextMenu?: boolean;
}

/**
 * Attach pointer listeners to the host element. Returns an unsubscribe
 * for cleanup. Use `bus.register` to add `game.click-to-move` and
 * `game.interact-at` commands BEFORE attaching, otherwise dispatches
 * will silently no-op (the bus' default behavior).
 */
export function attachMouse(
	bus: CommandBus,
	host: HTMLElement,
	opts: AttachMouseOptions = {},
): () => void {
	const preventCtx = opts.preventContextMenu ?? true;

	const onPointerDown = async (raw: Event): Promise<void> => {
		const e = raw as PointerEvent;
		// Only react to primary mouse buttons; touch contributes through
		// the same path because pointerdown encompasses touch.
		if (e.pointerType === "mouse" && e.button !== 0 && e.button !== 2) return;

		const { x: pixelX, y: pixelY } = canvasCoords(host, e);
		const world = opts.camera ? canvasToWorld(opts.camera, host, pixelX, pixelY) : undefined;

		if (e.button === 2) {
			const args: InteractArgs = { pixelX, pixelY, ...world };
			await bus.dispatch(INPUT_COMMAND_IDS.interactAt, args);
			return;
		}
		const args: ClickToMoveArgs = {
			pixelX, pixelY,
			...world,
			button: e.button,
			shift: e.shiftKey, ctrl: e.ctrlKey, alt: e.altKey, meta: e.metaKey,
		};
		await bus.dispatch(INPUT_COMMAND_IDS.clickToMove, args);
	};

	const onContextMenu = (e: Event): void => {
		if (preventCtx) e.preventDefault();
	};

	host.addEventListener("pointerdown", onPointerDown);
	host.addEventListener("contextmenu", onContextMenu);
	return () => {
		host.removeEventListener("pointerdown", onPointerDown);
		host.removeEventListener("contextmenu", onContextMenu);
	};
}

// ---- Coordinate helpers ---------------------------------------------

/** Map a PointerEvent to host-relative pixel coords. */
export function canvasCoords(host: HTMLElement, e: { clientX: number; clientY: number }): { x: number; y: number } {
	const rect = host.getBoundingClientRect();
	return {
		x: Math.round(e.clientX - rect.left),
		y: Math.round(e.clientY - rect.top),
	};
}

/** Convert a canvas-pixel coord to world sub-pixels using the camera. */
export function canvasToWorld(
	cam: CameraReader,
	host: HTMLElement,
	pixelX: number,
	pixelY: number,
): { worldX: number; worldY: number } {
	const cw = cam.canvasW();
	const ch = cam.canvasH();
	const _ = host;
	const halfCw = cw / 2;
	const halfCh = ch / 2;
	const sub = cam.subPerCanvasPx();
	const worldX = cam.cx() + Math.round((pixelX - halfCw) * sub);
	const worldY = cam.cy() + Math.round((pixelY - halfCh) * sub);
	return { worldX, worldY };
}
