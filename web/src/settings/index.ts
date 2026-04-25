// Boxland — settings/ barrel.

export {
	DEFAULT_SETTINGS,
	FONT_OPTIONS,
	coerceSettings,
} from "./types";
export type {
	Settings,
	FontName,
	AudioLevels,
	SpectatorPrefs,
	HotkeyBindings,
} from "./types";

export {
	loadLocal,
	saveLocal,
	loadRemote,
	saveRemote,
	makeRemoteSaver,
	readCSRFToken,
	lsKey,
} from "./store";

export {
	applyFont,
	applySpectator,
	applyBindings,
	applyAudio,
	applyAll,
} from "./apply";
export type { VolumeApplier } from "./apply";

export { renderRebinderRows } from "./rebinder";

export { bootSettings } from "./entry-settings";
