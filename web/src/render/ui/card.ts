// Boxland — Card primitive.
//
// A Card is the page's primary container: an off-white paper rectangle
// with a subtle drop shadow against the canvas, an index-card-style
// header strip (title row + pale-red rule below it), and a body area
// for arbitrary children.
//
// Cards are the only display object that should "float" on the page
// background; nested groupings (Buttons, Toolbar, Layer Rows...) use
// Surface with a sunken tone so the card itself remains the only
// shadowed layer.

import { Container, Graphics, Text } from "pixi.js";
import { DropShadowFilter } from "pixi-filters";

import { Surface } from "./surface";
import { truncateText } from "./layout";
import { pixiUITokens, type PixiUITokens, type SurfaceTone } from "./tokens";

export type CardTitleSize = "sm" | "md" | "lg" | "xl";

export interface CardOptions {
	width: number;
	height: number;
	title?: string;
	titleSize?: CardTitleSize;
	tokens?: PixiUITokens;
	tone?: SurfaceTone;
	contentPadding?: number;
	headerHeight?: number;
	showHeaderRule?: boolean;
	shadow?: boolean;
}

export class Card extends Container {
	readonly inner: Container;
	private readonly bg: Surface;
	private titleText: Text | null = null;
	private rule: Graphics | null = null;
	private opts: Required<Omit<CardOptions, "tokens" | "title">> & {
		tokens: PixiUITokens;
		title: string | null;
	};

	constructor(opts: CardOptions) {
		super();
		const tokens = opts.tokens ?? pixiUITokens;
		const titleSize = opts.titleSize ?? "md";
		const contentPadding = opts.contentPadding ?? tokens.space.cardPad;
		const headerHeight = opts.headerHeight ?? defaultHeaderHeight(titleSize, tokens);
		this.opts = {
			width: Math.max(1, Math.floor(opts.width)),
			height: Math.max(1, Math.floor(opts.height)),
			title: opts.title ?? null,
			titleSize,
			tokens,
			tone: opts.tone ?? "panel",
			contentPadding,
			headerHeight,
			showHeaderRule: opts.showHeaderRule ?? !!opts.title,
			shadow: opts.shadow ?? true,
		};
		this.bg = new Surface({ width: this.opts.width, height: this.opts.height, tone: this.opts.tone, tokens });
		this.addChild(this.bg);
		this.inner = new Container();
		this.addChild(this.inner);
		this.layoutHeader();
		this.positionInner();
		if (this.opts.shadow) this.applyShadow();
	}

	resize(width: number, height: number): void {
		this.opts.width = Math.max(1, Math.floor(width));
		this.opts.height = Math.max(1, Math.floor(height));
		this.bg.resize(this.opts.width, this.opts.height);
		this.layoutHeader();
		this.positionInner();
	}

	get headerHeight(): number { return this.opts.headerHeight; }
	get contentPadding(): number { return this.opts.contentPadding; }
	get headerBodyGap(): number { return this.opts.title || this.opts.showHeaderRule ? 8 : 0; }
	get contentTop(): number {
		const base = this.opts.title || this.opts.showHeaderRule ? this.opts.headerHeight : this.opts.contentPadding;
		return base + this.headerBodyGap;
	}
	get contentWidth(): number { return Math.max(0, this.opts.width - this.opts.contentPadding * 2); }
	get contentHeight(): number {
		return Math.max(0, this.opts.height - this.contentTop - this.opts.contentPadding);
	}

	private positionInner(): void {
		this.inner.position.set(this.opts.contentPadding, this.contentTop);
	}

	private layoutHeader(): void {
		if (this.titleText) { this.titleText.destroy(); this.titleText = null; }
		if (this.rule) { this.rule.destroy(); this.rule = null; }
		const { title, titleSize, tokens, contentPadding, headerHeight, width, showHeaderRule } = this.opts;
		if (title) {
			const fontSize = titleFontSize(titleSize, tokens);
			const t = new Text({
				text: title,
				style: {
					fontFamily: tokens.type.family,
					fontSize,
					fontWeight: tokens.type.weightBold,
					fill: tokens.color.text,
					letterSpacing: 0,
				},
			});
			truncateText(t, Math.max(32, width - contentPadding * 2));
			t.position.set(contentPadding, Math.max(4, Math.round((headerHeight - fontSize) / 2) - 2));
			this.addChild(t);
			this.titleText = t;
		}
		if (showHeaderRule) {
			const g = new Graphics();
			const ruleY = headerHeight - Math.max(1, tokens.shape.headerRuleWidth);
			const ruleW = Math.max(0, width - contentPadding * 2);
			g.rect(contentPadding, ruleY, ruleW, Math.max(1, tokens.shape.headerRuleWidth))
				.fill(tokens.color.headerRule);
			this.addChild(g);
			this.rule = g;
		}
	}

	private applyShadow(): void {
		const s = this.opts.tokens.shadow;
		this.filters = [new DropShadowFilter({
			color: s.color,
			alpha: s.alpha,
			blur: s.blur,
			offset: { x: s.offsetX, y: s.offsetY },
			quality: 3,
		})];
	}
}

function titleFontSize(size: CardTitleSize, tokens: PixiUITokens): number {
	switch (size) {
		case "sm": return tokens.type.sizeSm;
		case "md": return tokens.type.sizeMd;
		case "lg": return tokens.type.sizeLg;
		case "xl": return tokens.type.sizeXl;
	}
}

function defaultHeaderHeight(size: CardTitleSize, tokens: PixiUITokens): number {
	const fs = titleFontSize(size, tokens);
	return Math.round(fs * 1.6 + 12);
}
