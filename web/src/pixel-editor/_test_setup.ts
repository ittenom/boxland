// Test-only DOM polyfills that jsdom doesn't ship by default but the
// pixel editor relies on.

class FakeImageData {
	data: Uint8ClampedArray;
	width: number;
	height: number;
	colorSpace = "srgb" as const;
	constructor(data: Uint8ClampedArray | number, w?: number, h?: number) {
		if (data instanceof Uint8ClampedArray) {
			this.data = data;
			this.width = w ?? 0;
			this.height = h ?? 0;
		} else {
			// new ImageData(w, h)
			this.width = data;
			this.height = w ?? 0;
			this.data = new Uint8ClampedArray(this.width * this.height * 4);
		}
	}
}

if (typeof (globalThis as { ImageData?: unknown }).ImageData === "undefined") {
	(globalThis as { ImageData: typeof FakeImageData }).ImageData = FakeImageData;
}
