// Boxland Pixi UI design tokens.
//
// These are renderer-native tokens for editor chrome and tools. They
// intentionally do not depend on DOM CSS variables: Pixi components need
// deterministic numbers at construction and resize time.

export interface PixiUIColorTokens {
	canvas: number;
	surface: number;
	surfaceRaised: number;
	surfaceSunken: number;
	surfaceMuted: number;
	border: number;
	borderStrong: number;
	text: number;
	textMuted: number;
	textSubtle: number;
	accent: number;
	accentSoft: number;
	accentText: number;
	warning: number;
	danger: number;
	success: number;
	focus: number;
	disabled: number;
	headerRule: number;
	dotGrid: number;
}

export interface PixiUITypographyTokens {
	family: string;
	monoFamily: string;
	sizeXs: number;
	sizeSm: number;
	sizeMd: number;
	sizeLg: number;
	sizeXl: number;
	weightRegular: "400";
	weightMedium: "600";
	weightBold: "700";
}

export interface PixiUISpacingTokens {
	px: number;
	xs: number;
	sm: number;
	md: number;
	lg: number;
	xl: number;
	cardPad: number;
	cardGap: number;
	dotGrid: number;
}

export interface PixiUIShapeTokens {
	radiusSm: number;
	radiusMd: number;
	radiusLg: number;
	borderWidth: number;
	focusWidth: number;
	headerRuleWidth: number;
}

export interface PixiUIShadowTokens {
	color: number;
	alpha: number;
	blur: number;
	offsetX: number;
	offsetY: number;
}

export interface PixiUITokens {
	color: PixiUIColorTokens;
	type: PixiUITypographyTokens;
	space: PixiUISpacingTokens;
	shape: PixiUIShapeTokens;
	shadow: PixiUIShadowTokens;
}

export const pixiUITokens: PixiUITokens = {
	color: {
		canvas: 0xefe9da,
		surface: 0xfaf6ec,
		surfaceRaised: 0xfffdf6,
		surfaceSunken: 0xeae3d2,
		surfaceMuted: 0xf2ecdc,
		border: 0xcfc6b0,
		borderStrong: 0xa39879,
		text: 0x1c1812,
		textMuted: 0x6a604f,
		textSubtle: 0x9a9180,
		accent: 0x2f5d8e,
		accentSoft: 0xc8d7ea,
		accentText: 0xffffff,
		warning: 0xc78a26,
		danger: 0xb24a4a,
		success: 0x4f8a4a,
		focus: 0x2f5d8e,
		disabled: 0xddd5c2,
		headerRule: 0xe6928c,
		dotGrid: 0xa3c08a,
	},
	type: {
		family: "DM Mono, Consolas, monospace",
		monoFamily: "DM Mono, Consolas, monospace",
		sizeXs: 10,
		sizeSm: 12,
		sizeMd: 14,
		sizeLg: 18,
		sizeXl: 28,
		weightRegular: "400",
		weightMedium: "600",
		weightBold: "700",
	},
	space: {
		px: 1,
		xs: 4,
		sm: 6,
		md: 10,
		lg: 14,
		xl: 20,
		cardPad: 16,
		cardGap: 24,
		dotGrid: 32,
	},
	shape: {
		radiusSm: 0,
		radiusMd: 0,
		radiusLg: 0,
		borderWidth: 1,
		focusWidth: 2,
		headerRuleWidth: 1,
	},
	shadow: {
		color: 0x1a1305,
		alpha: 0.18,
		blur: 6,
		offsetX: 3,
		offsetY: 4,
	},
};

export type SurfaceTone =
	| "panel"
	| "raised"
	| "sunken"
	| "button"
	| "buttonActive"
	| "buttonDisabled"
	| "toolActive"
	| "slot"
	| "slotSelected"
	| "scrollTrack"
	| "scrollThumb"
	| "input";

export interface SurfacePalette {
	fill: number;
	border: number;
	highlight?: number;
	text?: number;
}

export function surfacePalette(tone: SurfaceTone, tokens: PixiUITokens = pixiUITokens): SurfacePalette {
	const c = tokens.color;
	switch (tone) {
		case "raised":
			return { fill: c.surfaceRaised, border: c.borderStrong, text: c.text };
		case "sunken":
			return { fill: c.surfaceSunken, border: c.border, text: c.textMuted };
		case "button":
			return { fill: c.surfaceRaised, border: c.borderStrong, text: c.text };
		case "buttonActive":
			return { fill: c.accentSoft, border: c.accent, highlight: c.accent, text: c.text };
		case "buttonDisabled":
			return { fill: c.disabled, border: c.border, text: c.textSubtle };
		case "toolActive":
			return { fill: c.accent, border: c.accent, text: c.accentText };
		case "slot":
			return { fill: c.surfaceSunken, border: c.borderStrong, text: c.text };
		case "slotSelected":
			return { fill: c.surfaceRaised, border: c.accent, highlight: c.accent, text: c.text };
		case "scrollTrack":
			return { fill: c.surfaceSunken, border: c.border };
		case "scrollThumb":
			return { fill: c.borderStrong, border: c.borderStrong };
		case "input":
			return { fill: c.surfaceSunken, border: c.borderStrong, text: c.text };
		case "panel":
			return { fill: c.surface, border: c.border, text: c.text };
	}
}
