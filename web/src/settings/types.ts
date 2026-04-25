// Boxland — settings/types.ts
//
// User-preference shape. Versioned by `v` so future migrations can
// detect old payloads and coerce them. Server stores the JSON blob
// verbatim; the client owns the schema.

/** Stable list of bundled fonts (PLAN.md §1: "5 included fonts shipped
 *  in app bundle"). The picker offers exactly these. */
export const FONT_OPTIONS = [
	"C64esque",
	"AtariGames",
	"BIOSfontII",
	"Kubasta",
	"TinyUnicode",
] as const;
export type FontName = typeof FONT_OPTIONS[number];

/** Audio levels are stored as integer percents (0..100) so the JSON
 *  blob round-trips without float drift. The renderer divides by 100
 *  when feeding Web Audio gain nodes. */
export interface AudioLevels {
	master: number;
	music: number;
	sfx: number;
}

export interface SpectatorPrefs {
	freeCam: boolean;
}

/** Hotkey bindings: map of canonical-combo string -> command id.
 *  The CommandBus already speaks this shape via bus.bindings(). */
export type HotkeyBindings = Record<string, string>;

/** The full settings payload. */
export interface Settings {
	v: 1;
	font: FontName;
	audio: AudioLevels;
	spectator: SpectatorPrefs;
	bindings: HotkeyBindings;
}

/** Defaults used when no row exists yet (or when a field is missing
 *  from a migrated old payload). Match the server-side default-font
 *  decision (PLAN.md §1 default = C64esque). */
export const DEFAULT_SETTINGS: Settings = {
	v: 1,
	font: "C64esque",
	audio: { master: 80, music: 70, sfx: 90 },
	spectator: { freeCam: false },
	bindings: {},
};

/** Merge a partial / unknown payload onto DEFAULT_SETTINGS, coercing
 *  any fields that are missing or the wrong type. Used when hydrating
 *  from localStorage or from the server response so the rest of the
 *  client never has to guard against undefined fields. */
export function coerceSettings(input: unknown): Settings {
	if (!isObject(input)) return { ...DEFAULT_SETTINGS };
	const out: Settings = {
		v: 1,
		font: coerceFont(input["font"]),
		audio: coerceAudio(input["audio"]),
		spectator: coerceSpectator(input["spectator"]),
		bindings: coerceBindings(input["bindings"]),
	};
	return out;
}

function isObject(v: unknown): v is Record<string, unknown> {
	return typeof v === "object" && v !== null && !Array.isArray(v);
}

function coerceFont(v: unknown): FontName {
	if (typeof v === "string" && (FONT_OPTIONS as readonly string[]).includes(v)) {
		return v as FontName;
	}
	return DEFAULT_SETTINGS.font;
}

function coerceAudio(v: unknown): AudioLevels {
	if (!isObject(v)) return { ...DEFAULT_SETTINGS.audio };
	const pick = (k: keyof AudioLevels): number => {
		const raw = v[k];
		if (typeof raw !== "number" || !Number.isFinite(raw)) return DEFAULT_SETTINGS.audio[k];
		return Math.max(0, Math.min(100, Math.round(raw)));
	};
	return { master: pick("master"), music: pick("music"), sfx: pick("sfx") };
}

function coerceSpectator(v: unknown): SpectatorPrefs {
	if (!isObject(v)) return { ...DEFAULT_SETTINGS.spectator };
	return { freeCam: typeof v["freeCam"] === "boolean" ? v["freeCam"] : false };
}

function coerceBindings(v: unknown): HotkeyBindings {
	if (!isObject(v)) return {};
	const out: HotkeyBindings = {};
	for (const [k, raw] of Object.entries(v)) {
		if (typeof k === "string" && typeof raw === "string" && k && raw) {
			out[k] = raw;
		}
	}
	return out;
}
