import { describe, expect, it } from "vitest";
import { StaticAssetCatalog } from "./static-catalog";

describe("StaticAssetCatalog.urlFor", () => {
	it("returns the registered URL for a known id", () => {
		const cat = new StaticAssetCatalog({
			entries: [{ id: 7, url: "/design/assets/blob/7", atlasCols: 1, tileSize: 32, atlasIndex: 0 }],
		});
		expect(cat.urlFor(7)).toBe("/design/assets/blob/7");
	});

	it("returns empty string for an unknown id rather than throwing", () => {
		// Empty string matches RemoteAssetCatalog's contract. TextureCache
		// treats it as "no texture" without asking Pixi to load a bogus URL.
		const cat = new StaticAssetCatalog({ entries: [] });
		expect(cat.urlFor(123)).toBe("");
	});

	it("ignores variant_id on the static catalog (editors render base art)", () => {
		const cat = new StaticAssetCatalog({
			entries: [{ id: 1, url: "/x.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 }],
		});
		expect(cat.urlFor(1, 0)).toBe("/x.png");
		expect(cat.urlFor(1, 5)).toBe("/x.png");
	});
});

describe("StaticAssetCatalog.frame", () => {
	it("computes (sx, sy, sw, sh) from atlasIndex and atlasCols", () => {
		// 4-col atlas. Index 5 = col 1, row 1 → (32, 32, 32, 32).
		const cat = new StaticAssetCatalog({
			entries: [{ id: 1, url: "/x.png", atlasCols: 4, tileSize: 32, atlasIndex: 5 }],
		});
		expect(cat.frame(1, 0, 0)).toEqual({
			asset_id: 1, anim_id: 0, frame: 5,
			sx: 32, sy: 32, sw: 32, sh: 32,
			ax: 0, ay: 0,
		});
	});

	it("returns a 1x1-cell frame for plain single sprites (cols=1)", () => {
		const cat = new StaticAssetCatalog({
			entries: [{ id: 1, url: "/x.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 }],
		});
		expect(cat.frame(1, 0, 0)).toEqual({
			asset_id: 1, anim_id: 0, frame: 0,
			sx: 0, sy: 0, sw: 32, sh: 32,
			ax: 0, ay: 0,
		});
	});

	it("respects a custom tileSize for non-32 atlases", () => {
		const cat = new StaticAssetCatalog({
			entries: [{ id: 1, url: "/x.png", atlasCols: 2, tileSize: 16, atlasIndex: 3 }],
		});
		// idx 3 in a 2-col atlas → col 1, row 1; tileSize 16.
		expect(cat.frame(1, 0, 0)).toEqual({
			asset_id: 1, anim_id: 0, frame: 3,
			sx: 16, sy: 16, sw: 16, sh: 16,
			ax: 0, ay: 0,
		});
	});

	it("returns undefined for unknown ids (renderer skips the texture)", () => {
		const cat = new StaticAssetCatalog({ entries: [] });
		expect(cat.frame(999, 0, 0)).toBeUndefined();
	});

	it("uses the explicit frame index when caller scrubs (not atlasIndex)", () => {
		const cat = new StaticAssetCatalog({
			entries: [{ id: 1, url: "/x.png", atlasCols: 4, tileSize: 32, atlasIndex: 0 }],
		});
		// Scrubbing to frame 7 → col 3, row 1.
		expect(cat.frame(1, 0, 7)).toEqual({
			asset_id: 1, anim_id: 0, frame: 7,
			sx: 96, sy: 32, sw: 32, sh: 32,
			ax: 0, ay: 0,
		});
	});

	it("clamps a non-finite frame index back to atlasIndex", () => {
		const cat = new StaticAssetCatalog({
			entries: [{ id: 1, url: "/x.png", atlasCols: 4, tileSize: 32, atlasIndex: 5 }],
		});
		const f = cat.frame(1, 0, NaN);
		expect(f?.frame).toBe(5);
	});
});

describe("StaticAssetCatalog construction + ergonomics", () => {
	it("rejects duplicate ids whose metadata disagrees", () => {
		expect(() => new StaticAssetCatalog({
			entries: [
				{ id: 1, url: "/a.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 },
				{ id: 1, url: "/b.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 },
			],
		})).toThrow(/duplicate id 1/);
	});

	it("accepts duplicate ids with identical metadata (idempotent inserts)", () => {
		const cat = new StaticAssetCatalog({
			entries: [
				{ id: 1, url: "/a.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 },
				{ id: 1, url: "/a.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 },
			],
		});
		expect(cat.size()).toBe(1);
	});

	it("urls() returns each distinct URL once for prefetch", () => {
		const cat = new StaticAssetCatalog({
			entries: [
				{ id: 1, url: "/sheet.png", atlasCols: 4, tileSize: 32, atlasIndex: 0 },
				{ id: 2, url: "/sheet.png", atlasCols: 4, tileSize: 32, atlasIndex: 1 },
				{ id: 3, url: "/other.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 },
			],
		});
		const urls = cat.urls();
		expect(urls).toContain("/sheet.png");
		expect(urls).toContain("/other.png");
		expect(urls).toHaveLength(2);
	});

	it("urls() omits empty strings (entries with no sprite)", () => {
		const cat = new StaticAssetCatalog({
			entries: [
				{ id: 1, url: "", atlasCols: 1, tileSize: 32, atlasIndex: 0 },
				{ id: 2, url: "/x.png", atlasCols: 1, tileSize: 32, atlasIndex: 0 },
			],
		});
		expect(cat.urls()).toEqual(["/x.png"]);
	});

	it("animationByID returns undefined (editors don't tick anim clock)", () => {
		const cat = new StaticAssetCatalog({ entries: [] });
		expect(cat.animationByID?.(1, 0)).toBeUndefined();
	});
});
