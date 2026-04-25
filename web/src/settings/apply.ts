// Boxland — settings/apply.ts
//
// Side-effecting layer that turns a Settings object into runtime state:
// font CSS variable, audio gain nodes, command-bus rebindings,
// spectator preference broadcast.
//
// Pure functions where possible; the only DOM touch is setProperty on
// :root for the font and on document for the spectator data attribute.

import type { CommandBus } from "@command-bus";

import type { Settings } from "./types";

/** Apply the font choice as a CSS custom property override. The base
 *  pixel.css declares `--bx-font: "C64esque", ...`; we override only
 *  the first family so the fallback chain is preserved. */
export function applyFont(font: string): void {
	if (typeof document === "undefined") return;
	document.documentElement.style.setProperty(
		"--bx-font",
		`"${font}", "C64esque", monospace`,
	);
}

/** Apply spectator preferences as data attributes the renderer reads
 *  on init. Avoids the renderer needing to import this module
 *  directly. */
export function applySpectator(freeCam: boolean): void {
	if (typeof document === "undefined") return;
	document.documentElement.dataset.bxSpectatorFreecam = freeCam ? "1" : "0";
}

/** Apply hotkey rebindings to the supplied bus. Only commands that
 *  exist in `bus.all()` are rebound; orphaned bindings (referencing a
 *  deleted command id) are silently dropped. The default bindings
 *  the surface installed remain in place EXCEPT where a user binding
 *  shadows them on the same combo. */
export function applyBindings(bus: CommandBus, bindings: Record<string, string>): void {
	const known = new Set(bus.all().map((c) => c.id));
	for (const [combo, cmdId] of Object.entries(bindings)) {
		if (!known.has(cmdId)) continue;
		// Drop any prior binding on this combo so the user's choice is
		// authoritative (re-binding "ArrowUp" away from move-up clears
		// the move binding rather than producing two simultaneous fires).
		bus.unbindHotkey(combo);
		try {
			bus.bindHotkey(combo, cmdId);
		} catch {
			/* invalid combo -- skip silently rather than break boot */
		}
	}
}

/** Volume application is done by the renderer's audio system (added in
 *  task #119). For now we expose a callback hook the renderer wires
 *  in. The settings module never imports a Web Audio context itself
 *  to keep its bundle tiny. */
export interface VolumeApplier {
	setMaster(linear01: number): void;
	setMusic(linear01: number): void;
	setSfx(linear01: number): void;
}

export function applyAudio(s: Settings, volume?: VolumeApplier): void {
	if (!volume) return;
	volume.setMaster(s.audio.master / 100);
	volume.setMusic(s.audio.music / 100);
	volume.setSfx(s.audio.sfx / 100);
}

/** One-call apply for all sections. Surfaces typically call this on
 *  boot AND every time the Settings object mutates. */
export function applyAll(
	s: Settings,
	opts: { bus?: CommandBus; volume?: VolumeApplier } = {},
): void {
	applyFont(s.font);
	applySpectator(s.spectator.freeCam);
	if (opts.bus) applyBindings(opts.bus, s.bindings);
	applyAudio(s, opts.volume);
}
