import { describe, it, expect } from "vitest";

import { coerceSettings, DEFAULT_SETTINGS, FONT_OPTIONS } from "./types";

describe("coerceSettings", () => {
	it("returns a fresh DEFAULT_SETTINGS for non-objects", () => {
		expect(coerceSettings(null)).toEqual(DEFAULT_SETTINGS);
		expect(coerceSettings(undefined)).toEqual(DEFAULT_SETTINGS);
		expect(coerceSettings("oops")).toEqual(DEFAULT_SETTINGS);
		expect(coerceSettings([1, 2])).toEqual(DEFAULT_SETTINGS);
	});

	it("preserves valid font names", () => {
		const out = coerceSettings({ font: "Kubasta" });
		expect(out.font).toBe("Kubasta");
	});

	it("falls back to default font on unknown name", () => {
		const out = coerceSettings({ font: "ComicSans" });
		expect(out.font).toBe(DEFAULT_SETTINGS.font);
	});

	it("clamps audio levels to 0..100 and rounds", () => {
		const out = coerceSettings({ audio: { master: -5, music: 999, sfx: 42.7 } });
		expect(out.audio.master).toBe(0);
		expect(out.audio.music).toBe(100);
		expect(out.audio.sfx).toBe(43);
	});

	it("ignores non-number audio entries", () => {
		const out = coerceSettings({ audio: { master: "loud", music: null, sfx: 50 } });
		expect(out.audio.master).toBe(DEFAULT_SETTINGS.audio.master);
		expect(out.audio.music).toBe(DEFAULT_SETTINGS.audio.music);
		expect(out.audio.sfx).toBe(50);
	});

	it("coerces spectator.freeCam to a boolean", () => {
		expect(coerceSettings({ spectator: { freeCam: true } }).spectator.freeCam).toBe(true);
		expect(coerceSettings({ spectator: { freeCam: "yes" } }).spectator.freeCam).toBe(false);
	});

	it("filters bindings to string->string entries", () => {
		const out = coerceSettings({
			bindings: { "ArrowUp": "game.move.up", "Bad": 42, "": "x", "Ok": "" },
		});
		expect(out.bindings).toEqual({ "ArrowUp": "game.move.up" });
	});

	it("FONT_OPTIONS contains the five bundled fonts", () => {
		expect(FONT_OPTIONS).toEqual([
			"C64esque", "AtariGames", "BIOSfontII", "Kubasta", "TinyUnicode",
		]);
	});
});
