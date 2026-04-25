// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from "vitest";

import { CommandBus, type Command } from "@command-bus";
import { attachKeyboard, attachBlurSafety } from "./keyboard";

function makeBus(): { bus: CommandBus; held: Set<string>; combos: Set<string> } {
	const bus = new CommandBus();
	const held = new Set<string>();
	const combos = new Set<string>();
	const cmd: Command<void> = {
		id: "test.hold",
		description: "test hold",
		do:      () => { held.add("a"); combos.add("A"); },
		release: () => { held.delete("a"); combos.delete("A"); },
	};
	bus.register(cmd);
	bus.bindHotkey("A", "test.hold");
	return { bus, held, combos };
}

describe("keyboard pump", () => {
	beforeEach(() => {
		// Fresh window for each test so listeners don't leak.
	});

	it("fires press on keydown, release on keyup", async () => {
		const { bus, held } = makeBus();
		attachKeyboard(bus, window);
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "A" }));
		await Promise.resolve();
		expect(held.has("a")).toBe(true);
		window.dispatchEvent(new KeyboardEvent("keyup", { key: "A" }));
		await Promise.resolve();
		expect(held.has("a")).toBe(false);
	});

	it("drops OS auto-repeat keydown events so do() fires once", async () => {
		const { bus } = makeBus();
		let fired = 0;
		bus.unbindHotkey("A");
		bus.register({
			id: "test.norepeat",
			description: "norepeat",
			do: () => { fired++; },
		});
		bus.bindHotkey("B", "test.norepeat");
		attachKeyboard(bus, window);
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "B" }));
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "B", repeat: true }));
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "B", repeat: true }));
		await Promise.resolve();
		expect(fired).toBe(1);
	});

	it("does not fire while a text input is focused", async () => {
		const { bus, held } = makeBus();
		attachKeyboard(bus, window);
		const input = document.createElement("input");
		document.body.appendChild(input);
		input.focus();
		const ev = new KeyboardEvent("keydown", { key: "A", bubbles: true });
		Object.defineProperty(ev, "target", { value: input });
		input.dispatchEvent(ev);
		await Promise.resolve();
		expect(held.has("a")).toBe(false);
	});

	it("returned dispose unsubscribes both keydown and keyup", async () => {
		const { bus, held } = makeBus();
		const off = attachKeyboard(bus, window);
		off();
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "A" }));
		await Promise.resolve();
		expect(held.has("a")).toBe(false);
	});
});

describe("attachBlurSafety", () => {
	it("releases all held combos on blur", async () => {
		const { bus, combos } = makeBus();
		// Press A so the held set has it.
		await bus.handleKeyEvent(new KeyboardEvent("keydown", { key: "A" }), false);
		expect(combos.has("A")).toBe(true);
		attachBlurSafety(bus, () => Array.from(combos), window);
		window.dispatchEvent(new Event("blur"));
		await Promise.resolve(); await Promise.resolve();
		expect(combos.has("A")).toBe(false);
	});
});
