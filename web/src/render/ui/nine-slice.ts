// Boxland — compatibility surface factory.
//
// Older editor code asks for `NineSlice({ theme, role, ... })`.
// The editor chrome is no longer asset-skinned: this class now maps
// those semantic roles onto the canonical Pixi UI token surfaces. The
// API stays stable while callers migrate to `Surface` directly.
//
// Asset-backed nine-slice remains a game/HUD concept, but not editor
// chrome; keeping this class drawn avoids the Gradient PNG dependency
// that caused inconsistent panels and broken scrollbars.

import { Container } from "pixi.js";

import type { Theme, Role } from "./theme";
import { Surface, roleTone } from "./surface";

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

/** Compatibility wrapper around Surface. resize() lets callers update
 *  dimensions without rebuilding the container. */
export class NineSlice extends Container {
	private readonly bg: Surface;
	private _width: number;
	private _height: number;

	constructor(opts: NineSliceOptions) {
		super();
		this._width = Math.max(1, Math.floor(opts.width));
		this._height = Math.max(1, Math.floor(opts.height));
		void opts.theme;
		void opts.fallbackColor;
		this.bg = new Surface({
			width: this._width,
			height: this._height,
			tone: roleTone(String(opts.role)),
			accentEdge: String(opts.role).includes("selected") || String(opts.role).includes("press") ? "left" : "none",
		});
		this.addChild(this.bg);
	}

	/** Resize the slice. Cheap when the underlying NineSliceSprite
	 *  is mounted (it's a property assignment); the placeholder
	 *  redraw is also cheap. */
	resize(width: number, height: number): void {
		this._width = Math.max(1, Math.floor(width));
		this._height = Math.max(1, Math.floor(height));
		this.bg.resize(this._width, this._height);
	}

	/** Override Container.width/height to surface the configured
	 *  size rather than the internal NineSliceSprite's bounds.
	 *  Pixi's bounds are computed lazily and need a render pass to
	 *  populate, which is unsafe to depend on at layout time. */
	override get width(): number { return this._width; }
	override get height(): number { return this._height; }
}
