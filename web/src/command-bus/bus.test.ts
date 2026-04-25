// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CommandBus } from "./bus";
import type { Command } from "./types";

// Track invocations for verification across tests.
function counterCommand(id: string, opts: Partial<Command<number>> = {}): {
	cmd: Command<number>;
	state: { applied: number; undone: number; lastArg?: number };
} {
	const state = { applied: 0, undone: 0 } as { applied: number; undone: number; lastArg?: number };
	const cmd: Command<number> = {
		id,
		description: id,
		do(arg: number) {
			state.applied++;
			state.lastArg = arg;
		},
		undo(arg: number) {
			state.undone++;
			state.lastArg = arg;
		},
		...opts,
	};
	return { cmd, state };
}

describe("CommandBus", () => {
	let bus: CommandBus;

	beforeEach(() => {
		bus = new CommandBus();
	});

	it("rejects duplicate registrations", () => {
		const { cmd } = counterCommand("dup");
		bus.register(cmd);
		expect(() => bus.register(cmd)).toThrow(/duplicate/);
	});

	it("dispatch invokes the command and pushes onto the undo stack", async () => {
		const { cmd, state } = counterCommand("paint");
		bus.register(cmd);

		const ok = await bus.dispatch("paint", 7);
		expect(ok).toBe(true);
		expect(state.applied).toBe(1);
		expect(state.lastArg).toBe(7);
		expect(bus.canUndo()).toBe(true);
	});

	it("dispatch returns false when do() returns false", async () => {
		const cmd: Command<void> = {
			id: "noop",
			description: "noop",
			do: () => false,
			undo: () => undefined,
		};
		bus.register(cmd);
		const ok = await bus.dispatch("noop", undefined);
		expect(ok).toBe(false);
		expect(bus.canUndo()).toBe(false);
	});

	it("undo / redo round-trips and preserves arguments", async () => {
		const { cmd, state } = counterCommand("paint");
		bus.register(cmd);
		await bus.dispatch("paint", 11);

		await bus.undo();
		expect(state.undone).toBe(1);
		expect(state.lastArg).toBe(11);
		expect(bus.canRedo()).toBe(true);

		await bus.redo();
		expect(state.applied).toBe(2);
		expect(bus.canRedo()).toBe(false);
	});

	it("non-undoable commands do not push onto the undo stack", async () => {
		const cmd: Command<void> = {
			id: "open-settings",
			description: "Open settings",
			do: vi.fn(),
		};
		bus.register(cmd);
		await bus.dispatch("open-settings", undefined);
		expect(bus.canUndo()).toBe(false);
	});

	it("dispatching invalidates the redo stack", async () => {
		const { cmd } = counterCommand("paint");
		bus.register(cmd);
		await bus.dispatch("paint", 1);
		await bus.undo();
		expect(bus.canRedo()).toBe(true);

		await bus.dispatch("paint", 2);
		expect(bus.canRedo()).toBe(false);
	});

	it("trims the undo stack to undoLimit", async () => {
		const limit = 3;
		const small = new CommandBus({ undoLimit: limit });
		const { cmd } = counterCommand("paint");
		small.register(cmd);
		for (let i = 0; i < 10; i++) await small.dispatch("paint", i);

		// Undo limit means only the last `limit` are recoverable.
		let undone = 0;
		while (await small.undo()) undone++;
		expect(undone).toBe(limit);
	});
});

describe("CommandBus hotkeys", () => {
	let bus: CommandBus;

	beforeEach(() => {
		bus = new CommandBus();
		const { cmd } = counterCommand("paint");
		bus.register(cmd);
	});

	it("rejects binding to unknown commands", () => {
		expect(() => bus.bindHotkey("Mod+P", "ghost")).toThrow(/unknown command/);
	});

	it("hotkeyFor returns the bound combo", () => {
		bus.bindHotkey("ctrl+p", "paint");
		expect(bus.hotkeyFor("paint")).toBe("Ctrl+P");
	});

	it("handleKeyEvent fires the bound command (Ctrl/Mod alias on non-mac)", async () => {
		bus.bindHotkey("Ctrl+P", "paint");
		Object.defineProperty(navigator, "platform", { value: "Win32", configurable: true });
		const e = new KeyboardEvent("keydown", { key: "p", code: "KeyP", ctrlKey: true });
		const fired = await bus.handleKeyEvent(e, false);
		expect(fired).toBe(true);
		expect(bus.canUndo()).toBe(true);
	});

	it("suppresses hotkeys while a text field has focus by default", async () => {
		bus.bindHotkey("Ctrl+P", "paint");
		Object.defineProperty(navigator, "platform", { value: "Win32", configurable: true });
		const e = new KeyboardEvent("keydown", { key: "p", code: "KeyP", ctrlKey: true });
		const fired = await bus.handleKeyEvent(e, true);
		expect(fired).toBe(false);
	});

	it("allows whileTyping commands while a text field has focus", async () => {
		const cmd: Command<void> = {
			id: "save",
			description: "save",
			whileTyping: true,
			do: () => undefined,
			undo: () => undefined,
		};
		bus.register(cmd);
		bus.bindHotkey("Mod+S", "save");
		Object.defineProperty(navigator, "platform", { value: "MacIntel", configurable: true });
		const e = new KeyboardEvent("keydown", { key: "s", code: "KeyS", metaKey: true });
		expect(await bus.handleKeyEvent(e, true)).toBe(true);
	});
});

describe("CommandBus gamepad", () => {
	it("dispatches the bound command on rising-edge button press", async () => {
		const bus = new CommandBus();
		const { cmd, state } = counterCommand("a-button");
		bus.register(cmd);
		bus.bindGamepad(0, "a-button");

		const fired = await bus.handleGamepadButton(0);
		expect(fired).toBe(true);
		expect(state.applied).toBe(1);
	});

	it("returns false for unbound buttons", async () => {
		const bus = new CommandBus();
		expect(await bus.handleGamepadButton(15)).toBe(false);
	});
});
