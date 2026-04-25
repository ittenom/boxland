// Boxland — audio/types.ts
//
// Public types for the Web-Audio sound engine.

/** SoundCatalog hands the engine a URL per sound_id. The renderer's
 *  asset catalog could implement this directly, but keeping a
 *  separate interface lets tests stub URLs without standing up the
 *  full sprite catalog. */
export interface SoundCatalog {
	urlFor(soundId: number): string | undefined;
	/** Optional hint: "sfx" or "music". Music tracks bypass the SFX
	 *  positional pipeline and feed the music gain bus directly. */
	kindFor?(soundId: number): "sfx" | "music" | undefined;
}

/** Positional audio source. Pixel coords match the renderer's world
 *  units (sub-pixels, 1 px = 256 sub). */
export interface PositionalSound {
	soundId: number;
	hasPosition: boolean;
	x: number;
	y: number;
	/** 0..255 from the wire schema. The engine divides by 255. */
	volume: number;
	/** Cents from base; positive = up. The engine maps to playback rate. */
	pitch: number;
}

/** Camera reader for positional pan/falloff. Same shape the input
 *  module uses (re-declared here so audio doesn't depend on @input). */
export interface AudioCameraReader {
	cx(): number;
	cy(): number;
}

/** Gain bus levels (0..1 linear). Driven by the Settings page. */
export interface VolumeLevels {
	master: number;
	music: number;
	sfx: number;
}

/** Default falloff distances in world sub-pixels (PLAN.md uses
 *  1 px = 256 sub-units). At INNER all sounds play at full volume;
 *  past OUTER they're inaudible; linear falloff between. */
export const DEFAULT_FALLOFF_INNER_SUB = 4 * 32 * 256;   // 4 tiles
export const DEFAULT_FALLOFF_OUTER_SUB = 24 * 32 * 256;  // 24 tiles
