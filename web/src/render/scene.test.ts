// @vitest-environment jsdom
//
// Scene reconciliation without a real Pixi renderer.
//
// Pixi 8 needs WebGL to init an Application; we don't need that to
// exercise the *unified entity reconciliation* (sprites added, removed,
// re-positioned). We pass a stub AssetCatalog whose frame() returns
// undefined, so the texture is never set, but the sprite lifecycle still
// runs through Scene.update.

import { describe, expect, it } from "vitest";
import { Scene } from "./scene";
import type { AssetCatalog, Renderable } from "./types";

const stubCatalog: AssetCatalog = {
	urlFor: () => "data:,",
	frame: () => undefined, // never resolves a frame; sprite stays texture-less
};

const SCENE_OPTS = { worldViewW: 320, worldViewH: 200 };

function rb(id: number, x = 0, y = 0): Renderable {
	return { id, asset_id: 1, anim_id: 0, anim_frame: 0, x, y, layer: 0 };
}

describe("Scene variant + tint", () => {
	it("frame() is consulted on each update so anim_id and frame can change", async () => {
		const frameSeen: Array<[number, number, number]> = [];
		const trackingCatalog: AssetCatalog = {
			urlFor: () => "data:,",
			frame(asset_id, anim_id, frame) {
				frameSeen.push([asset_id, anim_id, frame]);
				return undefined;
			},
		};
		const s = new Scene(trackingCatalog, SCENE_OPTS);
		await s.update([{ ...rb(1), anim_id: 0, anim_frame: 0 }], { cx: 0, cy: 0 });
		await s.update([{ ...rb(1), anim_id: 0, anim_frame: 1 }], { cx: 0, cy: 0 });
		expect(frameSeen.length).toBe(2);
		expect(frameSeen[1]?.[2]).toBe(1);
	});

	it("strips the alpha byte when applying tint as a Pixi RGB multiply", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		// 0xFF8800FF = orange RGB, fully-opaque alpha.
		await s.update([{ ...rb(1), tint: 0xff8800ff }], { cx: 0, cy: 0 });
		const sprite = s.entitySprites()[0] as { tint: number };
		expect(sprite.tint).toBe(0xff8800);
	});

	it("falls back to white tint when r.tint is 0 or absent", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		await s.update([{ ...rb(1), tint: 0 }, rb(2)], { cx: 0, cy: 0 });
		for (const child of s.entitySprites()) {
			expect((child as { tint: number }).tint).toBe(0xffffff);
		}
	});
});

describe("Scene", () => {
	it("creates one sprite per Renderable on first update", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		await s.update([rb(1), rb(2), rb(3)], { cx: 0, cy: 0 });
		expect(s.size()).toBe(3);
	});

	it("re-uses sprites for the same id across updates", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		await s.update([rb(1)], { cx: 0, cy: 0 });
		await s.update([rb(1, 100, 100)], { cx: 0, cy: 0 });
		expect(s.size()).toBe(1);
	});

	it("removes sprites whose ids drop out of the new set", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		await s.update([rb(1), rb(2), rb(3)], { cx: 0, cy: 0 });
		await s.update([rb(2)], { cx: 0, cy: 0 });
		expect(s.size()).toBe(1);
	});

	it("integrates resize() to update the root container scale", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		await s.update([rb(1)], { cx: 0, cy: 0 });
		s.resize(640, 400);
		expect(s.root.scale.x).toBe(2);
		s.resize(960, 600);
		expect(s.root.scale.x).toBe(3);
	});

	it("keeps z-order matching layer", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		const top: Renderable    = { ...rb(1), layer: 10 };
		const middle: Renderable = { ...rb(2), layer: 5 };
		const bottom: Renderable = { ...rb(3), layer: 0 };
		// Inserted out of order on purpose.
		await s.update([middle, bottom, top], { cx: 0, cy: 0 });
		const zs = s.entitySprites().map((c) => c.zIndex ?? 0);
		expect(zs).toEqual([...zs].sort((a, b) => a - b));
	});
});

describe("Scene y-sort + draw-above (indie-RPG research §P1 #8)", () => {
	it("y-sorts siblings on the same layer by ascending footY", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		// Three trees on layer 0 at increasing footY -- southern trees draw
		// later (on top), matching the walk-behind illusion.
		const north  = { ...rb(1), layer: 0, footY: 100 };
		const middle = { ...rb(2), layer: 0, footY: 200 };
		const south  = { ...rb(3), layer: 0, footY: 300 };
		// Inserted out of order on purpose.
		await s.update([south, north, middle], { cx: 0, cy: 0 });
		expect(s.sortedEntityIds()).toEqual([1, 2, 3]);
	});

	it("draws the player on top when north of the tree, behind when south", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		const tree = { ...rb(1), layer: 0, footY: 500 };
		// Player north of tree -> player should draw FIRST (under).
		await s.update([tree, { ...rb(2), layer: 0, footY: 200 }], { cx: 0, cy: 0 });
		expect(s.sortedEntityIds()).toEqual([2, 1]);
		// Player south of tree -> player should draw LAST (over).
		await s.update([tree, { ...rb(2), layer: 0, footY: 800 }], { cx: 0, cy: 0 });
		expect(s.sortedEntityIds()).toEqual([1, 2]);
	});

	it("draw-above pins a sprite over its y-sorted siblings on the same layer", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		const tree    = { ...rb(1), layer: 0, footY: 999 };           // very south
		const overlay = { ...rb(2), layer: 0, footY: 100, drawAbove: true }; // very north
		await s.update([tree, overlay], { cx: 0, cy: 0 });
		// Despite the lower footY, drawAbove forces the overlay to paint last.
		expect(s.sortedEntityIds()).toEqual([1, 2]);
	});

	it("layer dominates footY and drawAbove", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		const treeAbove = { ...rb(1), layer: 0,   footY: 0,   drawAbove: true };
		const hudLayer  = { ...rb(2), layer: 100, footY: 999 };
		await s.update([treeAbove, hudLayer], { cx: 0, cy: 0 });
		// layer 0 always before layer 100 regardless of inner keys.
		expect(s.sortedEntityIds()).toEqual([1, 2]);
	});

	it("falls back to layer-only sort when footY is undefined (back-compat)", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		const a = { ...rb(1), layer: 5 };
		const b = { ...rb(2), layer: 2 };
		const c = { ...rb(3), layer: 9 };
		await s.update([a, b, c], { cx: 0, cy: 0 });
		expect(s.sortedEntityIds()).toEqual([2, 1, 3]);
	});

	it("is stable across re-updates (no per-frame churn for static entities)", async () => {
		const s = new Scene(stubCatalog, SCENE_OPTS);
		const a = { ...rb(10), layer: 0, footY: 200 };
		const b = { ...rb(11), layer: 0, footY: 200 }; // tied footY
		await s.update([a, b], { cx: 0, cy: 0 });
		const first = s.sortedEntityIds();
		await s.update([b, a], { cx: 0, cy: 0 }); // input order swapped
		expect(s.sortedEntityIds()).toEqual(first); // tiebreak by id keeps it stable
	});
});
