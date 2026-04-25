// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from "vitest";

import { CommandBus, type Command } from "@command-bus";
import { applyAll, applyBindings, applyFont, applySpectator } from "./apply";
import { DEFAULT_SETTINGS } from "./types";

beforeEach(() => {
	document.documentElement.style.removeProperty("--bx-font");
	delete document.documentElement.dataset.bxSpectatorFreecam;
});

describe("applyFont", () => {
	it("sets --bx-font on :root with fallback chain", () => {
		applyFont("Kubasta");
		const v = document.documentElement.style.getPropertyValue("--bx-font");
		expect(v).toContain("Kubasta");
		expect(v).toContain("C64esque");
	});
});

describe("applySpectator", () => {
	it("toggles the data attribute", () => {
		applySpectator(true);
		expect(document.documentElement.dataset.bxSpectatorFreecam).toBe("1");
		applySpectator(false);
		expect(document.documentElement.dataset.bxSpectatorFreecam).toBe("0");
	});
});

describe("applyBindings", () => {
	it("rebinds known commands and skips unknown ones", () => {
		const bus = new CommandBus();
		const cmd: Command<void> = { id: "test.fire", description: "fire", do: () => undefined };
		bus.register(cmd);
		bus.bindHotkey("X", "test.fire");
		applyBindings(bus, { "Y": "test.fire", "Z": "no.such.command" });
		expect(bus.bindings().get("Y")).toBe("test.fire");
		// "Z" was unknown -> skipped, no throw.
		expect(bus.bindings().get("Z")).toBeUndefined();
	});

	it("displaces a prior binding on the same combo", () => {
		const bus = new CommandBus();
		bus.register({ id: "a", description: "a", do: () => undefined });
		bus.register({ id: "b", description: "b", do: () => undefined });
		bus.bindHotkey("X", "a");
		applyBindings(bus, { "X": "b" });
		expect(bus.bindings().get("X")).toBe("b");
	});
});

describe("applyAll", () => {
	it("applies font + spectator + bindings in one call", () => {
		const bus = new CommandBus();
		bus.register({ id: "test.x", description: "x", do: () => undefined });
		applyAll({ ...DEFAULT_SETTINGS, font: "AtariGames",
			spectator: { freeCam: true },
			bindings: { "Q": "test.x" } }, { bus });
		const v = document.documentElement.style.getPropertyValue("--bx-font");
		expect(v).toContain("AtariGames");
		expect(document.documentElement.dataset.bxSpectatorFreecam).toBe("1");
		expect(bus.bindings().get("Q")).toBe("test.x");
	});
});
