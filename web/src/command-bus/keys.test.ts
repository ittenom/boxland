// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { canonicalizeCombo, comboFromEvent } from "./keys";

describe("canonicalizeCombo", () => {
	it("preserves a single printable key", () => {
		expect(canonicalizeCombo("a")).toBe("A");
		expect(canonicalizeCombo("Z")).toBe("Z");
	});

	it("orders modifiers Mod -> Ctrl -> Alt -> Shift", () => {
		expect(canonicalizeCombo("Shift+Alt+Mod+K")).toBe("Mod+Alt+Shift+K");
	});

	it("normalizes synonyms", () => {
		expect(canonicalizeCombo("cmd+k")).toBe("Mod+K");
		expect(canonicalizeCombo("ctrl+alt+del")).toBe("Ctrl+Alt+Delete");
		expect(canonicalizeCombo("space")).toBe("Space");
		expect(canonicalizeCombo("esc")).toBe("Escape");
	});

	it("returns empty for invalid input", () => {
		expect(canonicalizeCombo("")).toBe("");
		expect(canonicalizeCombo("Mod+Shift+")).toBe("");
	});
});

describe("comboFromEvent", () => {
	it("emits modifier+key in canonical order on non-mac", () => {
		// Force non-mac path by stubbing navigator.
		Object.defineProperty(navigator, "platform", { value: "Win32", configurable: true });
		const e = new KeyboardEvent("keydown", { key: "z", code: "KeyZ", ctrlKey: true, shiftKey: true });
		expect(comboFromEvent(e)).toBe("Mod+Shift+Z");
	});

	it("treats Cmd as Mod on mac", () => {
		Object.defineProperty(navigator, "platform", { value: "MacIntel", configurable: true });
		const e = new KeyboardEvent("keydown", { key: "k", code: "KeyK", metaKey: true });
		expect(comboFromEvent(e)).toBe("Mod+K");
	});

	it("returns empty for pure modifier presses", () => {
		const e = new KeyboardEvent("keydown", { key: "Shift", code: "ShiftLeft", shiftKey: true });
		expect(comboFromEvent(e)).toBe("");
	});

	it("normalizes Space", () => {
		const e = new KeyboardEvent("keydown", { key: " ", code: "Space" });
		expect(comboFromEvent(e)).toBe("Space");
	});
});
