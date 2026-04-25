// Boxland — renderer debug overlay.
//
// Per PLAN.md §6b: "Debug overlay (collision boxes, AOI radius, entity ids)
// — visible only in Sandbox." Mounted as a top-most layer; the Sandbox UI
// flips `visible` based on a designer toggle.
//
// Inputs are the same Renderable list the scene already consumes plus an
// optional camera-centered AOI radius (in tile units) drawn as a square.

import { Container, Graphics, Text } from "pixi.js";

import { TILE_SIZE_SUB } from "@collision";
import type { Camera, Renderable } from "./types";
import { worldToScreen, type ViewportLayout } from "./viewport";

export interface DebugOptions {
	worldViewW: number;
	worldViewH: number;
}

export class DebugOverlay {
	readonly root = new Container();
	private readonly boxes = new Graphics();
	private readonly aoi = new Graphics();
	private readonly idLabels = new Container();

	constructor(private readonly opts: DebugOptions) {
		this.root.label = "debug-overlay";
		this.root.visible = false;
		this.root.addChild(this.aoi);
		this.root.addChild(this.boxes);
		this.root.addChild(this.idLabels);
	}

	/** Toggle the entire overlay's visibility. */
	setVisible(v: boolean): void {
		this.root.visible = v;
	}

	isVisible(): boolean {
		return this.root.visible;
	}

	/**
	 * Refresh the overlay's graphics from the current Renderable list and
	 * camera. Called from the same per-tick code path that updates the
	 * scene, so debug visuals are always frame-coherent with gameplay.
	 *
	 * `aoiRadiusTiles` draws a square AOI region centered on the camera;
	 * pass 0 to suppress.
	 */
	update(renderables: readonly Renderable[], camera: Camera, aoiRadiusTiles: number): void {
		this.drawAOI(camera, aoiRadiusTiles);
		this.drawAABBs(renderables, camera);
		this.drawIds(renderables, camera);
	}

	private layoutPx(): ViewportLayout {
		return {
			scale: 1,
			offsetX: 0,
			offsetY: 0,
			scaledW: this.opts.worldViewW,
			scaledH: this.opts.worldViewH,
		};
	}

	private drawAOI(camera: Camera, aoiRadiusTiles: number): void {
		this.aoi.clear();
		if (aoiRadiusTiles <= 0) return;

		const layout = this.layoutPx();
		const radiusSub = aoiRadiusTiles * TILE_SIZE_SUB;
		const tl = worldToScreen(
			camera.cx - radiusSub, camera.cy - radiusSub,
			camera.cx, camera.cy,
			layout, this.opts.worldViewW, this.opts.worldViewH, 256,
		);
		const br = worldToScreen(
			camera.cx + radiusSub, camera.cy + radiusSub,
			camera.cx, camera.cy,
			layout, this.opts.worldViewW, this.opts.worldViewH, 256,
		);
		this.aoi
			.rect(tl.x, tl.y, br.x - tl.x, br.y - tl.y)
			.stroke({ color: 0x4ad7ff, width: 1, alpha: 0.5 });
	}

	private drawAABBs(renderables: readonly Renderable[], camera: Camera): void {
		this.boxes.clear();
		const layout = this.layoutPx();
		for (const r of renderables) {
			if (!r.debug?.aabb) continue;
			const halfW = Math.floor(r.debug.aabb.w / 2);
			const halfH = Math.floor(r.debug.aabb.h / 2);
			const tl = worldToScreen(
				r.x - halfW * 256, r.y - halfH * 256,
				camera.cx, camera.cy,
				layout, this.opts.worldViewW, this.opts.worldViewH, 256,
			);
			this.boxes
				.rect(tl.x, tl.y, r.debug.aabb.w, r.debug.aabb.h)
				.stroke({ color: 0xff5e7e, width: 1, alpha: 0.8 });
		}
	}

	private drawIds(renderables: readonly Renderable[], camera: Camera): void {
		// Reuse Text instances when possible to avoid GC churn.
		while (this.idLabels.children.length > renderables.length) {
			const t = this.idLabels.removeChildAt(this.idLabels.children.length - 1);
			t.destroy();
		}
		const layout = this.layoutPx();
		for (let i = 0; i < renderables.length; i++) {
			const r = renderables[i]!;
			let label = this.idLabels.children[i] as Text | undefined;
			if (!label) {
				label = new Text({
					text: "",
					style: {
						fontFamily: "TinyUnicode, C64esque, monospace",
						fontSize: 8,
						fill: 0xffd34a,
					},
				});
				this.idLabels.addChild(label);
			}
			label.text = `#${r.id}`;
			const pt = worldToScreen(
				r.x, r.y,
				camera.cx, camera.cy,
				layout, this.opts.worldViewW, this.opts.worldViewH, 256,
			);
			label.position.set(pt.x + 1, pt.y + 1);
		}
	}

	/** Test helper. */
	idLabelCount(): number {
		return this.idLabels.children.length;
	}
}
