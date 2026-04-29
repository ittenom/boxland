// Boxland — level editor public surface.
//
// Entry: `entry-level-editor.ts` (auto-runs when body[data-surface=
// "level-editor-entities"]). Pure modules below are exported so a
// future surface dispatcher or test harness can call them directly.

export { bootLevelEditor } from "./entry-level-editor";
export { EditorState } from "./state";
export { LevelOps } from "./ops";
export { LevelEditorWire } from "./wire";
export { WSPlacementWire } from "./ws-wire";
export { buildRenderables, defaultCamera, TILE_SUB_PX } from "./render-bridge";
export {
	handlePointerDown,
	handlePointerMove,
	handlePointerUp,
	rotate,
} from "./tools";
export type {
	BackdropTile,
	Cell,
	LevelEditorBoot,
	PaletteAtlasEntry,
	Placement,
	PlacementWire,
	Tool,
} from "./types";
