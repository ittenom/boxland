// Boxland command bus public surface.
// Surfaces import from "@command-bus" via the tsconfig path alias.

export { CommandBus } from "./bus";
export type { BusOptions } from "./bus";
export type { Command, KeyCombo, UndoEntry } from "./types";
export { GamepadButton } from "./types";
export { canonicalizeCombo, comboFromEvent } from "./keys";
export {
	clearStoredBindings,
	loadBindings,
	saveBindings,
} from "./persistence";
export type { BindingsSnapshot } from "./persistence";
export { attachGamepad, attachKeyboard } from "./listeners";
export { CommandPalette, fuzzyMatch } from "./palette";
export type { PaletteOptions } from "./palette";
