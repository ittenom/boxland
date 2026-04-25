// Boxland — input/ barrel.
//
// One import surface for the player game module + Sandbox + Mapmaker.
// PLAN.md §6h: every input is a Command on the shared bus, rebindable
// via Settings.

import type { CommandBus } from "@command-bus";

import { attachKeyboard, attachBlurSafety } from "./keyboard";
import { attachMouse, type AttachMouseOptions } from "./mouse";
import { attachGamepad, type AxisListener, type GamepadAttachOptions } from "./gamepad";

export { attachKeyboard, attachBlurSafety } from "./keyboard";
export { attachMouse, canvasCoords, canvasToWorld } from "./mouse";
export type { AttachMouseOptions } from "./mouse";
export { attachGamepad, readStick } from "./gamepad";
export type { GamepadAttachOptions, AxisListener, GamepadScheduler } from "./gamepad";
export { INPUT_COMMAND_IDS } from "./types";
export type {
	ClickToMoveArgs,
	InteractArgs,
	AxisVector,
	CameraReader,
} from "./types";

export interface AttachInputOptions {
	/** DOM element scoped for keyboard listeners. Defaults to window. */
	keyboardTarget?: EventTarget;
	/** DOM element pointer events fire on. Required for mouse pump. */
	mouseHost?: HTMLElement;
	/** Optional camera reader for click-to-world conversion. */
	camera?: AttachMouseOptions["camera"];
	/** Optional gamepad axis listener. */
	onAxes?: AxisListener;
	/** Held-key tracker for blur-safety. Returns the combos currently
	 *  pressed; the keyboard pump fires `release` on each at blur. */
	heldCombos?: () => Iterable<string>;
	/** Gamepad options. */
	gamepad?: GamepadAttachOptions;
	/** Mouse options. */
	mouse?: Omit<AttachMouseOptions, "camera">;
}

/**
 * Attach every input source the host wants in one call. Returns a
 * single dispose() that tears all of them down. Surfaces that need
 * just one source can call the per-source `attach*` helpers directly.
 */
export function attachInput(bus: CommandBus, opts: AttachInputOptions = {}): () => void {
	const offs: Array<() => void> = [];
	offs.push(attachKeyboard(bus, opts.keyboardTarget));
	if (opts.heldCombos) {
		offs.push(attachBlurSafety(bus, opts.heldCombos, opts.keyboardTarget));
	}
	if (opts.mouseHost) {
		const camera = opts.camera;
		offs.push(attachMouse(bus, opts.mouseHost, {
			...(opts.mouse ?? {}),
			...(camera !== undefined ? { camera } : {}),
		}));
	}
	offs.push(attachGamepad(bus, opts.onAxes, opts.gamepad));
	return () => {
		for (const off of offs) off();
	};
}
