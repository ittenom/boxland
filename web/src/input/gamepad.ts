// Boxland — input/gamepad.ts
//
// Gamepad pump. Polls navigator.getGamepads() at requestAnimationFrame
// cadence and dispatches:
//
//   * Button rising edge  -> bus.handleGamepadButton(idx)
//   * Button falling edge -> bus.handleGamepadButtonRelease(idx)
//   * Stick axes          -> AxisListener({vx, vy}) clamped to ±1000
//
// PLAN.md §6h: gamepad inputs join the same Command bus the keyboard
// + mouse use; rebinding works through gamepadBindings(). Axes drive
// the same MovementIntent the keyboard does, so the game loop sees
// one unified vector regardless of input source.
//
// We poll instead of listening for gamepad button events because the
// standard doesn't actually emit per-button events; only connect /
// disconnect. The polling cost is negligible (<1µs per frame).

import type { CommandBus } from "@command-bus";

import type { AxisVector } from "./types";

export interface GamepadAttachOptions {
	/** Stick deadzone (0..1). Below this magnitude the axes report 0.
	 *  Default 0.18 — tuned for Xbox sticks; cheap pads need higher. */
	deadzone?: number;
	/** Index of the stick to read (0 = left stick X/Y axes). */
	stickIndex?: 0 | 1;
	/** Polling tick driver. Defaults to requestAnimationFrame; tests
	 *  pass a fake driver to step the loop deterministically. */
	scheduler?: GamepadScheduler;
	/** Source of gamepads. Defaults to navigator.getGamepads(); tests
	 *  inject a stub. */
	gamepadSource?: () => ReadonlyArray<Gamepad | null>;
}

/** Listener invoked every poll with the current axis vector (or
 *  {0,0} when no pad is connected). The orchestrator's MovementIntent
 *  reads this. */
export type AxisListener = (v: AxisVector) => void;

/** Tick driver injected for tests. Default uses rAF + cancelAF. */
export interface GamepadScheduler {
	requestFrame(cb: () => void): unknown;
	cancelFrame(handle: unknown): void;
}

const defaultScheduler: GamepadScheduler = {
	requestFrame: (cb) =>
		typeof globalThis.requestAnimationFrame === "function"
			? globalThis.requestAnimationFrame(cb)
			: globalThis.setTimeout(cb, 16),
	cancelFrame: (h) => {
		if (typeof globalThis.cancelAnimationFrame === "function") {
			globalThis.cancelAnimationFrame(h as number);
		} else {
			globalThis.clearTimeout(h as ReturnType<typeof setTimeout>);
		}
	},
};

/**
 * Attach the gamepad pump. Returns an unsubscribe; calling it twice is
 * idempotent. `onAxes` is optional — surfaces that only care about
 * button bindings can omit it.
 */
export function attachGamepad(
	bus: CommandBus,
	onAxes?: AxisListener,
	opts: GamepadAttachOptions = {},
): () => void {
	const deadzone = opts.deadzone ?? 0.18;
	const stickIdx = opts.stickIndex ?? 0;
	const sched = opts.scheduler ?? defaultScheduler;
	const source = opts.gamepadSource
		?? (() => navigatorGetGamepads());

	let stopped = false;
	let handle: unknown = null;
	const lastPressed = new Map<number, boolean[]>();

	const tick = (): void => {
		if (stopped) return;
		const pads = source();
		let v: AxisVector = { vx: 0, vy: 0 };
		for (const pad of pads) {
			if (!pad) continue;
			// Buttons: rising edge -> press, falling edge -> release.
			const prev = lastPressed.get(pad.index) ?? [];
			const curr = pad.buttons.map((b) => b.pressed);
			for (let i = 0; i < curr.length; i++) {
				const wasDown = !!prev[i];
				const isDown = !!curr[i];
				if (isDown && !wasDown) void bus.handleGamepadButton(i);
				else if (!isDown && wasDown) void bus.handleGamepadButtonRelease(i);
			}
			lastPressed.set(pad.index, curr);

			// First connected pad wins for the axis vector.
			if (v.vx === 0 && v.vy === 0) {
				v = readStick(pad, stickIdx, deadzone);
			}
		}
		if (onAxes) onAxes(v);
		handle = sched.requestFrame(tick);
	};

	// Bail only if there's no source AT ALL (no injected source AND no
	// browser navigator). Tests that inject `gamepadSource` always run.
	const hasNavigator = typeof navigator !== "undefined" && typeof navigator.getGamepads === "function";
	if (!hasNavigator && !opts.gamepadSource) {
		return () => { stopped = true; };
	}
	handle = sched.requestFrame(tick);
	return () => {
		stopped = true;
		if (handle != null) sched.cancelFrame(handle);
	};
}

/** Read a stick into {vx, vy} clamped to [-1000..1000]. Stick `0`
 *  reads axes 0+1; stick `1` reads axes 2+3 (standard mapping). */
export function readStick(pad: Gamepad, stickIdx: 0 | 1, deadzone: number): AxisVector {
	const axes = pad.axes;
	const xAxis = stickIdx === 0 ? 0 : 2;
	const yAxis = stickIdx === 0 ? 1 : 3;
	let x = axes[xAxis] ?? 0;
	let y = axes[yAxis] ?? 0;
	const mag = Math.hypot(x, y);
	if (mag < deadzone) return { vx: 0, vy: 0 };
	// Rescale so the deadzone band gets remapped to [0..1] linearly.
	// This avoids a sudden jump out of the deadzone at low magnitudes.
	const scaled = (mag - deadzone) / (1 - deadzone);
	x = (x / mag) * scaled;
	y = (y / mag) * scaled;
	return {
		vx: Math.max(-1000, Math.min(1000, Math.round(x * 1000))),
		vy: Math.max(-1000, Math.min(1000, Math.round(y * 1000))),
	};
}

function navigatorGetGamepads(): ReadonlyArray<Gamepad | null> {
	if (typeof navigator === "undefined" || typeof navigator.getGamepads !== "function") return [];
	return navigator.getGamepads();
}
