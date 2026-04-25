// @vitest-environment jsdom
//
// Verifies the variant-id -> URL pipeline. Pixi's Assets.load is mocked
// to return a stub texture so this test runs without WebGL / network.

import { beforeEach, describe, expect, it, vi } from "vitest";

// Mock pixi.js Assets BEFORE importing TextureCache so the module
// captures the mocked symbol.
const loaded: string[] = [];
vi.mock("pixi.js", async () => {
	const actual = await vi.importActual<Record<string, unknown>>("pixi.js");
	return {
		...actual,
		Assets: {
			load: vi.fn(async (url: string) => {
				loaded.push(url);
				return {
					source: { scaleMode: "" as string },
				};
			}),
		},
	};
});

const { TextureCache } = await import("./textures");
import type { AssetCatalog } from "./types";

describe("TextureCache variant pipeline", () => {
	beforeEach(() => {
		loaded.length = 0;
	});

	it("urlFor receives variant_id and the result is what gets loaded", async () => {
		const catalog: AssetCatalog = {
			urlFor: (asset_id, variant_id) => `https://cdn/${asset_id}/${variant_id ?? 0}.png`,
			frame: () => undefined, // frame irrelevant; we test base() directly
		};
		const cache = new TextureCache(catalog);
		await cache.base(42, 7);
		expect(loaded).toContain("https://cdn/42/7.png");
	});

	it("re-uses the same base for repeated calls (cache hit)", async () => {
		const catalog: AssetCatalog = {
			urlFor: () => "https://cdn/x.png",
			frame: () => undefined,
		};
		const cache = new TextureCache(catalog);
		await cache.base(1, 0);
		await cache.base(1, 0);
		await cache.base(1, 0);
		expect(loaded.length).toBe(1);
	});

	it("variant_id distinguishes cache entries", async () => {
		const catalog: AssetCatalog = {
			urlFor: (a, v) => `https://cdn/${a}-${v ?? 0}.png`,
			frame: () => undefined,
		};
		const cache = new TextureCache(catalog);
		await cache.base(1, 0);
		await cache.base(1, 5);
		expect(loaded.length).toBe(2);
		expect(loaded).toContain("https://cdn/1-0.png");
		expect(loaded).toContain("https://cdn/1-5.png");
	});
});
