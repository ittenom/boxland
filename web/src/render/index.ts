// Boxland — renderer public surface.
export { BoxlandApp } from "./app";
export type { BoxlandAppOptions } from "./app";
export { Scene } from "./scene";
export type { SceneOptions } from "./scene";
export { TextureCache } from "./textures";
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
