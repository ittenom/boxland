// Boxland — 9-slice sprite factory.
//
// Thin wrapper around `pixi.js` `NineSliceSprite` that takes a
// `Theme` role + target dimensions and builds a properly-skinned
// container. Returns immediately with a placeholder (graphics
// fill) and swaps in the real `NineSliceSprite` once the texture
// resolves — so the first frame paints without waiting for any
// network round-trip.
//
// Used by every panel/button/frame factory in widgets.ts. Pure
// renderer code; no editor-specific knowledge.

import { Container, Graphics, NineSliceSprite } from "pixi.js";

import type { Theme, Role } from "./theme";

export interface NineSliceOptions {
	theme: Theme;
	role: Role | string;
	width: number;
	height: number;
	/** Optional fallback fill colour while the texture is loading
	 *  or when the role is missing from the theme. Default is a
	 *  muted slate so the placeholder is obviously a placeholder
	 *  without being visually loud. */
	fallbackColor?: number;
}

/** A NineSlice container hosts the placeholder fill + swaps in the
 *  real sprite when the texture loads. resize() lets callers update
 *  dimensions without rebuilding the container. */
export class NineSlice extends Container {
	private bg: Graphics | NineSliceSprite;
	private readonly fallbackColor: number;
	private readonly insets: { left: number; top: number; right: number; bottom: number };
	private _width: number;
	private _height: number;

	constructor(opts: NineSliceOptions) {
		super();
		this._width = Math.max(1, Math.floor(opts.width));
		this._height = Math.max(1, Math.floor(opts.height));
		this.fallbackColor = opts.fallbackColor ?? 0x223044;

		const entry = opts.theme.get(opts.role);
		if (!entry) {
			// No theme entry — render a debug-friendly fill so the
			// missing role is visible. The editor's startup logs
			// the missing role separately.
			this.insets = { left: 1, top: 1, right: 1, bottom: 1 };
			this.bg = this.makePlaceholder();
			this.addChild(this.bg);
			return;
		}

		this.insets = entry.nineSlice;
		this.bg = this.makePlaceholder();
		this.addChild(this.bg);

		// Async: load the texture, swap the placeholder out.
		const p = opts.theme.textureFor(opts.role);
		if (p) {
			void p.then((tex) => {
				if (this.destroyed) return;
				const nine = new NineSliceSprite({
					texture: tex,
					leftWidth: this.insets.left,
					topHeight: this.insets.top,
					rightWidth: this.insets.right,
					bottomHeight: this.insets.bottom,
					width: this._width,
					height: this._height,
				});
				nine.roundPixels = true;
				this.removeChild(this.bg);
				this.bg.destroy();
				this.bg = nine;
				this.addChildAt(this.bg, 0);
			}).catch(() => { /* keep the placeholder */ });
		}
	}

	/** Resize the slice. Cheap when the underlying NineSliceSprite
	 *  is mounted (it's a property assignment); the placeholder
	 *  redraw is also cheap. */
	resize(width: number, height: number): void {
		this._width = Math.max(1, Math.floor(width));
		this._height = Math.max(1, Math.floor(height));
		if (this.bg instanceof NineSliceSprite) {
			this.bg.width = this._width;
			this.bg.height = this._height;
			return;
		}
		// Placeholder Graphics — clear and re-draw at the new size.
		const g = this.bg;
		g.clear();
		g.rect(0, 0, this._width, this._height).fill(this.fallbackColor);
	}

	/** Override Container.width/height to surface the configured
	 *  size rather than the internal NineSliceSprite's bounds.
	 *  Pixi's bounds are computed lazily and need a render pass to
	 *  populate, which is unsafe to depend on at layout time. */
	override get width(): number { return this._width; }
	override get height(): number { return this._height; }

	private makePlaceholder(): Graphics {
		const g = new Graphics();
		g.rect(0, 0, this._width, this._height).fill(this.fallbackColor);
		return g;
	}
}
