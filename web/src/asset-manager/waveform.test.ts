// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mountWaveform } from "./waveform";

// Stub a basic Canvas2D context on the prototype so jsdom canvases respond.
const stubCtx = {
	imageSmoothingEnabled: false,
	fillStyle: "",
	fillRect: vi.fn(),
};

beforeEach(() => {
	(HTMLCanvasElement.prototype as unknown as { getContext: () => unknown }).getContext = () => stubCtx;
	stubCtx.fillRect.mockClear();
	// Clean fetch override before each test.
	(globalThis as { fetch?: unknown }).fetch = vi.fn(() =>
		Promise.reject(new Error("no network in tests")),
	);
});

afterEach(() => {
	delete (window as unknown as { AudioContext?: unknown }).AudioContext;
	delete (window as unknown as { webkitAudioContext?: unknown }).webkitAudioContext;
});

describe("mountWaveform", () => {
	it("falls back to no-preview when fetch fails", async () => {
		const c = document.createElement("canvas");
		await mountWaveform(c, { url: "http://nope" });
		// Initial bg fill + the no-preview hairline = at least 2 fillRect calls.
		expect(stubCtx.fillRect.mock.calls.length).toBeGreaterThanOrEqual(2);
	});

	it("uses bins/height options for canvas sizing", async () => {
		const c = document.createElement("canvas");
		await mountWaveform(c, { url: "http://nope", bins: 80, height: 40 });
		expect(c.width).toBe(80);
		expect(c.height).toBe(40);
	});

	it("falls back when AudioContext.decodeAudioData rejects", async () => {
		(globalThis as { fetch?: unknown }).fetch = vi.fn(() =>
			Promise.resolve({
				ok: true,
				arrayBuffer: () => Promise.resolve(new ArrayBuffer(8)),
			}) as unknown as Response,
		);
		(window as unknown as { AudioContext: unknown }).AudioContext = class {
			decodeAudioData() {
				return Promise.reject(new Error("decode failed"));
			}
		};
		const c = document.createElement("canvas");
		await mountWaveform(c, { url: "http://x" });
		// Fallback path triggered; no throw.
		expect(stubCtx.fillRect.mock.calls.length).toBeGreaterThanOrEqual(2);
	});

	it("paints peak bars when decoding succeeds", async () => {
		(globalThis as { fetch?: unknown }).fetch = vi.fn(() =>
			Promise.resolve({
				ok: true,
				arrayBuffer: () => Promise.resolve(new ArrayBuffer(8)),
			}) as unknown as Response,
		);
		// Build a deterministic stub AudioBuffer with one channel.
		const samples = new Float32Array(64);
		for (let i = 0; i < samples.length; i++) samples[i] = (i % 2 === 0) ? 0.8 : -0.6;
		(window as unknown as { AudioContext: unknown }).AudioContext = class {
			decodeAudioData() {
				return Promise.resolve({
					getChannelData: () => samples,
				} as unknown as AudioBuffer);
			}
		};

		const c = document.createElement("canvas");
		await mountWaveform(c, { url: "http://x", bins: 8, height: 8 });
		// We expect: bg fill + bg fill (re-clear) + at least bins peak bars.
		expect(stubCtx.fillRect.mock.calls.length).toBeGreaterThanOrEqual(8);
	});
});
