// Boxland — shared command bus types.
//
// Every interactive surface (pixel editor, Mapmaker, Sandbox, game input)
// expresses its actions as Command objects on a CommandBus. The bus
// provides:
//   * a typed undo / redo stack (for surfaces that opt into history)
//   * a rebindable hotkey registry
//   * a rebindable gamepad button registry
//   * the data backing the Cmd-K command palette (task #38)
//
// One bus per surface keeps undo histories isolated. Multiple buses can
// share a single keymap registry if a global hotkey scope is needed
// (e.g., "Esc closes any open modal"); for v1 each surface owns its own.

/**
 * A Command is the smallest action a user can take. It MUST be:
 *   - idempotent w.r.t. its own do/undo cycle
 *   - serializable in description (the palette shows `description`)
 *   - safe to call when its prerequisites aren't met (return false / no-op)
 *
 * Commands without an undo are non-undoable (e.g., "open settings"). The
 * bus simply skips pushing them onto the undo stack.
 */
export interface Command<TArg = void> {
	/** Stable id; used for hotkey/gamepad bindings and palette filtering. */
	readonly id: string;

	/** Human-readable label shown in palette + cheatsheet. */
	readonly description: string;

	/** Optional grouping, e.g. "Mapmaker > Tools" — used by the palette. */
	readonly category?: string;

	/**
	 * If true, the command may fire while a text input is focused. Defaults
	 * to false (the global Esc behavior in boot.js follows the same rule).
	 */
	readonly whileTyping?: boolean;

	/**
	 * Apply the command. Return false to indicate the command short-circuited
	 * (e.g., prerequisites not met) so the bus does not push it onto the
	 * undo stack.
	 */
	do(arg: TArg): boolean | void | Promise<boolean | void>;

	/** Reverse a previous do(arg). Omit on non-undoable commands. */
	undo?(arg: TArg): void | Promise<void>;
}

/**
 * UndoEntry captures one applied command with the argument it was given,
 * so undo() can be called with the matching value.
 */
export interface UndoEntry {
	command: Command<unknown>;
	arg: unknown;
}

/** Normalized key combo like "Mod+Shift+Z". See parseCombo / formatCombo. */
export type KeyCombo = string;

/**
 * Standard Gamepad API button indices we care about. Other surfaces can
 * still bind to raw integer indices; the named constants below are sugar.
 */
export enum GamepadButton {
	A = 0,
	B = 1,
	X = 2,
	Y = 3,
	LB = 4,
	RB = 5,
	LT = 6,
	RT = 7,
	Back = 8,
	Start = 9,
	LStick = 10,
	RStick = 11,
	DUp = 12,
	DDown = 13,
	DLeft = 14,
	DRight = 15,
	Home = 16,
}
