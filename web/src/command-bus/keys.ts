// Boxland — keyboard combo normalization.
//
// Combos are strings like "Mod+Shift+Z". `Mod` portably resolves to Cmd on
// macOS and Ctrl elsewhere so the same binding works on both. Modifiers
// are emitted in canonical order (Mod, Ctrl, Alt, Shift) so equality is
// just string compare.

import type { KeyCombo } from "./types";

const MOD_ORDER = ["Mod", "Ctrl", "Alt", "Shift"] as const;

const isMac = (): boolean =>
	typeof navigator !== "undefined" &&
	/(Mac|iPhone|iPad|iPod)/i.test(navigator.platform || navigator.userAgent || "");

/**
 * Build the canonical KeyCombo string from a KeyboardEvent. Returns "" for
 * pure modifier presses (so callers can short-circuit on combos like
 * "Shift" alone).
 */
export function comboFromEvent(e: KeyboardEvent): KeyCombo {
	const parts: string[] = [];
	const mac = isMac();

	// Mod = Cmd on mac, Ctrl elsewhere.
	if ((mac && e.metaKey) || (!mac && e.ctrlKey)) parts.push("Mod");
	if (mac && e.ctrlKey) parts.push("Ctrl"); // distinct from Mod on mac
	if (e.altKey) parts.push("Alt");
	if (e.shiftKey) parts.push("Shift");

	const key = normalizeKey(e.key, e.code);
	if (!key) return "";
	if (MOD_ORDER.includes(key as (typeof MOD_ORDER)[number])) return "";

	parts.push(key);
	return parts.join("+");
}

/**
 * Canonicalize a hand-typed combo (used by the rebinder UI) into the same
 * format comboFromEvent emits. Trims whitespace, sorts modifiers, fixes
 * casing on common keys.
 */
export function canonicalizeCombo(input: string): KeyCombo {
	const tokens = input
		.split("+")
		.map((t) => t.trim())
		.filter(Boolean);
	if (tokens.length === 0) return "";

	const mods = new Set<string>();
	let key = "";
	for (const t of tokens) {
		const norm = normalizeToken(t);
		if (MOD_ORDER.includes(norm as (typeof MOD_ORDER)[number])) {
			mods.add(norm);
		} else {
			key = norm;
		}
	}
	if (!key) return "";
	const ordered = MOD_ORDER.filter((m) => mods.has(m));
	return [...ordered, key].join("+");
}

function normalizeToken(t: string): string {
	const lower = t.toLowerCase();
	switch (lower) {
		case "ctrl":
		case "control":
			return "Ctrl";
		case "alt":
		case "option":
		case "opt":
			return "Alt";
		case "shift":
			return "Shift";
		case "mod":
		case "cmd":
		case "command":
		case "meta":
		case "win":
		case "super":
			return "Mod";
		case "esc":
			return "Escape";
		case "del":
			return "Delete";
		case "ins":
			return "Insert";
		case "space":
			return "Space";
		case "return":
			return "Enter";
		default:
			return normalizeKey(t, "");
	}
}

/**
 * Normalize a single key to a stable label. Single printable keys are
 * upper-cased; named keys (Escape, Enter, …) keep their KeyboardEvent.key
 * spelling.
 */
function normalizeKey(eventKey: string, eventCode: string): string {
	if (!eventKey) return "";
	// Spacebar shows up as a single space in event.key; promote to "Space"
	// before the single-char fast-path so " " doesn't slip through.
	if (eventKey === " " || eventCode === "Space") return "Space";
	if (eventKey.length === 1) return eventKey.toUpperCase();
	return eventKey;
}
