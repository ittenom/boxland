// Boxland — game/ barrel.
//
// Public surface for the player game module. The Sandbox UI (task #131)
// re-uses GameLoop + the prediction module to drive its own viewport.

export { GameLoop } from "./loop";
export type { GameLoopOptions, RendererLike, HudLike, LoopScheduler } from "./loop";

export { PlaceholderCatalog } from "./catalog";

export {
	predictStep,
	reconcile,
	resolveHost,
	freshLocalState,
	HOST_SPEED_SUB_PER_MS,
	RECONCILE_HARD_SNAP_SUB,
} from "./prediction";

export {
	MovementIntent,
	buildMovementCommands,
	installMovementBindings,
} from "./intents";

export { mailboxAsWorld } from "./world";

export {
	GameCamera,
	buildCameraToggleCommand,
	CAMERA_TOGGLE_COMMAND_ID,
	CAMERA_PAN_SUB_PER_MS,
} from "./camera";
export type { CameraMode, CameraPos } from "./camera";

export type {
	GameBootConfig,
	LocalState,
	ReconcileResult,
	HostEntity,
} from "./types";

export { bootGame } from "./entry-game";
