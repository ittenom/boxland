// @vitest-environment jsdom
//
// Verifies the variant-id -> URL pipeline. Pixi's Assets.load is mocked
// to return a stub texture so this test runs without WebGL / network.

import { beforeEach, describe, expect, it, vi } from "vitest";

// Mock pixi.js Assets BEFORE importing TextureCache so the module
// captures the mocked symbol.
const loaded: string[] = [];
const loadedRaw: unknown[] = [];
vi.mock("pixi.js", async () => {
	const actual = await vi.importActual<Record<string, unknown>>("pixi.js");
	return {
		...actual,
		Assets: {
			load: vi.fn(async (asset: string | { src: string }) => {
				loadedRaw.push(asset);
				const url = typeof asset === "string" ? asset : asset.src;
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
		loadedRaw.length = 0;
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

	it("frame() treats an empty URL as a missing texture without calling Assets.load", async () => {
		const catalog: AssetCatalog = {
			urlFor: () => "",
			frame: (asset_id, anim_id, frame) => ({
				asset_id, anim_id, frame,
				sx: 0, sy: 0, sw: 32, sh: 32,
				ax: 0, ay: 0,
			}),
		};
		const cache = new TextureCache(catalog);
		await expect(cache.frame(1, 0, 0)).resolves.toBeUndefined();
		expect(loaded).toHaveLength(0);
	});

	it("forces the texture parser for extensionless designer blob URLs", async () => {
		const catalog: AssetCatalog = {
			urlFor: () => "/design/assets/blob/42",
			frame: (asset_id, anim_id, frame) => ({
				asset_id, anim_id, frame,
				sx: 0, sy: 0, sw: 32, sh: 32,
				ax: 0, ay: 0,
			}),
		};
		const cache = new TextureCache(catalog);
		await cache.base(1, 0);
		expect(loadedRaw[0]).toEqual({
			alias: "/design/assets/blob/42",
			src: "/design/assets/blob/42",
			parser: "texture",
		});
	});
});
