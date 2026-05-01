import { Container, Graphics } from "pixi.js";

import { pixiUITokens, surfacePalette, type PixiUITokens, type SurfaceTone } from "./tokens";

export interface SurfaceOptions {
	width: number;
	height: number;
	tone?: SurfaceTone;
	tokens?: PixiUITokens;
	radius?: number;
	borderWidth?: number;
	focus?: boolean;
	accentEdge?: "left" | "top" | "right" | "bottom" | "none";
	alpha?: number;
}

export class Surface extends Container {
	private readonly g = new Graphics();
	private opts: Required<Omit<SurfaceOptions, "tokens" | "accentEdge">> & {
		tokens: PixiUITokens;
		accentEdge: NonNullable<SurfaceOptions["accentEdge"]>;
	};

	constructor(opts: SurfaceOptions) {
		super();
		this.opts = {
			width: Math.max(1, Math.floor(opts.width)),
			height: Math.max(1, Math.floor(opts.height)),
			tone: opts.tone ?? "panel",
			tokens: opts.tokens ?? pixiUITokens,
			radius: 0,
			borderWidth: opts.borderWidth ?? pixiUITokens.shape.borderWidth,
			focus: opts.focus ?? false,
			accentEdge: opts.accentEdge ?? "none",
			alpha: opts.alpha ?? 1,
		};
		this.addChild(this.g);
		this.redraw();
	}

	resize(width: number, height: number): void {
		this.opts.width = Math.max(1, Math.floor(width));
		this.opts.height = Math.max(1, Math.floor(height));
		this.redraw();
	}

	setTone(tone: SurfaceTone): void {
		this.opts.tone = tone;
		this.redraw();
	}

	setFocus(focus: boolean): void {
		this.opts.focus = focus;
		this.redraw();
	}

	override get width(): number { return this.opts.width; }
	override get height(): number { return this.opts.height; }

	private redraw(): void {
		const { width, height, borderWidth, tokens, tone } = this.opts;
		const p = surfacePalette(tone, tokens);
		this.g.clear();
		this.g.rect(0, 0, width, height)
			.fill({ color: p.fill, alpha: this.opts.alpha })
			.rect(0, 0, width, height)
			.stroke({ color: p.border, width: borderWidth, alignment: 1 });

		if (p.highlight !== undefined && this.opts.accentEdge !== "none") {
			const w = Math.min(4, Math.max(2, Math.floor(width / 8)));
			const h = Math.min(4, Math.max(2, Math.floor(height / 8)));
			switch (this.opts.accentEdge) {
				case "left":
					this.g.rect(0, 0, w, height).fill(p.highlight);
					break;
				case "right":
					this.g.rect(width - w, 0, w, height).fill(p.highlight);
					break;
				case "top":
					this.g.rect(0, 0, width, h).fill(p.highlight);
					break;
				case "bottom":
					this.g.rect(0, height - h, width, h).fill(p.highlight);
					break;
			}
		}

		if (this.opts.focus) {
			const inset = tokens.shape.focusWidth;
			this.g.rect(inset, inset, Math.max(1, width - inset * 2), Math.max(1, height - inset * 2))
				.stroke({ color: tokens.color.focus, width: tokens.shape.focusWidth, alignment: 1 });
		}
	}
}

export function roleTone(role: string): SurfaceTone {
	if (role.includes("slot")) return role.includes("selected") ? "slotSelected" : "slot";
	if (role.includes("scroll_bar")) return "scrollTrack";
	if (role.includes("scroll_handle")) return "scrollThumb";
	if (role.includes("press") || role.includes("selected")) return "buttonActive";
	if (role.includes("lock") || role.includes("unavailable")) return "buttonDisabled";
	if (role.includes("textfield") || role.includes("dropdown") || role.includes("slider")) return "input";
	if (role.includes("button")) return "button";
	if (role.includes("frame")) return "panel";
	return "panel";
}
