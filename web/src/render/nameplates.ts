// Boxland — render/nameplates.ts
//
// Nameplate + HP-bar layer. PLAN.md §6h: render the EntityState
// `nameplate` string and `hp_pct` percentage when present. Everything
// renders in a separate Container so the entity sprites can be
// reordered freely without disturbing the overlay tree.
//
// Tests run headlessly through Pixi's classes which work fine in
// Node provided we never touch the GPU; rendering math is in this file
// (positionFor/barWidth) so unit tests can assert without standing up
// a renderer.

import { Container, Graphics, Text, type ContainerChild } from "pixi.js";

import type { Camera, EntityId, Renderable } from "./types";
import { SUB_PER_PX } from "./types";
import { worldToScreen, type ViewportLayout } from "./viewport";

/** Sentinel values from world.fbs:
 *   * EntityState.hp_pct = 255 -> "no HP bar"
 *   * EntityState.nameplate = "" -> "no nameplate" */
export const NO_HP_BAR = 255;

/** Default styling. Centralized so the future Settings page can
 *  expose font + colors without spelunking through render code. */
export const DEFAULT_NAMEPLATE_FONT_PX = 12;
export const NAMEPLATE_OFFSET_PX = 28;       // px above the sprite anchor
export const HP_BAR_WIDTH_PX     = 32;
export const HP_BAR_HEIGHT_PX    = 3;
export const HP_BAR_OFFSET_PX    = 22;
export const HP_BAR_BG_COLOR     = 0x141028;
export const HP_BAR_FG_COLOR     = 0x4caf50;
export const HP_BAR_BORDER_COLOR = 0x000000;

/** Per-entity overlay nodes. Pooled so an entity that drops in/out of
 *  AOI doesn't churn the GC every frame. */
interface OverlayNodes {
	root: Container;
	text: Text | null;
	bar: Graphics | null;
}

export interface NameplateOptions {
	worldViewW: number;
	worldViewH: number;
}

export class NameplateLayer {
	readonly root = new Container();
	private readonly nodes = new Map<EntityId, OverlayNodes>();

	constructor(private readonly opts: NameplateOptions) {
		this.root.label = "nameplates";
		// Scaled with the rest of the scene; we live in the same
		// coordinate space as the entity sprites.
	}

	/** Update overlays from a frame's Renderables. Entities that drop
	 *  out are torn down. Entities with neither a nameplate nor an
	 *  HP bar pay zero allocation -- we skip the upsert entirely. */
	update(renderables: Renderable[], camera: Camera): void {
		const seen = new Set<EntityId>();
		for (const r of renderables) {
			if (!shouldShow(r)) continue;
			seen.add(r.id);
			this.upsert(r, camera);
		}
		for (const id of [...this.nodes.keys()]) {
			if (!seen.has(id)) this.removeOne(id);
		}
	}

	/** Read-only count for tests. */
	size(): number { return this.nodes.size; }

	private upsert(r: Renderable, camera: Camera): void {
		let n = this.nodes.get(r.id);
		if (!n) {
			n = { root: new Container(), text: null, bar: null };
			n.root.label = `nameplate:${r.id}`;
			this.root.addChild(n.root);
			this.nodes.set(r.id, n);
		}

		// --- Position the overlay (entity-anchor in screen-relative space).
		const screen = worldToScreen(
			r.x, r.y,
			camera.cx, camera.cy,
			passthroughLayout(this.opts.worldViewW, this.opts.worldViewH),
			this.opts.worldViewW, this.opts.worldViewH,
			SUB_PER_PX,
		);
		n.root.position.set(screen.x, screen.y);

		// --- Nameplate text.
		const wantText = r.nameplate && r.nameplate.length > 0;
		if (wantText) {
			if (!n.text) {
				n.text = new Text({
					text: r.nameplate ?? "",
					style: {
						fill: 0xffffff,
						fontSize: DEFAULT_NAMEPLATE_FONT_PX,
						fontFamily: "monospace",
						stroke: { color: 0x000000, width: 2 },
						align: "center",
					},
				});
				n.text.anchor.set(0.5, 1);
				n.text.position.set(0, -NAMEPLATE_OFFSET_PX);
				n.root.addChild(n.text);
			} else if (n.text.text !== r.nameplate) {
				n.text.text = r.nameplate ?? "";
			}
		} else if (n.text) {
			n.text.destroy();
			n.text = null;
		}

		// --- HP bar.
		const wantBar = r.hpPct !== undefined && r.hpPct !== NO_HP_BAR;
		if (wantBar) {
			if (!n.bar) {
				n.bar = new Graphics();
				n.bar.position.set(-HP_BAR_WIDTH_PX / 2, -HP_BAR_OFFSET_PX);
				n.root.addChild(n.bar);
			}
			drawHpBar(n.bar, r.hpPct ?? 0);
		} else if (n.bar) {
			n.bar.destroy();
			n.bar = null;
		}
	}

	private removeOne(id: EntityId): void {
		const n = this.nodes.get(id);
		if (!n) return;
		n.root.destroy({ children: true });
		this.nodes.delete(id);
	}

	/** Read-only access for tests. Production code never reaches in. */
	getOverlay(id: EntityId): { root: Container; text: Text | null; bar: Graphics | null } | undefined {
		return this.nodes.get(id);
	}

	/** Read-only access to all overlay containers (z-ordered). */
	overlays(): readonly ContainerChild[] {
		return this.root.children as unknown as readonly ContainerChild[];
	}
}

// ---- Helpers --------------------------------------------------------

export function shouldShow(r: Renderable): boolean {
	const hasName = r.nameplate !== undefined && r.nameplate.length > 0;
	const hasBar  = r.hpPct !== undefined && r.hpPct !== NO_HP_BAR;
	return hasName || hasBar;
}

/** Compute the filled-foreground width of an HP bar for `pct` (0..100). */
export function barWidth(pct: number): number {
	if (!Number.isFinite(pct) || pct <= 0) return 0;
	if (pct >= 100) return HP_BAR_WIDTH_PX;
	return Math.round((HP_BAR_WIDTH_PX * pct) / 100);
}

/** Draw the bar into a Graphics. Background, fill, and 1-px border. */
export function drawHpBar(g: Graphics, pct: number): void {
	const w = barWidth(pct);
	g.clear();
	// Background.
	g.rect(0, 0, HP_BAR_WIDTH_PX, HP_BAR_HEIGHT_PX);
	g.fill({ color: HP_BAR_BG_COLOR });
	// Foreground.
	if (w > 0) {
		g.rect(0, 0, w, HP_BAR_HEIGHT_PX);
		g.fill({ color: HP_BAR_FG_COLOR });
	}
	// Border (drawn last so it overlays both fills).
	g.rect(0, 0, HP_BAR_WIDTH_PX, HP_BAR_HEIGHT_PX);
	g.stroke({ color: HP_BAR_BORDER_COLOR, width: 1 });
}

function passthroughLayout(w: number, h: number): ViewportLayout {
	return { scale: 1, offsetX: 0, offsetY: 0, scaledW: w, scaledH: h };
}
