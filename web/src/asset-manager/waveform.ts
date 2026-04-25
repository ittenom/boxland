// Boxland — chunky pixel waveform preview.
//
// Per PLAN.md §5c. Renders a downsampled max-amplitude waveform of an
// audio file into a Canvas2D. Designed to look "blocky" (no antialiasing,
// no smoothing) so it matches the rest of the pixel-art UI.
//
// Audio decoding uses the standard Web Audio API; supports WAV, OGG, and
// MP3 in any browser that does. We don't need playback control here --
// just samples for rendering.

export interface WaveformOptions {
	url: string;
	bins?: number;        // horizontal pixel count of the waveform
	height?: number;      // vertical pixel count
	color?: string;       // foreground bar color (CSS)
	background?: string;  // canvas clear color (CSS)
}

/**
 * Mount a waveform onto the supplied canvas. Returns a Promise that
 * resolves when the audio has been decoded and rendered (handy for
 * tests). Errors fall back to a "no preview available" fill so the UI
 * doesn't render an empty box.
 */
export async function mountWaveform(canvas: HTMLCanvasElement, opts: WaveformOptions): Promise<void> {
	const ctx = canvas.getContext("2d");
	if (!ctx) return;
	ctx.imageSmoothingEnabled = false;

	const bins = Math.max(8, opts.bins ?? 64);
	const height = Math.max(8, opts.height ?? 32);
	canvas.width = bins;
	canvas.height = height;
	canvas.style.imageRendering = "pixelated";

	const fg = opts.color ?? "#ffd34a";
	const bg = opts.background ?? "#1a1733";

	// Initial paint: empty bg so the user sees a stable placeholder.
	ctx.fillStyle = bg;
	ctx.fillRect(0, 0, bins, height);

	let buf: ArrayBuffer;
	try {
		const res = await fetch(opts.url);
		if (!res.ok) throw new Error(`fetch ${opts.url}: ${res.status}`);
		buf = await res.arrayBuffer();
	} catch {
		paintNoPreview(ctx, bins, height, bg, fg);
		return;
	}

	let audio: AudioBuffer;
	try {
		const Ac =
			(window as unknown as { AudioContext?: typeof AudioContext }).AudioContext ??
			(window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
		if (!Ac) throw new Error("Web Audio not supported");
		const ac = new Ac();
		audio = await ac.decodeAudioData(buf);
	} catch {
		paintNoPreview(ctx, bins, height, bg, fg);
		return;
	}

	const peaks = downsamplePeaks(audio, bins);
	ctx.fillStyle = bg;
	ctx.fillRect(0, 0, bins, height);
	ctx.fillStyle = fg;
	const half = Math.floor(height / 2);
	for (let x = 0; x < peaks.length; x++) {
		const peak = peaks[x] ?? 0;
		const h = Math.max(1, Math.floor(peak * half));
		ctx.fillRect(x, half - h, 1, h * 2);
	}
}

/**
 * downsamplePeaks reduces an AudioBuffer to `bins` per-bin peak amplitudes
 * in the [0, 1] range. Uses max-of-absolute-value within each bucket so
 * loud transients still register at the chunky resolution.
 */
function downsamplePeaks(audio: AudioBuffer, bins: number): number[] {
	const ch0 = audio.getChannelData(0);
	const samplesPerBin = Math.max(1, Math.floor(ch0.length / bins));
	const peaks = new Array<number>(bins).fill(0);
	for (let i = 0; i < bins; i++) {
		const start = i * samplesPerBin;
		const end = Math.min(start + samplesPerBin, ch0.length);
		let peak = 0;
		for (let j = start; j < end; j++) {
			const v = Math.abs(ch0[j] ?? 0);
			if (v > peak) peak = v;
		}
		peaks[i] = Math.min(1, peak);
	}
	return peaks;
}

function paintNoPreview(
	ctx: CanvasRenderingContext2D,
	w: number,
	h: number,
	bg: string,
	fg: string,
): void {
	ctx.fillStyle = bg;
	ctx.fillRect(0, 0, w, h);
	// One horizontal hairline so the box doesn't look broken.
	ctx.fillStyle = fg;
	const mid = Math.floor(h / 2);
	ctx.fillRect(0, mid, w, 1);
}

/**
 * Auto-attach to every <canvas data-bx-waveform="<url>"> on the page.
 * Idempotent (rebinds; previous attempts are GC'd by the browser).
 */
export function autoMountWaveforms(root: Document | HTMLElement = document): void {
	const list = root.querySelectorAll<HTMLCanvasElement>("canvas[data-bx-waveform]");
	for (const canvas of list) {
		const url = canvas.dataset.bxWaveform;
		if (!url) continue;
		const bins = canvas.dataset.bins ? Number(canvas.dataset.bins) : undefined;
		const height = canvas.dataset.height ? Number(canvas.dataset.height) : undefined;
		void mountWaveform(canvas, {
			url,
			...(bins !== undefined ? { bins } : {}),
			...(height !== undefined ? { height } : {}),
		});
	}
}
