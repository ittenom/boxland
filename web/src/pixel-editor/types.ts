// Boxland — pixel editor types.
//
// The editor operates on a single in-memory ImageData buffer. Tools mutate
// the buffer through Command objects on the shared command bus so undo /
// redo work uniformly. The buffer is rendered to a Canvas2D each frame at
// integer scale; saving exports the buffer (not the displayed pixels).

export interface RGBA {
	r: number;
	g: number;
	b: number;
	a: number;
}

export const TRANSPARENT: RGBA = { r: 0, g: 0, b: 0, a: 0 };

/** A single pixel write captured for undo. */
export interface PixelEdit {
	x: number;
	y: number;
	prev: RGBA;
	next: RGBA;
}

/**
 * EditorState is the canonical state the editor renders from. Tools mutate
 * via mutators that issue Commands; nothing mutates state directly.
 */
export interface EditorState {
	width: number;          // canvas px (logical, not zoomed)
	height: number;
	color: RGBA;             // current paint color
	tool: ToolID;
	zoom: number;            // integer scale, 1..16
}

export type ToolID = "pencil" | "eraser" | "picker";
