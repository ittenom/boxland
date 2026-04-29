// Boxland — vitest global setup.
//
// Polyfills the small set of browser globals jsdom doesn't provide
// but Pixi 8's `Text` measurement path expects. Pixi calls
// `getCanvasRenderingContext2D()` on its BrowserAdapter even from
// `Text.bounds`, so just having jsdom installed isn't enough — we
// need `globalThis.CanvasRenderingContext2D` to exist.
//
// We delegate to the `canvas` npm package when available (real
// font metrics), falling back to a minimal stub otherwise. The
// stub is good enough to satisfy Pixi's `instanceof` check and
// return zero-bounds; tests that depend on actual text metrics
// should opt into real measurement explicitly.

import { CanvasRenderingContext2D as NodeCanvas2D, createCanvas } from "canvas";

// Expose CanvasRenderingContext2D on the global so Pixi's
// `getCanvasRenderingContext2D()` returns the node-canvas
// constructor.
(globalThis as unknown as { CanvasRenderingContext2D: typeof NodeCanvas2D }).CanvasRenderingContext2D = NodeCanvas2D;

// Some Pixi paths also look at the document's createElement for
// `<canvas>` and call `.getContext("2d")` on the result. jsdom's
// HTMLCanvasElement has no `.getContext`; replace it with one that
// delegates to node-canvas. (jsdom log noise about
// "HTMLCanvasElement's getContext() method" disappears too.)
if (typeof globalThis.document !== "undefined") {
	const orig = HTMLCanvasElement.prototype.getContext;
	HTMLCanvasElement.prototype.getContext = function (
		this: HTMLCanvasElement,
		type: string,
		...args: unknown[]
	): unknown {
		if (type === "2d") {
			const w = this.width || 1;
			const h = this.height || 1;
			const node = createCanvas(w, h);
			return node.getContext("2d");
		}
		return (orig as unknown as (this: HTMLCanvasElement, t: string, ...a: unknown[]) => unknown).apply(this, [type, ...args]);
	} as typeof HTMLCanvasElement.prototype.getContext;
}
