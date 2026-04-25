// Boxland — pixel editor buffer.
//
// Wraps an ImageData with the small surface tools need: get/set pixel,
// blit to a destination canvas at an integer zoom, and export to a Blob
// for upload. All coordinates are buffer-pixel space (1 unit = 1 image px),
// not screen pixels.

import type { RGBA } from "./types";
import { TRANSPARENT } from "./types";

export class PixelBuffer {
	readonly width: number;
	readonly height: number;
	private readonly data: Uint8ClampedArray;

	constructor(width: number, height: number, init?: ImageData) {
		this.width = width;
		this.height = height;
		this.data = init
			? new Uint8ClampedArray(init.data)
			: new Uint8ClampedArray(width * height * 4);
	}

	/**
	 * Build a buffer from a loaded HTMLImageElement. Uses an offscreen
	 * Canvas2D to extract pixels.
	 */
	static fromImage(img: HTMLImageElement): PixelBuffer {
		const c = document.createElement("canvas");
		c.width = img.naturalWidth;
		c.height = img.naturalHeight;
		const ctx = c.getContext("2d");
		if (!ctx) throw new Error("PixelBuffer: 2d context unavailable");
		ctx.imageSmoothingEnabled = false;
		ctx.drawImage(img, 0, 0);
		const id = ctx.getImageData(0, 0, c.width, c.height);
		return new PixelBuffer(c.width, c.height, id);
	}

	get(x: number, y: number): RGBA {
		if (x < 0 || y < 0 || x >= this.width || y >= this.height) return TRANSPARENT;
		const i = (y * this.width + x) * 4;
		return {
			r: this.data[i] ?? 0,
			g: this.data[i + 1] ?? 0,
			b: this.data[i + 2] ?? 0,
			a: this.data[i + 3] ?? 0,
		};
	}

	/** Set a pixel without bounds checks (caller validates). */
	set(x: number, y: number, c: RGBA): void {
		if (x < 0 || y < 0 || x >= this.width || y >= this.height) return;
		const i = (y * this.width + x) * 4;
		this.data[i] = c.r;
		this.data[i + 1] = c.g;
		this.data[i + 2] = c.b;
		this.data[i + 3] = c.a;
	}

	toImageData(): ImageData {
		return new ImageData(new Uint8ClampedArray(this.data), this.width, this.height);
	}

	/** Export as a PNG Blob suitable for upload. */
	async toPNGBlob(): Promise<Blob> {
		const c = document.createElement("canvas");
		c.width = this.width;
		c.height = this.height;
		const ctx = c.getContext("2d");
		if (!ctx) throw new Error("PixelBuffer.toPNGBlob: 2d context unavailable");
		ctx.putImageData(this.toImageData(), 0, 0);
		return new Promise<Blob>((resolve, reject) => {
			c.toBlob((b) => (b ? resolve(b) : reject(new Error("toBlob returned null"))), "image/png");
		});
	}

	/** Render to a destination canvas at integer zoom. */
	blitTo(canvas: HTMLCanvasElement, zoom: number): void {
		const ctx = canvas.getContext("2d");
		if (!ctx) return;
		canvas.width = this.width * zoom;
		canvas.height = this.height * zoom;
		ctx.imageSmoothingEnabled = false;
		const tmp = document.createElement("canvas");
		tmp.width = this.width;
		tmp.height = this.height;
		const tctx = tmp.getContext("2d");
		if (!tctx) return;
		tctx.putImageData(this.toImageData(), 0, 0);
		ctx.clearRect(0, 0, canvas.width, canvas.height);
		ctx.drawImage(tmp, 0, 0, canvas.width, canvas.height);
	}
}
