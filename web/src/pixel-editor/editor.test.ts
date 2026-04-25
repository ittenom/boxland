// @vitest-environment jsdom
import "./_test_setup";
import { describe, expect, it } from "vitest";
import { PixelBuffer } from "./buffer";
import { PixelEditor } from "./editor";

// jsdom canvases don't support 2d context; stub it on the prototype.
function stubCanvasCtx(): void {
	const proto = HTMLCanvasElement.prototype as unknown as {
		getContext: () => CanvasRenderingContext2D | null;
	};
	proto.getContext = function () {
		return {
			imageSmoothingEnabled: false,
			drawImage() {},
			clearRect() {},
			putImageData() {},
			getImageData(_x: number, _y: number, w: number, h: number) {
				return new ImageData(new Uint8ClampedArray(w * h * 4), w, h);
			},
		} as unknown as CanvasRenderingContext2D;
	};
}

function newEditor(): PixelEditor {
	stubCanvasCtx();
	const canvas = document.createElement("canvas");
	const buffer = new PixelBuffer(4, 4);
	return new PixelEditor({ canvas, buffer });
}

describe("PixelEditor commands", () => {
	it("stroke commit captures undo correctly", async () => {
		const ed = newEditor();
		ed.setColor({ r: 200, g: 0, b: 0, a: 255 });

		// Simulate a stroke directly via the bus (the mouse handler does the
		// same thing but we can't synthesize a precise rect in jsdom).
		await ed.bus.dispatch("pencil.stroke", [
			{ x: 0, y: 0, prev: { r: 0, g: 0, b: 0, a: 0 }, next: { r: 200, g: 0, b: 0, a: 255 } },
			{ x: 1, y: 0, prev: { r: 0, g: 0, b: 0, a: 0 }, next: { r: 200, g: 0, b: 0, a: 255 } },
		]);
		expect(ed.bus.canUndo()).toBe(true);
		expect(ed.bus.canRedo()).toBe(false);

		await ed.bus.undo();
		expect(ed.bus.canUndo()).toBe(false);
		expect(ed.bus.canRedo()).toBe(true);

		await ed.bus.redo();
		expect(ed.bus.canRedo()).toBe(false);
	});

	it("hotkeys are registered", () => {
		const ed = newEditor();
		expect(ed.bus.hotkeyFor("tool.pencil")).toBe("B");
		expect(ed.bus.hotkeyFor("tool.eraser")).toBe("E");
		expect(ed.bus.hotkeyFor("tool.picker")).toBe("I");
		expect(ed.bus.hotkeyFor("edit.undo")).toBe("Mod+Z");
		expect(ed.bus.hotkeyFor("edit.redo")).toBe("Mod+Shift+Z");
	});

	it("setTool changes state", () => {
		const ed = newEditor();
		ed.setTool("eraser");
		expect(ed.state.tool).toBe("eraser");
	});

	it("setColor changes state", () => {
		const ed = newEditor();
		ed.setColor({ r: 1, g: 2, b: 3, a: 4 });
		expect(ed.state.color).toEqual({ r: 1, g: 2, b: 3, a: 4 });
	});
});
