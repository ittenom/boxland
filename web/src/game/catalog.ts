// Boxland — game/catalog.ts
//
// Two catalogs live here:
//
//   * RemoteAssetCatalog — production. Fetches /play/asset-catalog on
//     demand, batches concurrent ensures into one HTTP hit, and answers
//     `urlFor` + `frame()` from an in-memory table. Returns `undefined`
//     for frames it doesn't know yet so the renderer skips the draw
//     rather than rendering placeholder magenta — undefined frames go
//     away as soon as the next ensure() resolves.
//
//   * PlaceholderCatalog — preserved for tests + Mapmaker preview
//     surfaces that don't have a server connection. Returns a 1×1
//     magenta tile so unconfigured art is visually obvious.
//
// PLAN.md §1 "content-addressed CDN URLs": URLs returned by the catalog
// are the absolute CDN-fronted public URLs; TextureCache treats them
// as immutable per content sha (fine to long-cache).

import type { AnimationFrame, AssetCatalog, AnimId, AssetId } from "@render";

const MAGENTA_DATA_URL =
	"data:image/png;base64," +
	// 1x1 magenta PNG — visible at any zoom so missing-art shows up.
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==";

/** Minimal placeholder catalog. Every (asset_id) maps to the same
 *  magenta tile; every frame returns a 1x1 source rect. Used by
 *  unit tests and the Mapmaker preview surface where there's no
 *  server connection to fetch a real catalog from. */
export class PlaceholderCatalog implements AssetCatalog {
	urlFor(_assetId: number, _variantId?: number): string {
		return MAGENTA_DATA_URL;
	}
	frame(assetId: number, animId: number, frame: number): AnimationFrame | undefined {
		return {
			asset_id: assetId,
			anim_id: animId,
			frame,
			sx: 0,
			sy: 0,
			sw: 1,
			sh: 1,
			ax: 0,
			ay: 0,
		};
	}
}

// ---- RemoteAssetCatalog -----------------------------------------------

/** One animation row as returned by /play/asset-catalog. Mirrors the
 *  Go catalogAnimation struct. Names are server-canonical
 *  (`walk_north`, `idle`, …) so the client uses the same lookup
 *  table the server-side picker does. */
export interface CatalogAnimation {
	id: number;
	name: string;
	frame_from: number;
	frame_to: number;
	fps: number;
	direction: "forward" | "reverse" | "pingpong";
}

/** One asset record from /play/asset-catalog. */
export interface CatalogAsset {
	id: number;
	name: string;
	kind: string;
	url: string;
	grid_w: number;
	grid_h: number;
	cols: number;
	rows: number;
	frame_count: number;
	animations: CatalogAnimation[];
}

interface CatalogResponse {
	assets: CatalogAsset[];
}

export interface RemoteAssetCatalogOptions {
	/** Endpoint URL. Defaults to "/play/asset-catalog". */
	baseURL?: string;
	/** Override fetch (tests). Same signature as window.fetch. */
	fetchImpl?: typeof fetch;
	/** Max ids per request — should match the server cap. */
	maxBatch?: number;
}

const DEFAULT_BASE = "/play/asset-catalog";
const DEFAULT_BATCH = 256; // matches MaxCatalogIDs server-side

/**
 * Production catalog. Reads from the server endpoint, batches concurrent
 * ensures into one fetch per microtask burst, caches everything in
 * memory.
 *
 * Lookup contract:
 *   * `urlFor(id)` returns the empty string when the id hasn't been
 *     fetched yet — TextureCache treats that as "skip" and the next
 *     paint after `ensure()` resolves picks up the right URL.
 *   * `frame(id, anim, frame)` returns undefined for unknown ids OR
 *     unknown anims. Renderer skips draws on undefined.
 *
 * Anim lookup is by anim id (the persisted asset_animations.id from the
 * server), matching the wire protocol. The catalog also indexes by
 * lowercased name so future client-side picks (e.g. spectator
 * fallback when the server hasn't computed a clip) can resolve
 * `walk_east` directly.
 */
export class RemoteAssetCatalog implements AssetCatalog {
	private readonly baseURL: string;
	private readonly maxBatch: number;
	private readonly fetchImpl: typeof fetch;

	private readonly assets = new Map<AssetId, CatalogAsset>();
	private readonly animsByID = new Map<AssetId, Map<AnimId, CatalogAnimation>>();
	private readonly animsByName = new Map<AssetId, Map<string, CatalogAnimation>>();
	/** Per-id pending promise so concurrent ensure([1,2]) + ensure([2,3])
	 *  collapse into the right number of fetches without double-fetching id 2. */
	private readonly pending = new Map<AssetId, Promise<void>>();
	/** Ids known to be missing on the server — don't re-request them. */
	private readonly missing = new Set<AssetId>();

	constructor(opts: RemoteAssetCatalogOptions = {}) {
		this.baseURL = opts.baseURL ?? DEFAULT_BASE;
		this.maxBatch = Math.max(1, opts.maxBatch ?? DEFAULT_BATCH);
		this.fetchImpl = opts.fetchImpl ?? globalThis.fetch.bind(globalThis);
	}

