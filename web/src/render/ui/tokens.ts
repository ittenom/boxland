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
}

export interface PixiUITypographyTokens {
	family: string;
	monoFamily: string;
	sizeXs: number;
	sizeSm: number;
	sizeMd: number;
	sizeLg: number;
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
}

export interface PixiUIShapeTokens {
	radiusSm: number;
	radiusMd: number;
	radiusLg: number;
	borderWidth: number;
	focusWidth: number;
}

export interface PixiUITokens {
	color: PixiUIColorTokens;
	type: PixiUITypographyTokens;
	space: PixiUISpacingTokens;
	shape: PixiUIShapeTokens;
}

export const pixiUITokens: PixiUITokens = {
	color: {
		canvas: 0x0c111b,
		surface: 0x101827,
		surfaceRaised: 0x172033,
		surfaceSunken: 0x0b1220,
		surfaceMuted: 0x1c2638,
		border: 0x2d3b55,
		borderStrong: 0x49617f,
		text: 0xe8ecf2,
		textMuted: 0xa9b0c0,
		textSubtle: 0x748298,
		accent: 0xffd84a,
		accentSoft: 0x3b66bc,
		accentText: 0x10131c,
		warning: 0xf5b800,
		danger: 0xff5a6a,
		success: 0x5adfb0,
		focus: 0x6ea0ff,
		disabled: 0x30394a,
	},
	type: {
		family: "DM Mono, Consolas, monospace",
		monoFamily: "DM Mono, Consolas, monospace",
		sizeXs: 9,
		sizeSm: 11,
		sizeMd: 13,
		sizeLg: 16,
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
	},
	shape: {
		radiusSm: 0,
		radiusMd: 0,
		radiusLg: 0,
		borderWidth: 1,
		focusWidth: 2,
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
			return { fill: c.surfaceMuted, border: c.borderStrong, text: c.text };
		case "buttonActive":
			return { fill: c.accentSoft, border: c.accent, highlight: c.accent, text: c.text };
		case "buttonDisabled":
			return { fill: c.disabled, border: c.border, text: c.textSubtle };
		case "toolActive":
			return { fill: c.accent, border: c.accentText, text: c.accentText };
		case "slot":
			return { fill: c.surfaceSunken, border: c.borderStrong, text: c.text };
		case "slotSelected":
			return { fill: c.surfaceRaised, border: c.accent, highlight: c.accent, text: c.text };
		case "scrollTrack":
			return { fill: c.surfaceSunken, border: c.border };
		case "scrollThumb":
			return { fill: c.accentSoft, border: c.focus };
		case "input":
			return { fill: c.surfaceSunken, border: c.borderStrong, text: c.text };
		case "panel":
			return { fill: c.surface, border: c.border, text: c.text };
	}
}
