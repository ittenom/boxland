// Boxland — audio/engine.ts
//
// Web Audio integration. One AudioContext per page (lazily created on
// first user gesture so browsers don't block autoplay). Three gain
// buses: master, music, sfx. Positional sounds run sfx -> StereoPanner
// -> distance gain -> sfx bus.
//
// PLAN.md §6h, §5g audio defaults: master/music/sfx levels arrive
// from the Settings page via the VolumeApplier hook -- the engine
// implements that interface so settings.applyAudio drops directly
// into our gain nodes.

import { distanceGain, pan, pitchToRate, DEFAULT_FALLOFF, type FalloffConfig } from "./falloff";
import type {
	AudioCameraReader,
	PositionalSound,
	SoundCatalog,
	VolumeLevels,
} from "./types";

export interface SoundEngineOptions {
	catalog: SoundCatalog;
	camera?: AudioCameraReader;
	falloff?: FalloffConfig;
	/** Initial volume levels (0..1). Settings hydrate these later. */
	initialVolume?: Partial<VolumeLevels>;
	/** Override the AudioContext factory (tests). */
	audioCtxFactory?: () => AudioContext;
	/** Override fetch + decode (tests). */
	fetcher?: (url: string, ctx: AudioContext) => Promise<AudioBuffer>;
}

/**
 * SoundEngine: production glue between AudioEvents (drained from the
 * Mailbox each frame) and Web Audio. Lazily constructs the
 * AudioContext on first user gesture; until then `play()` calls are
 * silently no-op'd so we don't trip Chrome's autoplay-block warning.
 *
 * Implements VolumeApplier (settings/apply.ts) so the Settings page
 * can drive the gain buses directly with no extra glue.
 */
export class SoundEngine {
	readonly catalog: SoundCatalog;
	camera: AudioCameraReader | undefined;
	private readonly falloff: FalloffConfig;
	private readonly factory: () => AudioContext;
	private readonly fetcher: (url: string, ctx: AudioContext) => Promise<AudioBuffer>;

	private ctx: AudioContext | null = null;
	private masterGain: GainNode | null = null;
	private musicGain: GainNode | null = null;
	private sfxGain: GainNode | null = null;

	private masterLevel = 1;
	private musicLevel = 1;
	private sfxLevel = 1;

	private readonly buffers = new Map<number, AudioBuffer>();
	private readonly inflight = new Map<number, Promise<AudioBuffer | null>>();

	constructor(opts: SoundEngineOptions) {
		this.catalog = opts.catalog;
		this.camera = opts.camera;
		this.falloff = opts.falloff ?? DEFAULT_FALLOFF;
		this.factory = opts.audioCtxFactory ?? defaultAudioCtxFactory;
		this.fetcher = opts.fetcher ?? defaultFetcher;
		if (opts.initialVolume?.master !== undefined) this.masterLevel = clamp01(opts.initialVolume.master);
		if (opts.initialVolume?.music  !== undefined) this.musicLevel  = clamp01(opts.initialVolume.music);
		if (opts.initialVolume?.sfx    !== undefined) this.sfxLevel    = clamp01(opts.initialVolume.sfx);
	}

	// ---- VolumeApplier ----

	setMaster(linear01: number): void {
		this.masterLevel = clamp01(linear01);
		if (this.masterGain) this.masterGain.gain.value = this.masterLevel;
	}
	setMusic(linear01: number): void {
		this.musicLevel = clamp01(linear01);
		if (this.musicGain) this.musicGain.gain.value = this.musicLevel;
	}
	setSfx(linear01: number): void {
		this.sfxLevel = clamp01(linear01);
		if (this.sfxGain) this.sfxGain.gain.value = this.sfxLevel;
	}

	/** Read-only snapshot of the gain buses. */
	levels(): VolumeLevels {
		return { master: this.masterLevel, music: this.musicLevel, sfx: this.sfxLevel };
	}

	// ---- Lifecycle ----

	/** Lazily build the AudioContext on the first user gesture (the
	 *  game.click-to-move command would call this). Browsers block
	 *  audio creation before a gesture; we honor that by deferring. */
	resume(): void {
		this.ensureContext();
		if (this.ctx?.state === "suspended") {
			void this.ctx.resume();
		}
	}

