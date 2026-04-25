import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { CommandBus } from "./bus";
import { clearStoredBindings, loadBindings, saveBindings } from "./persistence";
import type { Command } from "./types";

// Tiny in-memory localStorage shim so the persistence tests work under
// the node test environment.
class MemoryStorage implements Storage {
	private store = new Map<string, string>();
	get length() { return this.store.size; }
	clear() { this.store.clear(); }
	getItem(k: string) { return this.store.get(k) ?? null; }
	key(i: number) { return [...this.store.keys()][i] ?? null; }
	removeItem(k: string) { this.store.delete(k); }
	setItem(k: string, v: string) { this.store.set(k, v); }
}

const KEY = "test-surface";

function makeBus(): CommandBus {
	const bus = new CommandBus();
	const cmd: Command<void> = { id: "paint", description: "paint", do: () => undefined, undo: () => undefined };
	const cmd2: Command<void> = { id: "save", description: "save", do: () => undefined };
	bus.register(cmd);
	bus.register(cmd2);
	return bus;
}

describe("persistence", () => {
	beforeEach(() => {
		(globalThis as unknown as { localStorage: Storage }).localStorage = new MemoryStorage();
	});
	afterEach(() => {
		clearStoredBindings(KEY);
	});

	it("save then load round-trips bindings", () => {
		const bus = makeBus();
		bus.bindHotkey("Ctrl+P", "paint");
		bus.bindGamepad(0, "save");
		const snap = saveBindings(bus, KEY);
		expect(snap.hotkeys).toEqual({ "Ctrl+P": "paint" });
		expect(snap.gamepad).toEqual({ "0": "save" });

		// Fresh bus, same commands, no bindings yet.
		const fresh = makeBus();
		const n = loadBindings(fresh, KEY);
		expect(n).toBe(2);
		expect(fresh.hotkeyFor("paint")).toBe("Ctrl+P");
		expect(fresh.gamepadBindings().get(0)).toBe("save");
	});

	it("silently drops bindings to commands no longer registered", () => {
		const bus = makeBus();
		bus.bindHotkey("Ctrl+P", "paint");
		saveBindings(bus, KEY);

		const fresh = new CommandBus(); // no commands registered at all
		const n = loadBindings(fresh, KEY);
		expect(n).toBe(0);
	});

	it("clearStoredBindings wipes the entry", () => {
		const bus = makeBus();
		bus.bindHotkey("Ctrl+P", "paint");
		saveBindings(bus, KEY);

		clearStoredBindings(KEY);
		const fresh = makeBus();
		const n = loadBindings(fresh, KEY);
		expect(n).toBe(0);
	});

	it("returns 0 when storage has no entry", () => {
		const fresh = makeBus();
		expect(loadBindings(fresh, "never-stored")).toBe(0);
	});
});
