import { describe, expect, it } from "vitest";

import { roleTone } from "./surface";
import { pixiUITokens, surfacePalette } from "./tokens";

describe("Pixi UI surface tokens", () => {
	it("maps legacy editor roles to canonical surface tones", () => {
		expect(roleTone("frame_vertical")).toBe("panel");
		expect(roleTone("button_sm_release_a")).toBe("button");
		expect(roleTone("button_sm_press_a")).toBe("buttonActive");
		expect(roleTone("button_sm_lock_a")).toBe("buttonDisabled");
		expect(roleTone("slot_selected")).toBe("slotSelected");
		expect(roleTone("scroll_bar")).toBe("scrollTrack");
		expect(roleTone("scroll_handle")).toBe("scrollThumb");
	});

	it("keeps every tone backed by a concrete fill and border color", () => {
		for (const tone of ["panel", "raised", "sunken", "button", "buttonActive", "buttonDisabled", "toolActive", "slot", "slotSelected", "scrollTrack", "scrollThumb", "input"] as const) {
			const palette = surfacePalette(tone);
			expect(Number.isInteger(palette.fill)).toBe(true);
			expect(Number.isInteger(palette.border)).toBe(true);
			expect(palette.fill).toBeGreaterThanOrEqual(0);
			expect(palette.fill).toBeLessThanOrEqual(0xffffff);
		}
	});

	it("defines a compact mono typography scale for editor chrome", () => {
		expect(pixiUITokens.type.family).toContain("DM Mono");
		expect(pixiUITokens.type.sizeXs).toBeLessThan(pixiUITokens.type.sizeSm);
		expect(pixiUITokens.type.sizeSm).toBeLessThan(pixiUITokens.type.sizeMd);
	});

	it("keeps editor chrome square-edged", () => {
		expect(pixiUITokens.shape.radiusSm).toBe(0);
		expect(pixiUITokens.shape.radiusMd).toBe(0);
		expect(pixiUITokens.shape.radiusLg).toBe(0);
	});
});
