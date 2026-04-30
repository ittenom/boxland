// Boxland — renderer public surface.
export { BoxlandApp } from "./app";
export type { BoxlandAppOptions } from "./app";
export { Scene } from "./scene";
export type { SceneOptions } from "./scene";
export { TextureCache } from "./textures";
export { loadTextureAsset } from "./asset-texture";
export { LightingLayer } from "./lighting";
export type { LightingCell, LightingOptions } from "./lighting";
export {
	NameplateLayer, NO_HP_BAR, shouldShow, barWidth, drawHpBar,
	DEFAULT_NAMEPLATE_FONT_PX,
	NAMEPLATE_OFFSET_PX, HP_BAR_WIDTH_PX, HP_BAR_HEIGHT_PX, HP_BAR_OFFSET_PX,
} from "./nameplates";
export type { NameplateOptions } from "./nameplates";
export { DebugOverlay } from "./debug";
export type { DebugOptions } from "./debug";
export { computeLayout, worldToScreen } from "./viewport";
export type { ViewportLayout, ViewportPx } from "./viewport";
export { StaticAssetCatalog } from "./static-catalog";
export type { StaticAssetCatalogOptions, StaticCatalogEntry } from "./static-catalog";
export { EditorHarness } from "./editor-harness";
export type { EditorHarnessOptions, FrameScheduler } from "./editor-harness";

// UI primitives layer (theme + 9-slice + widgets). See ./ui.
export {
	Theme, Roles, NineSlice, Surface, pixiUITokens, surfacePalette,
	bindThemeToTextureCache,
} from "./ui";
export type {
	Role, ThemeEntry, NineSliceInsets, NineSliceOptions,
	SurfaceOptions, PixiUITokens, SurfaceTone, SurfacePalette,
} from "./ui";

// Editor harness (Pixi-rendered editor scenes). See ./editors.
export {
	EditorApp, Toolbar, Statusbar, ModalManager,
	PaletteGrid, Inspector, EditorWire,
	buildEditorLayout, resizeEditorLayout,
} from "./editors";
export type {
	EditorAppOptions, EditorSlots,
	ToolbarOptions, StatusbarOptions, ModalManagerOptions,
	PaletteGridOptions, PaletteEntry,
	InspectorOptions, EditorWireOptions, DiffHandler,
	EditorKind, EditorSnapshot as EditorSnapshotProps, ViewportDims,
	ToolbarAction, StatusbarSlot, ModalSpec, ModalButton,
	FieldDescriptor, FieldKind, EnumOption,
} from "./editors";
export type {
	AnimationFrame,
	AssetCatalog,
	AssetId,
	AnimId,
	Camera,
	EntityId,
	Renderable,
} from "./types";
export { SUB_PER_PX } from "./types";
