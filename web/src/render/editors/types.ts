// Boxland — editor harness types.
//
// Shared interfaces between editor-app, layout, toolbar, statusbar,
// and modal. Pure types — keep them dependency-free so the rest of
// the harness can import without dragging Pixi in.

import type { Theme } from "../ui";

/** Editor surface ids. The ws layer (server-side) routes
 *  EditorJoin{Mapmaker,LevelEditor} opcodes by this enum. */
export type EditorKind = "mapmaker" | "level-editor";

/** Snapshot the server hands the client on join. The shape is
 *  surface-specific in the body (tiles for mapmaker, placements
 *  for level editor) but the harness slots are common. */
export interface EditorSnapshot {
	kind: EditorKind;
	title: string;
	theme: Theme;
	/** Surface-specific payload. Mapmaker = tiles + locks + layers;
	 *  level editor = placements + map dims. The harness doesn't
	 *  inspect this; the surface-specific entry script does. */
	body: unknown;
}

/** Layout dimensions in viewport pixels (logical, integer-scaled
 *  by the renderer). */
export interface ViewportDims {
	width: number;
	height: number;
}

/** One toolbar action — translates a button click into something
 *  the surface-specific entry script handles (tool change, undo,
 *  push-to-live, …). */
export interface ToolbarAction {
	id: string;
	label: string;
	hotkey?: string;
	/** Render the button as the active tool. Reflects state, not
	 *  intent — the toolbar's own state lives in the entry script
	 *  which calls `setActive(id)` on changes. */
	active?: boolean;
	/** Render in disabled style (greyed out, no input). */
	disabled?: boolean;
}

/** Statusbar slot ids — surface-specific entries. The harness's
 *  Statusbar widget renders these in order, left-to-right. */
export interface StatusbarSlot {
	id: string;
	text: string;
	/** Optional color for the text (hex 0xRRGGBB). Useful for
	 *  warning states ("saving..." → orange, error → red). */
	color?: number;
}

/** Modal config — drawn over the entire scene with a focus-trapped
 *  panel. The body is provided by the caller as a Pixi Container
 *  so the modal stays composable. */
export interface ModalSpec {
	id: string;
	title: string;
	/** Pixi Container that renders the modal body. Sized by
	 *  the modal layer; the caller doesn't need to position. */
	body: import("pixi.js").Container;
	/** Buttons in the modal footer; left-to-right. The first
	 *  button gets focus on open. */
	buttons: ModalButton[];
	/** Width hint (px). Default 480. The modal centers itself. */
	width?: number;
	/** Height hint (px). Default: auto (header + body height + footer). */
	height?: number;
	/** Called when the modal is dismissed by Esc / clicking
	 *  outside. The harness still calls onClose if a button's
	 *  closesModal=true is clicked. */
	onClose?: () => void;
}

export interface ModalButton {
	id: string;
	label: string;
	primary?: boolean;
	disabled?: boolean;
	closesModal?: boolean;
	onPress?: () => void | Promise<void>;
}
