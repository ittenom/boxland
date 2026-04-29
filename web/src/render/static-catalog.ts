// Boxland — static (in-memory) asset catalog.
//
// `RemoteAssetCatalog` (in @game) lazy-fetches a CatalogAsset row over
// HTTP per id. That's the right shape for the live game / sandbox,
// where the server pushes which entities exist via FlatBuffers diffs
// and the client backfills sprite metadata on demand.
//
// The design-tool editors (level editor, mapmaker) have a different
// shape: the templ shell already bakes the full catalog of placeable
// entity types — sprite URL, atlas index, atlas cols, tile size — into
// the page as `data-bx-*` attributes on the palette. The editor knows
// every asset it can possibly need at boot time, synchronously.
//
// `StaticAssetCatalog` is the renderer-facing adapter for that case.
// Construct it with a list of `(asset_id, url, atlasCols, atlasRows,
// tileSize)` rows, and it implements the @render `AssetCatalog`
// interface against an in-memory map. No HTTP, no animation manifest,
// no surprises — frame 0 only, since editors render static previews.
//
// Why a separate file rather than inlining into entry-{level-editor,
// mapmaker}.ts: both editors will use it; tilemap viewer / picker
// previews could reuse it later; and isolating the math means we can
// unit-test it without standing up Pixi.

import type {
	AnimationFrame,
	AssetCatalog,
	AssetId,
	AnimId,
	AnimationLookup,
} from "./types";

/** One entry in the static catalog. Shape mirrors the templ palette
 *  data-bx-* attributes so wiring is just `Number(el.dataset.bx*)`. */
export interface StaticCatalogEntry {
	/** Stable asset id the renderer references. Editors tend to reuse
	 *  the entity_type_id here since the two id-spaces don't collide
	 *  inside one editor instance, and conflating them keeps the
	 *  Renderable.asset_id wire shape consistent with the sandbox
	 *  (where asset_id == typeId, see entry-sandbox.ts adaptRenderer). */
	id: AssetId;
	/** Same-origin URL (typically /design/assets/blob/{id}). */
	url: string;
	/** Atlas grid in columns/rows. 1×1 for plain single sprites. */
	atlasCols: number;
	/** Cell size in source pixels. 32 is canonical Boxland. */
	tileSize: number;
	/** Cell index within the atlas, [0, atlasCols * atlasRows). */
	atlasIndex: number;
}

/**
 * Build options.
 *
 * `entries` are upserted by id; the same id appearing twice with
 * different metadata is undefined behaviour and will throw — that's
 * almost always a server bug worth surfacing loudly rather than a
 * legitimate use case.
 */
export interface StaticAssetCatalogOptions {
	entries: readonly StaticCatalogEntry[];
}

export class StaticAssetCatalog implements AssetCatalog {
	private readonly byID: Map<AssetId, StaticCatalogEntry>;

	constructor(opts: StaticAssetCatalogOptions) {
		this.byID = new Map();
		for (const e of opts.entries) {
			const existing = this.byID.get(e.id);
			if (existing && (existing.url !== e.url || existing.atlasIndex !== e.atlasIndex || existing.atlasCols !== e.atlasCols || existing.tileSize !== e.tileSize)) {
				throw new Error(
					`StaticAssetCatalog: duplicate id ${e.id} with conflicting metadata; ` +
					`existing=${JSON.stringify(existing)} new=${JSON.stringify(e)}`,
				);
			}
			this.byID.set(e.id, e);
		}
	}

	urlFor(asset_id: AssetId, _variant_id?: number): string {
		// Variants are intentionally ignored — editors render the base
		// art only. (When/if variants land in editor previews, the
		// AssetCatalog interface allows returning a different URL per
		// variant and we'd extend the entry shape to carry them.)
		return this.byID.get(asset_id)?.url ?? "";
	}

	frame(asset_id: AssetId, _anim_id: AnimId, frame: number): AnimationFrame | undefined {
		const e = this.byID.get(asset_id);
		if (!e) return undefined;
		// Editors render frame 0 of anim 0 — but if the caller passes
		// a different (anim_id, frame) we still return the cell at
		// `atlasIndex` rather than overcomplicate things. The atlas
		// index *is* the frame index for our purposes.
		const cols = Math.max(1, e.atlasCols);
		const ts = Math.max(1, e.tileSize);
		// `frame` is allowed to override atlasIndex when the caller
		// wants to scrub through a sprite sheet (e.g. the character-
		// generator preview); v0 editors won't do that, but baking it
		// in costs nothing and earns flexibility.
		const idx = Number.isFinite(frame) && frame > 0 ? frame : e.atlasIndex;
		const col = idx % cols;
		const row = Math.floor(idx / cols);
		return {
			asset_id, anim_id: _anim_id, frame: idx,
			sx: col * ts, sy: row * ts, sw: ts, sh: ts,
			ax: 0, ay: 0,
		};
	}

	/** Static catalogs don't carry animation timing. The optional
	 *  `animationByID` is intentionally unimplemented so the Scene's
	 *  AnimClock no-ops on these renderables (frame index passes
	 *  through unmodified). */
	animationByID?(_asset_id: AssetId, _anim_id: AnimId): AnimationLookup | undefined {
		return undefined;
	}

	/** Number of entries — handy for tests and for loading-progress
	 *  HUDs that want to show "loaded N/M sprites". */
	size(): number {
		return this.byID.size;
	}

	/** Return the raw static entry. Editor-only adapters use this when
	 *  they need to mirror PaletteGrid's direct atlas slicing instead
	 *  of going through the animation-oriented AssetCatalog.frame API. */
	entryFor(asset_id: AssetId): StaticCatalogEntry | undefined {
		return this.byID.get(asset_id);
	}

	/** Iterate every URL in the catalog. The caller can pass the
	 *  result to `Promise.all(urls.map(loadTextureAsset))` to warm
	 *  Pixi's texture cache before the first frame, so the editor
	 *  doesn't flash empty cells while textures stream in. */
	urls(): readonly string[] {
		const out: string[] = [];
		const seen = new Set<string>();
		for (const e of this.byID.values()) {
			if (e.url && !seen.has(e.url)) {
				seen.add(e.url);
				out.push(e.url);
			}
		}
		return out;
	}
}
