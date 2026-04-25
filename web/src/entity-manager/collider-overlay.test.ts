// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { autoMountColliderOverlays, drawColliderOverlay } from "./collider-overlay";

const stubCtx = {
	imageSmoothingEnabled: false,
	fillStyle: "",
	strokeStyle: "",
	lineWidth: 0,
	fillRect: vi.fn(),
	strokeRect: vi.fn(),
	drawImage: vi.fn(),
};

beforeEach(() => {
	stubCtx.fillRect.mockClear();
	stubCtx.strokeRect.mockClear();
	stubCtx.drawImage.mockClear();
	(HTMLCanvasElement.prototype as unknown as { getContext: () => unknown }).getContext = () => stubCtx;
});

describe("drawColliderOverlay", () => {
	it("paints background + outline even without a sprite URL", () => {
		const c = document.createElement("canvas");
		c.width = 64;
		c.height = 64;
		drawColliderOverlay(c, {
			spriteURL: "",
			colliderW: 16,
			colliderH: 16,
			anchorX: 8,
			anchorY: 16,
		});
		expect(stubCtx.fillRect.mock.calls.length).toBe(1); // bg
		expect(stubCtx.strokeRect.mock.calls.length).toBe(1); // outline
	});
});

describe("autoMountColliderOverlays", () => {
	it("attaches to every overlay canvas and live-updates on form input", () => {
		document.body.innerHTML = `
			<div class="bx-modal__body">
				<form>
					<input name="collider_w" value="16">
					<input name="collider_h" value="16">
					<input name="collider_anchor_x" value="8">
					<input name="collider_anchor_y" value="16">
				</form>
				<canvas data-bx-collider-overlay
				        data-collider-w="16" data-collider-h="16"
				        data-anchor-x="8" data-anchor-y="16"
				        width="64" height="64"></canvas>
			</div>
		`;
		autoMountColliderOverlays();
		const initial = stubCtx.strokeRect.mock.calls.length;
		expect(initial).toBeGreaterThanOrEqual(1);

		// Mutate the collider width and dispatch input.
		const wInput = document.querySelector('input[name="collider_w"]') as HTMLInputElement;
		wInput.value = "24";
		wInput.dispatchEvent(new Event("input", { bubbles: true }));
		expect(stubCtx.strokeRect.mock.calls.length).toBeGreaterThan(initial);
	});
});
