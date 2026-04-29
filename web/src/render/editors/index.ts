// Boxland — editor harness public surface.
//
// Surface entry scripts (mapmaker, level-editor) build on top of
// EditorApp + the harness widgets in this package.

export { EditorApp } from "./editor-app";
export type { EditorAppOptions } from "./editor-app";
export {
	buildEditorLayout,
	resizeEditorLayout,
	type EditorSlots,
} from "./editor-layout";
export { Toolbar } from "./toolbar";
export type { ToolbarOptions } from "./toolbar";
export { Statusbar } from "./statusbar";
export type { StatusbarOptions } from "./statusbar";
export { ModalManager } from "./modal";
export type { ModalManagerOptions } from "./modal";
export { PaletteGrid } from "./palette-grid";
export type { PaletteGridOptions, PaletteEntry } from "./palette-grid";
export { Inspector } from "./inspector";
export type { InspectorOptions } from "./inspector";
export type {
	EditorKind, EditorSnapshot, ViewportDims,
	ToolbarAction, StatusbarSlot, ModalSpec, ModalButton,
} from "./types";
export type { FieldDescriptor, FieldKind, EnumOption } from "./field-descriptor";
export { EditorWire } from "./editor-wire";
export type { EditorWireOptions, DiffHandler } from "./editor-wire";
