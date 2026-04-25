// Boxland — input/keyboard.ts
//
// Keyboard pump. Listens to keydown + keyup on the host element and
// dispatches them through the bus. Keydown calls `handleKeyEvent`
// (the existing press path); keyup calls `handleKeyRelease` so hold
// commands like Move see both edges from a single binding.
//
// PLAN.md §6h "every input is a Command on the shared bus, rebindable
// via Settings". The pump never decides what a key does -- only the
// bus' bindings do. Settings rebinds, the pump still works.

import type { CommandBus } from "@command-bus";

/**
 * Attach keydown + keyup listeners that route through `bus.handleKeyEvent`
 * and `bus.handleKeyRelease`. Returns an unsubscribe callback for HMR /
 * surface teardown.
 *
 * `target` defaults to `window`. Surfaces that scope input to a single
 * focusable element (e.g. the game canvas) pass that element instead so
 * keys never fire while a modal is open.
 *
 * `repeat` events from the OS auto-repeat machinery are dropped -- they
 * would otherwise fire `do` over and over while a key is held, breaking
 * the "press once, hold while pressed" semantics hold-commands rely on.
 */
export function attachKeyboard(
	bus: CommandBus,
	target: EventTarget = typeof window !== "undefined" ? window : ({} as EventTarget),
): () => void {
	const onKeyDown = async (raw: Event): Promise<void> => {
		const e = raw as KeyboardEvent;
		if (e.repeat) return;
		const fired = await bus.handleKeyEvent(e, isTextEditing(e.target));
		if (fired) e.preventDefault();
	};
	const onKeyUp = async (raw: Event): Promise<void> => {
		const e = raw as KeyboardEvent;
		const fired = await bus.handleKeyRelease(e, isTextEditing(e.target));
		if (fired) e.preventDefault();
	};
	target.addEventListener("keydown", onKeyDown);
	target.addEventListener("keyup", onKeyUp);
	return () => {
		target.removeEventListener("keydown", onKeyDown);
		target.removeEventListener("keyup", onKeyUp);
	};
}

/** Page-blur safety: when the window loses focus, fire `release` on
 *  every currently-bound hold command so a player who alt-tabs while
 *  holding D doesn't keep walking forever. The pump tracks which
 *  combos are currently held and emits releases on blur. */
export function attachBlurSafety(
	bus: CommandBus,
	heldCombos: () => Iterable<string>,
	target: EventTarget = typeof window !== "undefined" ? window : ({} as EventTarget),
): () => void {
	const handler = async (): Promise<void> => {
		for (const combo of heldCombos()) {
			// Synthesize a KeyboardEvent shaped enough that
			// bus.handleKeyRelease can recover the combo.
			const e = synthKeyEvent(combo);
			if (e) await bus.handleKeyRelease(e, false);
		}
	};
	target.addEventListener("blur", handler);
	return () => target.removeEventListener("blur", handler);
}

function isTextEditing(target: EventTarget | null): boolean {
	if (!(target instanceof HTMLElement)) return false;
	const tag = target.tagName;
	if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
	return target.isContentEditable;
}

/** Synthesize a KeyboardEvent the bus can decode back to the combo.
 *  Used by attachBlurSafety to release everything cleanly without
 *  knowing which physical key was held. */
function synthKeyEvent(combo: string): KeyboardEvent | null {
	if (typeof KeyboardEvent === "undefined") return null;
	// Combo format: "Mod+Shift+Key"; we just need `key` set to the last
	// segment because comboFromEvent recovers modifier flags from event.
	const parts = combo.split("+");
	const key = parts[parts.length - 1] ?? "";
	if (!key) return null;
	return new KeyboardEvent("keyup", {
		key,
		shiftKey: parts.includes("Shift"),
		ctrlKey: parts.includes("Ctrl"),
		altKey: parts.includes("Alt"),
		metaKey: parts.includes("Mod") || parts.includes("Meta"),
	});
}
