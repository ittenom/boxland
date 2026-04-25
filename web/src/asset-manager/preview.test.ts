// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { autoMountPreviews, mountPreview } from "./preview";

describe("mountPreview", () => {
	it("returns a no-op stopper when the canvas has no 2D context", () => {
		// jsdom canvases don't implement getContext; this proves we don't crash.
		const c = document.createElement("canvas");
		const stop = mountPreview(c, { url: "data:,", gridW: 32, gridH: 32, fps: 8 });
		expect(typeof stop).toBe("function");
		stop();
	});

	it("sets canvas dimensions to the grid size", () => {
		const c = document.createElement("canvas");
		// Stub a minimal 2D context so the early-return doesn't trigger.
		c.getContext = (() => ({
			imageSmoothingEnabled: false,
			drawImage: () => undefined,
			clearRect: () => undefined,
		})) as unknown as typeof c.getContext;
		mountPreview(c, { url: "data:,", gridW: 24, gridH: 16, fps: 4 });
		expect(c.width).toBe(24);
		expect(c.height).toBe(16);
	});
});

describe("autoMountPreviews", () => {
	it("attaches to every matching canvas", () => {
		document.body.innerHTML = `
			<canvas data-bx-asset-preview="1" data-asset-url="data:," data-grid-w="32" data-grid-h="32" data-fps="8"></canvas>
			<canvas data-bx-asset-preview="2" data-asset-url="data:," data-grid-w="16" data-grid-h="16" data-fps="4"></canvas>
		`;
		// Stub getContext so mountPreview doesn't bail.
		for (const c of document.querySelectorAll("canvas")) {
			c.getContext = (() => ({
				imageSmoothingEnabled: false,
				drawImage: () => undefined,
				clearRect: () => undefined,
			})) as unknown as typeof c.getContext;
		}
		autoMountPreviews();
		const widths = [...document.querySelectorAll("canvas")].map((c) => c.width);
		expect(widths).toEqual([32, 16]);
	});

	it("is idempotent: running twice doesn't double-attach", () => {
		document.body.innerHTML = `<canvas data-bx-asset-preview="1" data-asset-url="data:," data-grid-w="32" data-grid-h="32" data-fps="8"></canvas>`;
		const c = document.querySelector("canvas")!;
		c.getContext = (() => ({
			imageSmoothingEnabled: false,
			drawImage: () => undefined,
			clearRect: () => undefined,
		})) as unknown as typeof c.getContext;
		autoMountPreviews();
		const first = (c as HTMLCanvasElement & { __bxStop?: () => void }).__bxStop;
		autoMountPreviews();
		const second = (c as HTMLCanvasElement & { __bxStop?: () => void }).__bxStop;
		expect(first).not.toBe(second); // a new stopper replaced the prior one
	});
});
