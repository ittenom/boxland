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
const PAD = 8;
const SCROLL_W = 12;
const QUIET_FILL = 0x101827;
const QUIET_STROKE = 0x31415f;

export class PaletteGrid extends Container {
	private readonly theme: Theme;
	private readonly cellSize: number;
	private readonly gap: number;
	private readonly onSelect: ((e: PaletteEntry) => void) | null;
	private readonly cells = new Map<number, PaletteCell>();
	private entries: PaletteEntry[] = [];
	private selectedID: number | null = null;
	private readonly bg = new Graphics();
	private readonly content = new Container();
	private readonly scrollTrack: NineSlice;
	private readonly scrollHandle: NineSlice;
	private widthPx: number;
	private heightPx: number;
	private scrollY = 0;
	private contentHeight = 0;
	private drag: { startY: number; startScroll: number } | null = null;

	constructor(opts: PaletteGridOptions) {
		super();
		this.theme = opts.theme;
		this.cellSize = opts.cellSize ?? DEFAULT_CELL;
		this.gap = opts.gap ?? DEFAULT_GAP;
		this.onSelect = opts.onSelect ?? null;
		this.widthPx = opts.width;
		this.heightPx = opts.height;

		this.layout = {
			width: opts.width,
			height: opts.height,
		};
		this.eventMode = "static";
		this.cursor = "default";
		this.hitArea = new Rectangle(0, 0, opts.width, opts.height);

		this.bg.layout = {
			position: "absolute",
			top: 0,
			left: 0,
		};
		this.addChild(this.bg);
		this.addChild(this.content);
		this.redrawPanel(opts.width, opts.height);

		this.scrollTrack = new NineSlice({
			theme: opts.theme,
			role: Roles.ScrollBar,
			width: SCROLL_W,
			height: Math.max(1, opts.height - PAD * 2),
			fallbackColor: 0x244269,
		});
		this.scrollTrack.position.set(opts.width - PAD - SCROLL_W, PAD);
		this.addChild(this.scrollTrack);
		this.scrollHandle = new NineSlice({
			theme: opts.theme,
			role: Roles.ScrollHandle,
			width: SCROLL_W,
			height: 48,
			fallbackColor: 0x5e8cff,
		});
		this.addChild(this.scrollHandle);
		this.scrollHandle.eventMode = "static";
		this.scrollHandle.cursor = "grab";
		this.scrollHandle.on("pointerdown", (ev: { global: { y: number }; stopPropagation?: () => void }) => {
			this.drag = { startY: ev.global.y, startScroll: this.scrollY };
			this.scrollHandle.cursor = "grabbing";
			ev.stopPropagation?.();
		});
		this.on("pointermove", (ev: { global: { y: number } }) => {
			if (!this.drag) return;
			const viewportH = Math.max(1, this.heightPx - PAD * 2);
			const maxScroll = Math.max(0, this.contentHeight - viewportH);
			const handleH = Math.max(28, Math.floor((viewportH / this.contentHeight) * viewportH));
			const travel = Math.max(1, viewportH - handleH);
			this.setScroll(this.drag.startScroll + ((ev.global.y - this.drag.startY) / travel) * maxScroll);
		});
		this.on("pointerup", () => {
			this.drag = null;
			this.scrollHandle.cursor = "grab";
		});
		this.on("pointerupoutside", () => {
			this.drag = null;
			this.scrollHandle.cursor = "grab";
		});
		this.on("wheel", (ev: { deltaY?: number; preventDefault?: () => void; stopPropagation?: () => void }) => {
			this.setScroll(this.scrollY + (ev.deltaY ?? 0));
			ev.stopPropagation?.();
			ev.preventDefault?.();
		});
	}

	/** Replace the entry list. Existing cell containers are reused
	 *  when their id matches; new ids get a fresh cell; dropped ids
	 *  are destroyed. */
	setEntries(entries: readonly PaletteEntry[]): void {
		this.entries = [...entries];
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
				this.content.addChild(cell);
			} else {
				cell.update(e);
			}
		}
		for (const [id, cell] of this.cells) {
			if (!seen.has(id)) {
				this.content.removeChild(cell);
				cell.destroy();
				this.cells.delete(id);
			}
		}
		this.layoutCells();
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
		this.widthPx = width;
		this.heightPx = height;
		this.layout = {
			width, height,
		};
		this.hitArea = new Rectangle(0, 0, width, height);
		this.redrawPanel(width, height);
		this.scrollTrack.position.set(width - PAD - SCROLL_W, PAD);
		this.scrollTrack.resize(SCROLL_W, Math.max(1, height - PAD * 2));
		this.layoutCells();
	}

	private redrawPanel(width: number, height: number): void {
		this.bg.clear()
			.rect(0, 0, width, height)
			.fill(QUIET_FILL)
			.rect(0, 0, width, height)
			.stroke({ color: QUIET_STROKE, width: 1, alignment: 1 })
			.rect(PAD - 1, PAD - 1, Math.max(1, width - PAD * 3 - SCROLL_W + 2), Math.max(1, height - PAD * 2 + 2))
			.stroke({ color: 0x1f2f49, width: 1, alignment: 1 });
	}

	private layoutCells(): void {
		const usableW = Math.max(this.cellSize, this.widthPx - PAD * 3 - SCROLL_W);
		const cols = Math.max(1, Math.floor((usableW + this.gap) / (this.cellSize + this.gap)));
		for (let i = 0; i < this.entries.length; i++) {
			const cell = this.cells.get(this.entries[i]!.id);
			if (!cell) continue;
			const x = PAD + (i % cols) * (this.cellSize + this.gap);
			const y = PAD + Math.floor(i / cols) * (this.cellSize + this.gap);
			cell.layoutBaseY = y;
			cell.position.set(x, y - Math.round(this.scrollY));
		}
		const rows = Math.ceil(this.entries.length / cols);
		this.contentHeight = PAD * 2 + rows * this.cellSize + Math.max(0, rows - 1) * this.gap;
		this.setScroll(this.scrollY);
	}

	private setScroll(next: number): void {
		const viewportH = Math.max(1, this.heightPx - PAD * 2);
		const maxScroll = Math.max(0, this.contentHeight - viewportH);
		this.scrollY = Math.max(0, Math.min(maxScroll, next));
		const scroll = Math.round(this.scrollY);
		for (const cell of this.cells.values()) {
			const y = cell.layoutBaseY;
			const top = y - scroll;
			cell.position.y = top;
			cell.visible = top >= PAD && top + this.cellSize <= PAD + viewportH;
		}

		const show = maxScroll > 0;
		this.scrollTrack.visible = show;
		this.scrollHandle.visible = show;
		if (!show) return;
		const trackH = viewportH;
		const handleH = Math.max(28, Math.floor((viewportH / this.contentHeight) * trackH));
		const travel = Math.max(0, trackH - handleH);
		const ratio = maxScroll === 0 ? 0 : this.scrollY / maxScroll;
		this.scrollHandle.resize(SCROLL_W, handleH);
		this.scrollHandle.position.set(this.widthPx - PAD - SCROLL_W, PAD + Math.round(travel * ratio));
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
	layoutBaseY = 0;
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
		(this.bg as unknown as { ["__bxRole"]?: string }).__bxRole = Roles.SlotAvailable;
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
