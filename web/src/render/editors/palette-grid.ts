// Boxland — palette grid widget.
//
// Theme-skinned grid of selectable entity-type entries. Used by
// the mapmaker (tile classes) and the level editor (placeable
// classes). Each entry shows a 32x32 thumbnail clipped to the
// entity's atlas cell + an optional name label.
//
// Selection produces a single highlighted entry (yellow halo via
// pixi-filters `OutlineFilter`). Clicks are routed through the
// caller's onSelect callback; the entry script translates that
// into a "set active palette entry" state change.

import "./layout-init";
import { Container, Graphics, Sprite, Texture, Rectangle } from "pixi.js";
import { OutlineFilter } from "pixi-filters";

import { loadTextureAsset } from "../asset-texture";
import type { Theme } from "../ui";
import { NineSlice, Roles } from "../ui";

/** One entry the grid renders. The atlas math + URL come from the
 *  surface's snapshot; the grid is dumb — it just paints. */
export interface PaletteEntry {
	id: number;
	name: string;
	spriteUrl: string;
	atlasIndex: number;
	atlasCols: number;
	tileSize: number;
}

export interface PaletteGridOptions {
	theme: Theme;
	width: number;
	height: number;
	cellSize?: number;       // px per tile preview; default 36
	gap?: number;            // gap between cells in px; default 4
	onSelect?: (entry: PaletteEntry) => void;
}

const DEFAULT_CELL = 36;
const DEFAULT_GAP = 4;
const HALO_COLOR = 0xffd84a;

export class PaletteGrid extends Container {
	private readonly theme: Theme;
	private readonly cellSize: number;
	private readonly gap: number;
	private readonly onSelect: ((e: PaletteEntry) => void) | null;
	private readonly cells = new Map<number, PaletteCell>();
	private selectedID: number | null = null;
	private bg: NineSlice;
	private readonly clip = new Graphics();

	constructor(opts: PaletteGridOptions) {
		super();
		this.theme = opts.theme;
		this.cellSize = opts.cellSize ?? DEFAULT_CELL;
		this.gap = opts.gap ?? DEFAULT_GAP;
		this.onSelect = opts.onSelect ?? null;

		this.layout = {
			width: opts.width,
			height: opts.height,
			flexDirection: "row",
			flexWrap: "wrap",
			alignContent: "flex-start",
			gap: this.gap,
			padding: 6,
		};

		this.bg = new NineSlice({
			theme: opts.theme,
			role: Roles.FrameLite,
			width: opts.width,
			height: opts.height,
		});
		this.bg.layout = {
			position: "absolute",
			top: 0,
			left: 0,
		};
		this.addChild(this.bg);
		this.redrawClip(opts.width, opts.height);
		this.addChild(this.clip);
		this.mask = this.clip;
	}

	/** Replace the entry list. Existing cell containers are reused
	 *  when their id matches; new ids get a fresh cell; dropped ids
	 *  are destroyed. */
	setEntries(entries: readonly PaletteEntry[]): void {
		const seen = new Set<number>();
		for (const e of entries) {
			seen.add(e.id);
			let cell = this.cells.get(e.id);
			if (!cell) {
				cell = new PaletteCell({
					theme: this.theme,
					entry: e,
					size: this.cellSize,
					onClick: () => this.select(e.id),
				});
				this.cells.set(e.id, cell);
				this.addChild(cell);
			} else {
				cell.update(e);
			}
		}
		for (const [id, cell] of this.cells) {
			if (!seen.has(id)) {
				this.removeChild(cell);
				cell.destroy();
				this.cells.delete(id);
			}
		}
		// Reapply selection highlight in case the selected id is
		// still present in the new entry set.
		this.applySelection();
	}

	/** Programmatic selection. Mirrors a click on the cell. */
	select(id: number): void {
		if (this.selectedID === id) return;
		this.selectedID = id;
		this.applySelection();
		const entry = this.cells.get(id)?.entry;
		if (entry && this.onSelect) this.onSelect(entry);
	}

