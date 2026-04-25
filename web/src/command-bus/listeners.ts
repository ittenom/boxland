// Boxland — DOM + Gamepad listener wiring for a CommandBus.
//
// Optional layer; tests don't need it. Surfaces opt in by calling
// attachKeyboard / attachGamepad once at boot. Both return an unsubscribe
// callback for cleanup (HMR, surface teardown, etc.).

import type { CommandBus } from "./bus";

/**
 * Attach a keydown listener that routes through bus.handleKeyEvent.
 * The listener honors the "do not fire while typing" rule unless the
 * matched command opts in via `whileTyping: true`.
 */
export function attachKeyboard(
	bus: CommandBus,
	target: EventTarget = typeof window !== "undefined" ? window : ({} as EventTarget),
): () => void {
	const handler = async (raw: Event): Promise<void> => {
		const e = raw as KeyboardEvent;
		const fired = await bus.handleKeyEvent(e, isTextEditing(e.target));
		if (fired) e.preventDefault();
	};
	target.addEventListener("keydown", handler);
	return () => target.removeEventListener("keydown", handler);
}

/**
 * Poll the Gamepad API at requestAnimationFrame cadence. Detects rising
 * edges (newly-pressed buttons) and dispatches via bus.handleGamepadButton.
 *
 * Trade-off: we poll instead of listening for "gamepadbuttondown" because
 * the standard doesn't actually emit per-button events; only connect /
 * disconnect. The polling cost is negligible (1-2 µs per frame).
 */
export function attachGamepad(bus: CommandBus): () => void {
	if (typeof navigator === "undefined" || typeof navigator.getGamepads !== "function") {
		return () => undefined;
	}
	let stopped = false;
	const lastPressed = new Map<number, boolean[]>();

	const tick = (): void => {
		if (stopped) return;
		const pads = navigator.getGamepads?.() ?? [];
		for (const pad of pads) {
			if (!pad) continue;
			const prev = lastPressed.get(pad.index) ?? [];
			const curr = pad.buttons.map((b) => b.pressed);
			for (let i = 0; i < curr.length; i++) {
				if (curr[i] && !prev[i]) {
					void bus.handleGamepadButton(i);
				}
			}
			lastPressed.set(pad.index, curr);
		}
		requestAnimationFrame(tick);
	};
	requestAnimationFrame(tick);
	return () => { stopped = true; };
}

function isTextEditing(target: EventTarget | null): boolean {
	if (!(target instanceof HTMLElement)) return false;
	const tag = target.tagName;
	if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
	return target.isContentEditable;
}
