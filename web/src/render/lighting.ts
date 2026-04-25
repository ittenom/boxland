// Boxland — lighting layer compositor.
//
// Per PLAN.md §6b: "Lighting layer compositor (multiply blend mode of
// lighting cells)." We render lighting cells into their own Container
// using `blendMode = "multiply"` so the underlying scene is darkened or
// tinted per cell. A cell with full-bright white intensity is a no-op;
// a cell with #000 intensity 255 produces full darkness.
//
// Performance note: we use Graphics rectangles (one per cell) for v1.
// At realistic lighting densities (a few hundred visible cells) this is
// fine; if profiling shows it as a hot spot we can swap to a pre-baked
// shadow texture or instanced mesh.

import { Container, Graphics } from "pixi.js";

import { TILE_SIZE_SUB } from "@collision";
import type { Camera } from "./types";
import { worldToScreen, type ViewportLayout } from "./viewport";

/** One lighting cell. Coordinates are tile grid indices. */
export interface LightingCell {
	gx: number;
	gy: number;
	color: number;     // 0xRRGGBBAA
	intensity: number; // 0..255
}

export interface LightingOptions {
	worldViewW: number;
	worldViewH: number;
}

export class LightingLayer {
	readonly root = new Container();
	private readonly graphics = new Graphics();

	constructor(private readonly opts: LightingOptions) {
		this.root.blendMode = "multiply";
		this.root.label = "lighting";
		this.root.addChild(this.graphics);
	}

	/**
	 * Replace the lighting cells. Coordinates project through the same
	 * world-to-screen function the scene uses, so lighting stays aligned
	 * with the underlying tile grid as the camera pans.
	 */
	update(cells: LightingCell[], camera: Camera): void {
		this.graphics.clear();

		// Lighting renders inside the same parent transform as Scene.root
		// (i.e., the integer-scaled root). For positioning we work in raw
		// world pixels; the parent scale takes care of the rest.
		const dummyLayout: ViewportLayout = {
			scale: 1,
			offsetX: 0,
			offsetY: 0,
			scaledW: this.opts.worldViewW,
			scaledH: this.opts.worldViewH,
		};
		const tilePx = TILE_SIZE_SUB / 256; // sub-px / sub-per-px = world px

		for (const cell of cells) {
			const cellSubX = cell.gx * TILE_SIZE_SUB;
			const cellSubY = cell.gy * TILE_SIZE_SUB;
			const top = worldToScreen(
				cellSubX, cellSubY,
				camera.cx, camera.cy,
				dummyLayout,
				this.opts.worldViewW, this.opts.worldViewH,
				256,
			);

			const color = (cell.color >>> 8) & 0xffffff; // strip alpha byte
			const alpha = (cell.intensity & 0xff) / 255;
			this.graphics
				.rect(top.x, top.y, tilePx, tilePx)
				.fill({ color, alpha });
		}
	}

	/** Number of geometries pending render — handy for tests. */
	geometryCount(): number {
		// Pixi Graphics keeps its commands in `context.instructions`.
		// We can't reliably count them across versions; use the children
		// of the lighting Container as a proxy when each cell becomes
		// its own draw call (currently they're batched into one).
		// For tests we expose this as 1 if any cell rendered, 0 otherwise.
		// A more granular count is unnecessary at this layer.
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		return (this.graphics as unknown as { context: { instructions: unknown[] } })
			.context.instructions.length;
	}
}