	private ensureContext(): AudioContext | null {
		if (this.ctx) return this.ctx;
		try {
			this.ctx = this.factory();
		} catch {
			return null;
		}
		this.masterGain = this.ctx.createGain();
		this.musicGain  = this.ctx.createGain();
		this.sfxGain    = this.ctx.createGain();
		this.masterGain.gain.value = this.masterLevel;
		this.musicGain.gain.value  = this.musicLevel;
		this.sfxGain.gain.value    = this.sfxLevel;
		this.musicGain.connect(this.masterGain);
		this.sfxGain.connect(this.masterGain);
		this.masterGain.connect(this.ctx.destination);
		return this.ctx;
	}

	// ---- Playback ----

	/** Play one event. Loads the buffer on first request (cached for
	 *  reuse). Pre-load failures are silent so a missing asset doesn't
	 *  break gameplay. */
	play(ev: PositionalSound): void {
		const ctx = this.ensureContext();
		if (!ctx || !this.sfxGain) return;
		const url = this.catalog.urlFor(ev.soundId);
		if (!url) return;
		const kind = this.catalog.kindFor?.(ev.soundId) ?? "sfx";
		const buf = this.buffers.get(ev.soundId);
		if (!buf) {
			void this.preload(ev.soundId).then((b) => {
				if (b) this.actuallyPlay(ev, b, kind);
			});
			return;
		}
		this.actuallyPlay(ev, buf, kind);
	}

	/** Drain a frame's worth of events. Convenience for the loop. */
	playMany(events: Iterable<PositionalSound>): void {
		for (const ev of events) this.play(ev);
	}

	/** Ensure the buffer for `soundId` is decoded + cached. Subsequent
	 *  play() calls are immediate. */
	async preload(soundId: number): Promise<AudioBuffer | null> {
		const ctx = this.ensureContext();
		if (!ctx) return null;
		const cached = this.buffers.get(soundId);
		if (cached) return cached;
		const inflight = this.inflight.get(soundId);
		if (inflight) return inflight;
		const url = this.catalog.urlFor(soundId);
		if (!url) return null;
		const p = this.fetcher(url, ctx).then((buf) => {
			this.buffers.set(soundId, buf);
			this.inflight.delete(soundId);
			return buf;
		}).catch(() => {
			this.inflight.delete(soundId);
			return null;
		});
		this.inflight.set(soundId, p);
		return p;
	}

	// ---- Internals ----

	private actuallyPlay(ev: PositionalSound, buf: AudioBuffer, kind: "sfx" | "music"): void {
		const ctx = this.ctx;
		if (!ctx) return;
		const target = kind === "music" ? this.musicGain : this.sfxGain;
		if (!target) return;

		const source = ctx.createBufferSource();
		source.buffer = buf;
		source.playbackRate.value = pitchToRate(ev.pitch);

		// Per-event gain so we can attenuate by distance + per-event
		// volume without re-touching the bus.
		const eventGain = ctx.createGain();
		const baseVol = clamp01(ev.volume / 255);
		let gain = baseVol;
		if (ev.hasPosition && this.camera && kind === "sfx") {
			gain *= distanceGain(this.camera.cx(), this.camera.cy(), ev.x, ev.y, this.falloff);
		}
		eventGain.gain.value = gain;

		if (ev.hasPosition && this.camera && kind === "sfx" && typeof ctx.createStereoPanner === "function") {
			const panner = ctx.createStereoPanner();
			panner.pan.value = pan(this.camera.cx(), ev.x, this.falloff.innerSub);
			source.connect(eventGain).connect(panner).connect(target);
		} else {
			source.connect(eventGain).connect(target);
		}
		source.start();
	}
}

// ---- Defaults ----

function defaultAudioCtxFactory(): AudioContext {
	const W = globalThis as unknown as { AudioContext?: typeof AudioContext; webkitAudioContext?: typeof AudioContext };
	const Ctor = W.AudioContext ?? W.webkitAudioContext;
	if (!Ctor) throw new Error("audio: no AudioContext available");
	return new Ctor();
}

async function defaultFetcher(url: string, ctx: AudioContext): Promise<AudioBuffer> {
	const res = await fetch(url, { credentials: "same-origin" });
	if (!res.ok) throw new Error(`audio fetch ${url}: ${res.status}`);
	const ab = await res.arrayBuffer();
	return ctx.decodeAudioData(ab);
}

function clamp01(n: number): number {
	if (!Number.isFinite(n)) return 0;
	if (n < 0) return 0;
	if (n > 1) return 1;
	return n;
}
