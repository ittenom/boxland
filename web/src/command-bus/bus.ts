// Boxland — CommandBus core.
//
// Owns command registry + undo stack + hotkey bindings + gamepad bindings.
// Persistence (localStorage) and global keyboard listening are layered on
// top so the core stays test-friendly.

import { canonicalizeCombo, comboFromEvent } from "./keys";
import type { Command, GamepadButton, KeyCombo, UndoEntry } from "./types";

// On non-mac platforms, Mod and Ctrl point at the same physical key. Map
// one spelling to the other so a binding written either way matches.
function aliasOnNonMac(combo: KeyCombo): KeyCombo {
	if (typeof navigator === "undefined") return combo;
	const isMac = /(Mac|iPhone|iPad|iPod)/i.test(navigator.platform || navigator.userAgent || "");
	if (isMac) return combo;
	if (combo.startsWith("Mod+")) return "Ctrl+" + combo.slice(4);
	if (combo.startsWith("Ctrl+")) return "Mod+" + combo.slice(5);
	return combo;
}

export interface BusOptions {
	/** Max entries kept on the undo stack. Older entries fall off. */
	undoLimit?: number;
}

export class CommandBus {
	private readonly commands = new Map<string, Command<unknown>>();
	private readonly hotkeys = new Map<KeyCombo, string>();    // combo  -> command id
	private readonly gamepads = new Map<number, string>();     // button -> command id
	private readonly undoStack: UndoEntry[] = [];
	private readonly redoStack: UndoEntry[] = [];
	private readonly undoLimit: number;

	constructor(opts: BusOptions = {}) {
		this.undoLimit = Math.max(1, opts.undoLimit ?? 200);
	}

	// ---- Registration ----

	register<T>(cmd: Command<T>): void {
		if (this.commands.has(cmd.id)) {
			throw new Error(`CommandBus: duplicate command id ${cmd.id}`);
		}
		this.commands.set(cmd.id, cmd as Command<unknown>);
	}

	get(id: string): Command<unknown> | undefined {
		return this.commands.get(id);
	}

	all(): Command<unknown>[] {
		return [...this.commands.values()];
	}

	// ---- Hotkeys ----

	bindHotkey(combo: KeyCombo, commandId: string): void {
		const c = canonicalizeCombo(combo);
		if (!c) throw new Error(`CommandBus: invalid combo ${combo}`);
		if (!this.commands.has(commandId)) {
			throw new Error(`CommandBus: bindHotkey to unknown command ${commandId}`);
		}
		this.hotkeys.set(c, commandId);
	}

	unbindHotkey(combo: KeyCombo): void {
		this.hotkeys.delete(canonicalizeCombo(combo));
	}

	hotkeyFor(commandId: string): KeyCombo | undefined {
		for (const [combo, id] of this.hotkeys) {
			if (id === commandId) return combo;
		}
		return undefined;
	}

	bindings(): Map<KeyCombo, string> {
		// Defensive copy.
		return new Map(this.hotkeys);
	}

	// ---- Gamepad ----

	bindGamepad(button: GamepadButton | number, commandId: string): void {
		if (!this.commands.has(commandId)) {
			throw new Error(`CommandBus: bindGamepad to unknown command ${commandId}`);
		}
		this.gamepads.set(Number(button), commandId);
	}

	unbindGamepad(button: GamepadButton | number): void {
		this.gamepads.delete(Number(button));
	}

	gamepadBindings(): Map<number, string> {
		return new Map(this.gamepads);
	}

	// ---- Dispatch ----

	/**
	 * Dispatch a command by id. Returns true if the command ran AND was
	 * pushed onto the undo stack (i.e., undoable + did not short-circuit).
	 *
	 * Async commands resolve to true/false on completion. Synchronous
	 * commands return immediately.
	 */
	async dispatch<T>(commandId: string, arg: T): Promise<boolean> {
		const cmd = this.commands.get(commandId) as Command<T> | undefined;
		if (!cmd) return false;
		const result = await cmd.do(arg);
		if (result === false) return false;
		if (cmd.undo) {
			this.undoStack.push({ command: cmd as Command<unknown>, arg });
			if (this.undoStack.length > this.undoLimit) {
				this.undoStack.shift();
			}
			// Any new applied command invalidates the redo stack.
			this.redoStack.length = 0;
			return true;
		}
		return false;
	}

	/**
	 * Try to handle a KeyboardEvent. Returns true if a hotkey fired.
	 *
	 * On non-mac, `Ctrl+X` bindings and `Mod+X` bindings refer to the same
	 * physical key — so when the event maps to one, also probe the other
	 * spelling. On mac the two are distinct (Mod = Cmd, Ctrl = Ctrl) and
	 * we never alias them.
	 */
	async handleKeyEvent(e: KeyboardEvent, isTextEditing: boolean): Promise<boolean> {
		const combo = comboFromEvent(e);
		if (!combo) return false;
		const id = this.hotkeys.get(combo) ?? this.hotkeys.get(aliasOnNonMac(combo));
		if (!id) return false;
		const cmd = this.commands.get(id);
		if (!cmd) return false;
		if (isTextEditing && !cmd.whileTyping) return false;
		await this.dispatch(id, undefined);
		return true;
	}

	/** Try to handle a gamepad button-down. Returns true if a binding fired. */
	async handleGamepadButton(button: number): Promise<boolean> {
		const id = this.gamepads.get(button);
		if (!id) return false;
		await this.dispatch(id, undefined);
		return true;
	}

	// ---- Undo / Redo ----

	canUndo(): boolean { return this.undoStack.length > 0; }
	canRedo(): boolean { return this.redoStack.length > 0; }

	async undo(): Promise<boolean> {
		const entry = this.undoStack.pop();
		if (!entry) return false;
		await entry.command.undo!(entry.arg);
		this.redoStack.push(entry);
		return true;
	}

	async redo(): Promise<boolean> {
		const entry = this.redoStack.pop();
		if (!entry) return false;
		await entry.command.do(entry.arg);
		this.undoStack.push(entry);
		return true;
	}

	/** Clear undo + redo. Useful when loading a fresh document. */
	clearHistory(): void {
		this.undoStack.length = 0;
		this.redoStack.length = 0;
	}
}