	/** Ensure the catalog has rows for every id. Concurrent calls with
	 *  overlapping ids are coalesced into a single HTTP request per
	 *  unique missing id; hits the server at most once even if called
	 *  100 times in the same tick. Resolves when all requested ids are
	 *  loaded (or marked missing). */
	async ensure(ids: AssetId[]): Promise<void> {
		const wanted = new Set<AssetId>();
		for (const id of ids) {
			if (id <= 0) continue;
			if (this.assets.has(id) || this.missing.has(id)) continue;
			wanted.add(id);
		}
		if (wanted.size === 0) return;

		// Wait for any in-flight fetch covering one of the wanted ids,
		// AND fire a fresh fetch for the rest.
		const waits: Array<Promise<void>> = [];
		const toFetch: AssetId[] = [];
		for (const id of wanted) {
			const p = this.pending.get(id);
			if (p) {
				waits.push(p);
			} else {
				toFetch.push(id);
			}
		}
		if (toFetch.length > 0) {
			// Slice into max-batch chunks (the server caps incoming
			// id lists; clients shouldn't send oversized requests).
			for (let i = 0; i < toFetch.length; i += this.maxBatch) {
				const slice = toFetch.slice(i, i + this.maxBatch);
				const p = this.fetchBatch(slice);
				for (const id of slice) this.pending.set(id, p);
				waits.push(p);
			}
		}
		await Promise.all(waits);
	}

	urlFor(assetId: AssetId, _variantId?: number): string {
		// variantId is ignored for v1 — palette variants live as
		// separate baked PNGs which the bake job will register as
		// distinct asset rows; the catalog already returns each
		// row's URL on its own.
		const a = this.assets.get(assetId);
		return a?.url ?? "";
	}

	frame(assetId: AssetId, animId: AnimId, frame: number): AnimationFrame | undefined {
		const asset = this.assets.get(assetId);
		if (!asset || asset.grid_w <= 0 || asset.cols <= 0) return undefined;

		const anim = this.animsByID.get(assetId)?.get(animId);
		// Compute the absolute frame index. Without an animation row we
		// fall through to "first frame" — better than nothing, lets
		// tile / single-cell sprites render even when the server didn't
		// resolve an anim_id. The frame parameter is interpreted as
		// "frames since frame_from" when an animation is known, and as
		// an absolute index otherwise.
		let absoluteFrame = frame;
		if (anim) {
			absoluteFrame = anim.frame_from + Math.max(0, Math.min(frame, anim.frame_to - anim.frame_from));
		}
		if (absoluteFrame < 0 || absoluteFrame >= asset.frame_count) {
			absoluteFrame = 0;
		}
		const col = absoluteFrame % asset.cols;
		const row = Math.floor(absoluteFrame / asset.cols);
		return {
			asset_id: assetId,
			anim_id: animId,
			frame: absoluteFrame,
			sx: col * asset.grid_w,
			sy: row * asset.grid_h,
			sw: asset.grid_w,
			sh: asset.grid_h,
			ax: 0,
			ay: 0,
		};
	}

	/** Resolve an animation by name on a given sheet. Used by the
	 *  client-side animation clock to look up (fps, direction) and by
	 *  any future client fallback that picks `walk_east` directly. */
	animationByName(assetId: AssetId, name: string): CatalogAnimation | undefined {
		return this.animsByName.get(assetId)?.get(name.toLowerCase());
	}

	/** Resolve an animation by its server-assigned id. Used by the
	 *  client-side frame clock to step the cycle at the right fps. */
	animationByID(assetId: AssetId, animId: AnimId): CatalogAnimation | undefined {
		return this.animsByID.get(assetId)?.get(animId);
	}

	/** Test helper: is this id known to the catalog? */
	has(assetId: AssetId): boolean {
		return this.assets.has(assetId);
	}

	private async fetchBatch(ids: AssetId[]): Promise<void> {
		const url = `${this.baseURL}?ids=${ids.join(",")}`;
		try {
			const resp = await this.fetchImpl(url, { credentials: "same-origin" });
			if (!resp.ok) {
				// Mark as missing so we don't hot-loop on a 4xx / 5xx;
				// a fresh ensure() will retry only after a manual
				// invalidate (out of scope for v1 — publish flow
				// triggers a hard-reload of the page).
				for (const id of ids) this.missing.add(id);
				return;
			}
			const body = (await resp.json()) as CatalogResponse;
			const seen = new Set<AssetId>();
			for (const a of body.assets ?? []) {
				this.absorb(a);
				seen.add(a.id);
			}
			// Server silently drops missing ids — record them so we
			// don't re-request next ensure() call.
			for (const id of ids) {
				if (!seen.has(id)) this.missing.add(id);
			}
		} catch {
			for (const id of ids) this.missing.add(id);
		} finally {
			for (const id of ids) this.pending.delete(id);
		}
	}

	private absorb(a: CatalogAsset): void {
		this.assets.set(a.id, a);
		const byID = new Map<AnimId, CatalogAnimation>();
		const byName = new Map<string, CatalogAnimation>();
		for (const anim of a.animations ?? []) {
			byID.set(anim.id, anim);
			byName.set(anim.name.toLowerCase(), anim);
		}
		this.animsByID.set(a.id, byID);
		this.animsByName.set(a.id, byName);
	}
}