	/** Read-only access for surfaces that need to query the
	 *  current selection (e.g. on undo). */
	selectedId(): number | null { return this.selectedID; }

	private applySelection(): void {
		for (const [id, cell] of this.cells) {
			cell.setSelected(id === this.selectedID);
		}
	}

	/** Resize to new dims. */
	resize(width: number, height: number): void {
		this.layout = {
			width, height,
			flexDirection: "row",
			flexWrap: "wrap",
			alignContent: "flex-start",
			gap: this.gap,
			padding: 6,
		};
		this.bg.resize(width, height);
		this.redrawClip(width, height);
	}

	private redrawClip(width: number, height: number): void {
		this.clip.clear().rect(0, 0, width, height).fill(0xffffff);
	}
}

interface PaletteCellOptions {
	theme: Theme;
	entry: PaletteEntry;
	size: number;
	onClick: () => void;
}

class PaletteCell extends Container {
	entry: PaletteEntry;
	private readonly theme: Theme;
	private readonly size: number;
	private readonly thumb: Sprite;
	private readonly halo: Graphics;
	private bg: NineSlice;
	private readonly outline = new OutlineFilter({ thickness: 1, color: HALO_COLOR });

	constructor(opts: PaletteCellOptions) {
		super();
		this.theme = opts.theme;
		this.size = opts.size;
		this.entry = opts.entry;

		this.layout = { width: opts.size, height: opts.size };
		this.eventMode = "static";
		this.cursor = "pointer";

		this.bg = new NineSlice({
			theme: opts.theme,
			role: Roles.SlotAvailable,
			width: opts.size,
			height: opts.size,
		});
		this.addChild(this.bg);

		this.thumb = new Sprite();
		this.thumb.position.set(2, 2);
		this.addChild(this.thumb);

		this.halo = new Graphics();
		this.halo.visible = false;
		this.addChild(this.halo);
		this.drawHalo();

		this.loadThumb(opts.entry);

		this.on("pointertap", () => opts.onClick());
	}

	update(entry: PaletteEntry): void {
		this.entry = entry;
		this.loadThumb(entry);
	}

	setSelected(on: boolean): void {
		this.halo.visible = on;
		// Replace the bg with the "selected" frame when on. We
		// don't rebuild the NineSlice; instead, swap the role via
		// a fresh instance only when state actually changes.
		const role = on ? Roles.SlotSelected : Roles.SlotAvailable;
		const nextEntry = (this.bg as unknown as { ["__bxRole"]?: string }).__bxRole;
		if (nextEntry === role) return;
		this.removeChild(this.bg);
		this.bg.destroy();
		this.bg = new NineSlice({
			theme: this.theme,
			role,
			width: this.size,
			height: this.size,
		});
		(this.bg as unknown as { ["__bxRole"]?: string }).__bxRole = role;
		this.addChildAt(this.bg, 0);
		// Filter for emphasis on the thumb itself.
		this.thumb.filters = on ? [this.outline] : null;
	}

	private drawHalo(): void {
		this.halo.clear();
		this.halo
			.rect(-1, -1, this.size + 2, this.size + 2)
			.stroke({ color: HALO_COLOR, width: 2, alignment: 1 });
	}

	private loadThumb(entry: PaletteEntry): void {
		if (!entry.spriteUrl) return;
		void loadTextureAsset(entry.spriteUrl).then((base) => {
			if (this.destroyed || !base || !base.source) return;
			base.source.scaleMode = "nearest";
			const ts = entry.tileSize || 32;
			const cols = Math.max(1, entry.atlasCols);
			const sx = (entry.atlasIndex % cols) * ts;
			const sy = Math.floor(entry.atlasIndex / cols) * ts;
			this.thumb.texture = new Texture({
				source: base.source,
				frame: new Rectangle(sx, sy, ts, ts),
			});
			// Scale the thumb to fit cellSize - 4px padding.
			const scale = (this.size - 4) / ts;
			this.thumb.scale.set(scale);
		}).catch(() => { /* keep placeholder */ });
	}
}
