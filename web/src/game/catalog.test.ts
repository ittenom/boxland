import { describe, expect, it } from "vitest";
import { RemoteAssetCatalog, type CatalogAsset } from "./catalog";

function fakeAsset(over: Partial<CatalogAsset> = {}): CatalogAsset {
	return {
		id: 1,
		name: "hero",
		kind: "sprite",
		url: "https://cdn.test/assets/aa/bb/hero",
		grid_w: 32,
		grid_h: 32,
		cols: 4,
		rows: 4,
		frame_count: 16,
		animations: [
			{ id: 100, name: "walk_north", frame_from: 0, frame_to: 3, fps: 8, direction: "forward" },
			{ id: 101, name: "walk_east", frame_from: 4, frame_to: 7, fps: 8, direction: "forward" },
			{ id: 200, name: "idle", frame_from: 8, frame_to: 8, fps: 1, direction: "forward" },
		],
		...over,
	};
}

/** mockFetch returns a fetchImpl that records every URL it sees and
 *  responds with the supplied catalogs. */
function mockFetch(map: Map<string, CatalogAsset>): {
	calls: string[];
	fetchImpl: typeof fetch;
} {
	const calls: string[] = [];
	const fetchImpl = (async (input: string | URL | Request) => {
		const url = typeof input === "string" ? input : input.toString();
		calls.push(url);
		const idsParam = new URL(url, "http://x").searchParams.get("ids") ?? "";
		const ids = idsParam.split(",").map((s) => Number(s)).filter(Number.isFinite);
		const assets = ids.map((id) => map.get(String(id))).filter((a): a is CatalogAsset => !!a);
		return new Response(JSON.stringify({ assets }), {
			status: 200,
			headers: { "content-type": "application/json" },
		});
	}) as unknown as typeof fetch;
	return { calls, fetchImpl };
}

describe("RemoteAssetCatalog", () => {
	it("fetches once for batched ensures, twice for distinct sets", async () => {
		const map = new Map<string, CatalogAsset>([
			["1", fakeAsset({ id: 1 })],
			["2", fakeAsset({ id: 2, name: "boss" })],
			["3", fakeAsset({ id: 3, name: "tile" })],
		]);
		const { calls, fetchImpl } = mockFetch(map);
		const cat = new RemoteAssetCatalog({ fetchImpl });

		await cat.ensure([1, 2]);
		expect(calls.length).toBe(1);
		expect(cat.has(1)).toBe(true);
		expect(cat.has(2)).toBe(true);

		// Second ensure with the same ids must NOT refetch.
		await cat.ensure([1, 2]);
		expect(calls.length).toBe(1);

		// Adding a new id triggers exactly one new request, only for the new id.
		await cat.ensure([2, 3]);
		expect(calls.length).toBe(2);
		expect(calls[1]).toContain("ids=3");
	});

	it("coalesces concurrent ensures without double-fetching", async () => {
		const map = new Map<string, CatalogAsset>([["1", fakeAsset({ id: 1 })]]);
		const { calls, fetchImpl } = mockFetch(map);
		const cat = new RemoteAssetCatalog({ fetchImpl });
		await Promise.all([cat.ensure([1]), cat.ensure([1]), cat.ensure([1])]);
		// Three concurrent calls for the same id → one HTTP hit.
		expect(calls.length).toBe(1);
	});

	it("urlFor returns CDN URL after ensure, empty string before", async () => {
		const map = new Map<string, CatalogAsset>([["1", fakeAsset({ id: 1 })]]);
		const { fetchImpl } = mockFetch(map);
		const cat = new RemoteAssetCatalog({ fetchImpl });
		expect(cat.urlFor(1)).toBe(""); // not yet loaded
		await cat.ensure([1]);
		expect(cat.urlFor(1)).toContain("https://cdn.test");
	});

	it("frame() resolves the source rect using the animation's frame_from", async () => {
		const map = new Map<string, CatalogAsset>([["1", fakeAsset({ id: 1 })]]);
		const { fetchImpl } = mockFetch(map);
		const cat = new RemoteAssetCatalog({ fetchImpl });
		await cat.ensure([1]);
		// walk_east is anim 101 covering frames 4..7. Frame index 0
		// inside the animation = absolute frame 4 = column 0, row 1.
		const f = cat.frame(1, 101, 0)!;
		expect(f.sx).toBe(0);
		expect(f.sy).toBe(32);
		expect(f.sw).toBe(32);
		expect(f.sh).toBe(32);
	});

	it("frame() returns undefined for unknown asset ids", async () => {
		const cat = new RemoteAssetCatalog({ fetchImpl: mockFetch(new Map()).fetchImpl });
		expect(cat.frame(999, 1, 0)).toBeUndefined();
	});

	it("missing ids on the server are remembered so we don't refetch", async () => {
		const { calls, fetchImpl } = mockFetch(new Map()); // server returns empty
		const cat = new RemoteAssetCatalog({ fetchImpl });
		await cat.ensure([99]);
		await cat.ensure([99]);
		await cat.ensure([99, 100]);
		// First ensure fetched [99]; second was a no-op (already missing);
		// third should have requested only [100], not [99, 100].
		expect(calls.length).toBe(2);
		expect(calls[1]).not.toContain("99");
		expect(calls[1]).toContain("100");
	});

	it("animationByName resolves case-insensitively", async () => {
		const map = new Map<string, CatalogAsset>([["1", fakeAsset({ id: 1 })]]);
		const { fetchImpl } = mockFetch(map);
		const cat = new RemoteAssetCatalog({ fetchImpl });
		await cat.ensure([1]);
		expect(cat.animationByName(1, "walk_east")?.id).toBe(101);
		expect(cat.animationByName(1, "WALK_EAST")?.id).toBe(101);
		expect(cat.animationByName(1, "missing")).toBeUndefined();
	});

	it("animationByID returns the row including fps + direction", async () => {
		const map = new Map<string, CatalogAsset>([["1", fakeAsset({ id: 1 })]]);
		const { fetchImpl } = mockFetch(map);
		const cat = new RemoteAssetCatalog({ fetchImpl });
		await cat.ensure([1]);
		const a = cat.animationByID(1, 101)!;
		expect(a.fps).toBe(8);
		expect(a.direction).toBe("forward");
	});
});
