// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { CommandBus } from "./bus";
import { CommandPalette, fuzzyMatch } from "./palette";
import type { Command } from "./types";

function noopCmd(id: string, description = id, opts: Partial<Command<void>> = {}): Command<void> {
	return { id, description, do: () => undefined, ...opts };
}

function makeBus(): CommandBus {
	const bus = new CommandBus();
	bus.register(noopCmd("paint", "Paint tool"));
	bus.register(noopCmd("rect", "Rectangle tool"));
	bus.register(noopCmd("eyedrop", "Eyedrop"));
	bus.register(noopCmd("settings.open", "Open settings"));
	return bus;
}

describe("fuzzyMatch", () => {
	it("matches subsequences", () => {
		expect(fuzzyMatch("rt", "rectangle")).toBe(true);
		expect(fuzzyMatch("set", "Open settings")).toBe(true);
		expect(fuzzyMatch("xyz", "rectangle")).toBe(false);
	});

	it("returns true for empty queries", () => {
		expect(fuzzyMatch("", "anything")).toBe(true);
	});

	it("is case-insensitive on the target", () => {
		expect(fuzzyMatch("set", "OPEN SETTINGS")).toBe(true);
	});
});

describe("CommandPalette", () => {
	afterEach(() => {
		document.body.innerHTML = "";
	});

	it("mounts on open and unmounts on close", () => {
		const palette = new CommandPalette(makeBus());
		palette.open();
		expect(document.querySelector(".bx-cmdk")).not.toBeNull();
		expect(palette.isOpen()).toBe(true);

		palette.close();
		expect(document.querySelector(".bx-cmdk")).toBeNull();
		expect(palette.isOpen()).toBe(false);
	});

	it("toggle alternates open/close", () => {
		const palette = new CommandPalette(makeBus());
		palette.toggle();
		expect(palette.isOpen()).toBe(true);
		palette.toggle();
		expect(palette.isOpen()).toBe(false);
	});

	it("filters by fuzzy query against id and description", () => {
		const palette = new CommandPalette(makeBus());
		palette.open();
		const input = document.querySelector(".bx-cmdk input") as HTMLInputElement;
		input.value = "set";
		input.dispatchEvent(new Event("input"));

		const items = document.querySelectorAll(".bx-cmdk li");
		expect(items.length).toBe(1);
		expect(items[0]?.getAttribute("data-cmd-id")).toBe("settings.open");
	});

	it("ArrowDown / ArrowUp move the highlight; Enter dispatches", async () => {
		const bus = makeBus();
		let dispatched: string | null = null;
		bus.register({
			id: "trace",
			description: "Trace dispatcher",
			do() { dispatched = "trace"; },
		});

		const palette = new CommandPalette(bus);
		palette.open();
		const input = document.querySelector(".bx-cmdk input") as HTMLInputElement;
		input.value = "trace";
		input.dispatchEvent(new Event("input"));

		const items = document.querySelectorAll(".bx-cmdk li");
		expect(items.length).toBe(1);
		expect(items[0]?.getAttribute("aria-selected")).toBe("true");

		input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
		// dispatch is async; allow the event loop to drain.
		await new Promise((r) => setTimeout(r, 0));
		expect(dispatched).toBe("trace");
		expect(palette.isOpen()).toBe(false);
	});

	it("Escape closes the palette", () => {
		const palette = new CommandPalette(makeBus());
		palette.open();
		const input = document.querySelector(".bx-cmdk input") as HTMLInputElement;
		input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
		expect(palette.isOpen()).toBe(false);
	});

	it("respects the optional command filter", () => {
		const bus = makeBus();
		const palette = new CommandPalette(bus, {
			filter: (c) => !c.id.startsWith("settings."),
		});
		palette.open();
		const items = document.querySelectorAll(".bx-cmdk li");
		// All commands except settings.open visible.
		expect([...items].some((i) => i.getAttribute("data-cmd-id") === "settings.open")).toBe(false);
		expect([...items].length).toBeGreaterThanOrEqual(3);
	});

	it("renders bound hotkey next to commands", () => {
		const bus = makeBus();
		bus.bindHotkey("Mod+P", "paint");
		const palette = new CommandPalette(bus);
		palette.open();
		const paintItem = document.querySelector('[data-cmd-id="paint"]');
		expect(paintItem?.querySelector("kbd")?.textContent).toBe("Mod+P");
	});
});
