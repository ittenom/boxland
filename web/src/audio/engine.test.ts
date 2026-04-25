import { describe, it, expect, vi } from "vitest";

import { SoundEngine } from "./engine";
import type { SoundCatalog, AudioCameraReader } from "./types";

// ---- Fake AudioContext + AudioBuffer ----

interface FakeNode {
	connect: (next: FakeNode) => FakeNode;
	gain?: { value: number };
	pan?: { value: number };
	playbackRate?: { value: number };
	buffer?: object;
	start?: () => void;
	connections?: FakeNode[];
}

function makeFakeCtx(opts: { hasPanner?: boolean } = {}): {
	ctx: AudioContext;
	created: { gains: FakeNode[]; sources: FakeNode[]; panners: FakeNode[] };
	state: () => string;
} {
	const created = { gains: [] as FakeNode[], sources: [] as FakeNode[], panners: [] as FakeNode[] };
	let state = "suspended";
	const mkNode = (label: "gain" | "src" | "pan"): FakeNode => {
		const node: FakeNode = {
			connections: [],
			connect(next: FakeNode) { node.connections!.push(next); return next; },
		};
		if (label === "gain") node.gain = { value: 1 };
		if (label === "src") {
			node.buffer = {};
			node.playbackRate = { value: 1 };
			node.start = () => undefined;
		}
		if (label === "pan") node.pan = { value: 0 };
		return node;
	};
	const ctx = {
		state: state,
		createGain: () => { const n = mkNode("gain"); created.gains.push(n); return n; },
		createBufferSource: () => { const n = mkNode("src"); created.sources.push(n); return n; },
		createStereoPanner: opts.hasPanner === false
			? undefined
			: () => { const n = mkNode("pan"); created.panners.push(n); return n; },
		decodeAudioData: vi.fn(async () => ({ duration: 1 })),
		destination: mkNode("gain"),
		resume: vi.fn(async () => { state = "running"; }),
	} as unknown as AudioContext;
	return { ctx, created, state: () => state };
}

const stubCatalog: SoundCatalog = {
	urlFor: (id) => id === 99 ? undefined : `data:audio/wav;sound${id}`,
};

describe("SoundEngine volume buses", () => {
	it("applies initial volume levels to the gain buses on first context build", () => {
		const fake = makeFakeCtx();
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
			initialVolume: { master: 0.5, music: 0.3, sfx: 0.7 },
		});
		// Force context creation by playing a sound (no-op without buffer; still triggers init).
		eng.resume();
		// 3 buses + 1 destination... we created 3 buses ourselves.
		const [master, music, sfx] = fake.created.gains;
		expect(master?.gain?.value).toBe(0.5);
		expect(music?.gain?.value).toBe(0.3);
		expect(sfx?.gain?.value).toBe(0.7);
	});

	it("setMaster/Music/Sfx clamp + write through to gain nodes", () => {
		const fake = makeFakeCtx();
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		eng.resume();
		eng.setMaster(2);    // clamp to 1
		eng.setMusic(-1);    // clamp to 0
		eng.setSfx(0.4);
		expect(eng.levels()).toEqual({ master: 1, music: 0, sfx: 0.4 });
		const [master, music, sfx] = fake.created.gains;
		expect(master?.gain?.value).toBe(1);
		expect(music?.gain?.value).toBe(0);
		expect(sfx?.gain?.value).toBe(0.4);
	});

	it("stores levels even when no AudioContext has been built yet", () => {
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => { throw new Error("no audio yet"); },
			fetcher: async () => ({} as AudioBuffer),
		});
		eng.setMaster(0.4);
		eng.setMusic(0.6);
		eng.setSfx(0.8);
		expect(eng.levels()).toEqual({ master: 0.4, music: 0.6, sfx: 0.8 });
	});
});

describe("SoundEngine playback", () => {
	it("preload caches buffers across plays", async () => {
		const fake = makeFakeCtx();
		const fetcher = vi.fn(async () => ({ duration: 1 }) as unknown as AudioBuffer);
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher,
		});
		await eng.preload(1);
		await eng.preload(1);
		expect(fetcher).toHaveBeenCalledTimes(1);
	});

	it("preload returns null for unknown sound ids", async () => {
		const fake = makeFakeCtx();
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		expect(await eng.preload(99)).toBeNull();
	});

	it("play() builds source -> gain -> sfxBus when no panner is requested", async () => {
		const fake = makeFakeCtx();
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		await eng.preload(1);
		eng.play({ soundId: 1, hasPosition: false, x: 0, y: 0, volume: 200, pitch: 0 });
		// We expect: 3 bus gains + 1 per-event gain = 4 gains created.
		expect(fake.created.gains.length).toBe(4);
		expect(fake.created.sources.length).toBe(1);
		expect(fake.created.panners.length).toBe(0);
		// Per-event gain = 200/255 ~= 0.78.
		const eventGain = fake.created.gains[3]!;
		expect(eventGain.gain!.value).toBeCloseTo(200 / 255, 3);
		// playbackRate at 1 since pitch=0.
		expect(fake.created.sources[0]!.playbackRate!.value).toBe(1);
	});

	it("positional play() routes through a StereoPanner with camera-relative pan", async () => {
		const fake = makeFakeCtx();
		const cam: AudioCameraReader = { cx: () => 0, cy: () => 0 };
		const eng = new SoundEngine({
			catalog: stubCatalog,
			camera: cam,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		await eng.preload(1);
		// Far to the right -> pan ~= +1.
		eng.play({ soundId: 1, hasPosition: true, x: 100_000_000, y: 0, volume: 255, pitch: 0 });
		expect(fake.created.panners).toHaveLength(1);
		expect(fake.created.panners[0]!.pan!.value).toBe(1);
	});

	it("non-positional play() ignores the panner even when a camera is set", async () => {
		const fake = makeFakeCtx();
		const eng = new SoundEngine({
			catalog: stubCatalog,
			camera: { cx: () => 0, cy: () => 0 },
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		await eng.preload(1);
		eng.play({ soundId: 1, hasPosition: false, x: 999, y: 999, volume: 255, pitch: 0 });
		expect(fake.created.panners).toHaveLength(0);
	});

	it("playMany feeds every event through play()", async () => {
		const fake = makeFakeCtx();
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		await eng.preload(1);
		await eng.preload(2);
		eng.playMany([
			{ soundId: 1, hasPosition: false, x: 0, y: 0, volume: 255, pitch: 0 },
			{ soundId: 2, hasPosition: false, x: 0, y: 0, volume: 255, pitch: 0 },
		]);
		expect(fake.created.sources).toHaveLength(2);
	});

	it("missing sound id no-ops without throwing", () => {
		const fake = makeFakeCtx();
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		expect(() => eng.play({
			soundId: 99, hasPosition: false, x: 0, y: 0, volume: 255, pitch: 0,
		})).not.toThrow();
		expect(fake.created.sources).toHaveLength(0);
	});

	it("resume() calls AudioContext.resume when suspended", () => {
		const fake = makeFakeCtx();
		const resumeSpy = fake.ctx.resume as unknown as ReturnType<typeof vi.fn>;
		const eng = new SoundEngine({
			catalog: stubCatalog,
			audioCtxFactory: () => fake.ctx,
			fetcher: async () => ({} as AudioBuffer),
		});
		eng.resume();
		expect(resumeSpy).toHaveBeenCalled();
	});
});
