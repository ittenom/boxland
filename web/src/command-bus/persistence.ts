// Boxland — hotkey persistence to localStorage.
//
// Optional layer over CommandBus. Stores per-bus hotkey + gamepad bindings
// under stable keys so user rebindings survive reloads. Initial defaults
// come from the surface code; this layer only stores user *overrides*.

import type { CommandBus } from "./bus";
import { canonicalizeCombo } from "./keys";

const STORAGE_PREFIX = "bx.bindings.v1.";

export interface BindingsSnapshot {
	hotkeys: Record<string, string>;  // combo -> commandId
	gamepad: Record<number, string>;  // button -> commandId
}

/**
 * Save the bus's current bindings under the given storage key. Idempotent.
 * Returns the snapshot that was stored (handy for tests).
 */
export function saveBindings(bus: CommandBus, storageKey: string): BindingsSnapshot {
	const snap: BindingsSnapshot = {
		hotkeys: Object.fromEntries(bus.bindings()),
		gamepad: Object.fromEntries(bus.gamepadBindings()),
	};
	if (typeof localStorage !== "undefined") {
		localStorage.setItem(STORAGE_PREFIX + storageKey, JSON.stringify(snap));
	}
	return snap;
}

/**
 * Apply bindings from localStorage onto the bus. Unknown command ids are
 * silently dropped (e.g., after a feature was removed). Returns the number
 * of bindings applied.
 */
export function loadBindings(bus: CommandBus, storageKey: string): number {
	if (typeof localStorage === "undefined") return 0;
	const raw = localStorage.getItem(STORAGE_PREFIX + storageKey);
	if (!raw) return 0;

	let snap: BindingsSnapshot;
	try {
		snap = JSON.parse(raw) as BindingsSnapshot;
	} catch {
		return 0;
	}

	let count = 0;
	for (const [combo, id] of Object.entries(snap.hotkeys ?? {})) {
		if (bus.get(id)) {
			try {
				bus.bindHotkey(canonicalizeCombo(combo), id);
				count++;
			} catch {
				// invalid combo string in storage; skip silently.
			}
		}
	}
	for (const [btn, id] of Object.entries(snap.gamepad ?? {})) {
		if (bus.get(id)) {
			bus.bindGamepad(Number(btn), id);
			count++;
		}
	}
	return count;
}

/** Wipe stored bindings for a key. Used by a "reset to defaults" UI. */
export function clearStoredBindings(storageKey: string): void {
	if (typeof localStorage === "undefined") return;
	localStorage.removeItem(STORAGE_PREFIX + storageKey);
}
