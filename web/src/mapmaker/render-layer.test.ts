// @vitest-environment jsdom

import { describe, expect, it } from "vitest";

import { StaticAssetCatalog, SUB_PER_PX, type Renderable } from "@render";
import { MapmakerRenderableLayer } from "./render-layer";

function rb(id: number, asset = 1): Renderable {
	return {
		id,
		asset_id: asset,
		anim_id: 0,
		anim_frame: 0,
		x: id * 32 * SUB_PER_PX,
		y: 0,
		layer: 10,
	};
}

describe("MapmakerRenderableLayer", () => {
	it("keeps visible fallback cells for renderables without loaded sprite art", async () => {
		const layer = new MapmakerRenderableLayer(new StaticAssetCatalog({ entries: [] }), 40, 20);

		await layer.setRenderables([rb(1), rb(2), rb(3)]);

		expect(layer.fallbackCount()).toBe(3);
		expect(layer.spriteCount()).toBe(0);
	});

	it("removes stale fallback cells when renderables disappear", async () => {
		const layer = new MapmakerRenderableLayer(new StaticAssetCatalog({ entries: [] }), 40, 20);

		await layer.setRenderables([rb(1), rb(2), rb(3)]);
		await layer.setRenderables([rb(2)]);

		expect(layer.fallbackCount()).toBe(1);
	});
});
