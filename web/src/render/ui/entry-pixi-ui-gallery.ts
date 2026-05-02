import { Application, Container, Graphics, Rectangle, Text } from "pixi.js";
import type { FederatedPointerEvent, FederatedWheelEvent } from "pixi.js";
import { FancyButton } from "@pixi/ui";

import { Card } from "./card";
import { NineSlice } from "./nine-slice";
import { Roles, Theme } from "./theme";
import { Surface, roleTone } from "./surface";
import { drawDotGrid, flowRow, truncateText, wrapText } from "./layout";
import { pixiUITokens, surfacePalette, type PixiUIColorTokens, type PixiUITokens, type SurfaceTone } from "./tokens";

const TOKENS = pixiUITokens;
const SURFACE_TONES: readonly SurfaceTone[] = ["panel", "raised", "sunken", "button", "buttonActive", "buttonDisabled", "toolActive", "slot", "slotSelected", "scrollTrack", "scrollThumb", "input"];
const TOOL_ICONS: readonly string[] = ["✎", "▣", "◇", "↶", "↷", "⧉"];
const LAYER_LABELS: readonly string[] = ["base", "decoration", "lighting"];
const BUTTON_LABELS: readonly string[] = ["Default", "Active", "Disabled"];
const BUTTON_ROLES: readonly string[] = [Roles.ButtonSmReleaseA, Roles.ButtonSmPressA, Roles.ButtonSmLockA];

interface Scheme {
	name: string;
	color: Partial<PixiUIColorTokens>;
}

const SCHEMES: readonly Scheme[] = [
	{ name: "Boxland", color: {} },
	{
		name: "Forge",
		color: {
			canvas: 0xeae0c8,
			surface: 0xfff5dd,
			surfaceRaised: 0xfffae6,
			surfaceSunken: 0xeaddc1,
			surfaceMuted: 0xf2e6ce,
			border: 0xc4b18a,
			borderStrong: 0xa48d65,
			text: 0x2a1d0d,
			textMuted: 0x6f5c3e,
			textSubtle: 0xa19073,
			accent: 0xb8631e,
			accentSoft: 0xebbf95,
			accentText: 0xfff7e8,
			focus: 0xb8631e,
			disabled: 0xd8caa9,
			headerRule: 0xd97a6e,
			dotGrid: 0x8fae6e,
		},
	},
	{
		name: "Signal",
		color: {
			canvas: 0xe2e8e5,
			surface: 0xf5f8f6,
			surfaceRaised: 0xfafdfb,
			surfaceSunken: 0xd5dcd9,
			surfaceMuted: 0xe9efeb,
			border: 0xa9b6b2,
			borderStrong: 0x7a8985,
			text: 0x111b18,
			textMuted: 0x556862,
			textSubtle: 0x899692,
			accent: 0x1e6f5e,
			accentSoft: 0xb1d8c8,
			accentText: 0xeaf5f0,
			focus: 0x1e6f5e,
			disabled: 0xcad4d0,
			headerRule: 0xd58f8a,
			dotGrid: 0x83b095,
		},
	},
];
const DEFAULT_SCHEME = SCHEMES[0] as Scheme;

interface GalleryRuntime {
	app: Application;
	root: Container;
	host: HTMLElement;
	observer: ResizeObserver | null;
}

interface GalleryState {
	activeScheme: number;
	activeTool: number;
	activeLayer: number;
	scrollOffset: number;
	onScheme: (idx: number) => void;
	onTool: (idx: number) => void;
	onLayer: (idx: number) => void;
	onScroll: (offset: number) => void;
}

const SECTION_GAP = 10;
const SECTION_TOP = 8;
const SECTION_BOTTOM = 8;
const SECTION_MIN_H = 50;

export async function bootPixiUIGallery(): Promise<GalleryRuntime | null> {
	const host = document.querySelector("[data-bx-pixi-ui-gallery]") as HTMLElement | null;
	if (!host) return null;
	host.textContent = "";

	const app = new Application();
	await app.init({
		resizeTo: host,
		background: TOKENS.color.canvas,
		antialias: true,
		roundPixels: true,
		autoDensity: true,
		resolution: window.devicePixelRatio || 1,
	});
	app.canvas.style.display = "block";
	app.canvas.style.width = "100%";
	app.canvas.style.height = "100%";
	host.appendChild(app.canvas);

	const root = new Container();
	app.stage.addChild(root);

	let activeScheme = 0;
	let activeTool = 0;
	let activeLayer = 0;
	let scrollOffset = 0;

	const render = (): void => {
		const width = Math.max(320, Math.floor(host.clientWidth || 960));
		const height = Math.max(280, Math.floor(host.clientHeight || 640));
		const tokens = tokensForScheme(SCHEMES[activeScheme] ?? DEFAULT_SCHEME);
		app.renderer.resize(width, height);
		app.renderer.background.color = tokens.color.canvas;
		renderGallery(root, width, height, tokens, {
			activeScheme,
			activeTool,
			activeLayer,
			scrollOffset,
			onScheme: (idx) => { activeScheme = idx; render(); },
			onTool: (idx) => { activeTool = idx; render(); },
			onLayer: (idx) => { activeLayer = idx; render(); },
			onScroll: (offset) => { scrollOffset = offset; },
		});
	};
	render();

	const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(render);
	observer?.observe(host);

	return { app, root, host, observer };
}

function tokensForScheme(scheme: Scheme): PixiUITokens {
	return {
		color: { ...TOKENS.color, ...scheme.color },
		type: TOKENS.type,
		space: TOKENS.space,
		shape: TOKENS.shape,
		shadow: TOKENS.shadow,
	};
}

function renderGallery(root: Container, width: number, height: number, tokens: PixiUITokens, state: GalleryState): void {
	root.removeChildren().forEach((c) => c.destroy({ children: true }));
	const theme = Theme.fromEntries([]);

	root.addChild(drawDotGrid({ width, height, tokens }));

	const pad = tokens.space.cardGap;
	const gap = tokens.space.cardGap;
	const fullW = Math.max(120, width - pad * 2);

	const { card: headerCard, height: headerCardH } = buildHeaderCard(fullW, tokens);
	headerCard.position.set(pad, pad);
	root.addChild(headerCard);

	const bodyY = pad + headerCardH + gap;
	const bodyAvailH = Math.max(160, height - bodyY - pad);
	const stack = width < 880;

	let leftW: number;
	let rightX: number;
	let rightW: number;
	let leftH: number;
	let rightY: number;
	let rightH: number;
	if (stack) {
		leftW = fullW;
		rightX = pad;
		rightW = fullW;
		leftH = Math.max(220, Math.floor(bodyAvailH * 0.42));
		rightY = bodyY + leftH + gap;
		rightH = Math.max(200, height - rightY - pad);
	} else {
		leftW = Math.min(340, Math.max(260, Math.floor(fullW * 0.32)));
		rightX = pad + leftW + gap;
		rightW = Math.max(260, fullW - leftW - gap);
		leftH = bodyAvailH;
		rightY = bodyY;
		rightH = bodyAvailH;
	}

	const surfacesCard = new Card({ width: leftW, height: leftH, title: "Surfaces", titleSize: "md", tokens });
	surfacesCard.position.set(pad, bodyY);
	fillSurfacesCard(surfacesCard, tokens, state);
	root.addChild(surfacesCard);

	const editorCard = new Card({ width: rightW, height: rightH, title: "Editor Composition", titleSize: "md", tokens });
	editorCard.position.set(rightX, rightY);
	fillEditorCard(editorCard, theme, tokens, state);
	root.addChild(editorCard);
}

function buildHeaderCard(width: number, tokens: PixiUITokens): { card: Card; height: number } {
	const titleSize: "xl" = "xl";
	const card = new Card({ width, height: 60, title: "Pixi UI Gallery", titleSize, tokens });
	const subtitle = wrapText(
		label("Canonical editor components built with @pixi/ui and @pixi/layout — paper-style cards on a dotted canvas.", tokens.type.sizeSm, tokens.color.textMuted, tokens),
		Math.max(80, card.contentWidth),
	);
	card.inner.addChild(subtitle);
	const subH = Math.ceil(subtitle.height);
	const totalH = card.contentTop + subH + tokens.space.lg;
	card.resize(width, totalH);
	return { card, height: totalH };
}

function fillSurfacesCard(card: Card, tokens: PixiUITokens, state: GalleryState): void {
	const inner = card.inner;
	const innerW = card.contentWidth;
	const innerH = card.contentHeight;
	let y = 4;

	inner.addChild(at(label("Scheme", tokens.type.sizeXs, tokens.color.textMuted, tokens), 0, y));
	y += 18;

	const chipSize = 26;
	const chipGap = 8;
	const chipsRowW = SCHEMES.length * chipSize + (SCHEMES.length - 1) * chipGap;
	const labelGap = 12;
	for (let i = 0; i < SCHEMES.length; i++) {
		const scheme = SCHEMES[i] ?? DEFAULT_SCHEME;
		const schemeTokens = tokensForScheme(scheme);
		const chip = makeSchemeChip(schemeTokens.color.accent, schemeTokens.color.borderStrong, i === state.activeScheme, tokens);
		chip.eventMode = "static";
		chip.cursor = "pointer";
		chip.hitArea = new Rectangle(0, 0, chipSize, chipSize);
		chip.on("pointertap", () => state.onScheme(i));
		inner.addChild(at(chip, i * (chipSize + chipGap), y));
	}
	const nameMaxW = Math.max(0, innerW - chipsRowW - labelGap);
	if (nameMaxW > 0) {
		const nameLbl = label((SCHEMES[state.activeScheme] ?? DEFAULT_SCHEME).name, tokens.type.sizeXs, tokens.color.text, tokens);
		truncateText(nameLbl, nameMaxW);
		inner.addChild(at(nameLbl, chipsRowW + labelGap, y + 6));
	}
	y += chipSize + 18;

	const swatchSize = 22;
	const swatchLabelX = swatchSize + 10;
	for (const tone of SURFACE_TONES) {
		if (y + swatchSize > innerH) break;
		const swatch = new Surface({ width: swatchSize, height: swatchSize, tone, tokens });
		inner.addChild(at(swatch, 0, y));
		const p = surfacePalette(tone, tokens);
		const swatchLbl = label(`${tone}  #${p.fill.toString(16).padStart(6, "0")}`, tokens.type.sizeXs, tokens.color.textMuted, tokens);
		truncateText(swatchLbl, Math.max(0, innerW - swatchSize - 10));
		inner.addChild(at(swatchLbl, swatchLabelX, y + 4));
		y += swatchSize + 6;
	}
}

function fillEditorCard(card: Card, theme: Theme, tokens: PixiUITokens, state: GalleryState): void {
	const inner = card.inner;
	const innerW = card.contentWidth;
	const innerH = card.contentHeight;

	let y = 4;
	y = drawSection(inner, "Buttons", y, innerW, tokens, (section, sx, contentMaxW) => {
		return drawButtonsRow(section, theme, sx, contentMaxW, tokens);
	});
	y = drawSection(inner, "Toolbar", y, innerW, tokens, (section, sx, contentMaxW) => {
		return drawToolbar(section, sx, contentMaxW, tokens, state);
	});
	y = drawSection(inner, "Layer Rows", y, innerW, tokens, (section, sx, contentMaxW) => {
		return drawLayerRows(section, sx, contentMaxW, tokens, state);
	});
	const remaining = innerH - y - 4;
	if (remaining >= 80) {
		drawScrollDemo(inner, 0, y + 6, innerW, Math.min(180, remaining), tokens, state);
	}
}

function drawButtonsRow(section: Container, theme: Theme, sx: number, contentMaxW: number, tokens: PixiUITokens): number {
	const minBtnW = 72;
	const maxBtnW = 104;
	const btnH = 30;
	const gap = 8;
	let count = BUTTON_LABELS.length;
	let btnW = maxBtnW;
	while (count > 0) {
		const fitW = Math.floor((contentMaxW - (count - 1) * gap) / count);
		if (fitW >= maxBtnW) { btnW = maxBtnW; break; }
		if (fitW >= minBtnW) { btnW = fitW; break; }
		count--;
	}
	if (count <= 0 || btnW <= 0) return SECTION_TOP;
	let bx = sx;
	for (let i = 0; i < count; i++) {
		const role = BUTTON_ROLES[i] ?? Roles.ButtonSmReleaseA;
		const text = BUTTON_LABELS[i] ?? "";
		section.addChild(at(makeButton(theme, text, role, btnW, btnH, tokens, i === 2), bx, SECTION_TOP));
		bx += btnW + gap;
	}
	return SECTION_TOP + btnH;
}

function drawToolbar(section: Container, sx: number, contentMaxW: number, tokens: PixiUITokens, state: GalleryState): number {
	const toolSize = 32;
	const wrapper = new Container();
	const buttons: Container[] = [];
	for (const [idx, icon] of TOOL_ICONS.entries()) {
		const tb = makeToolButton(icon, toolSize, tokens, idx === state.activeTool);
		tb.eventMode = "static";
		tb.cursor = "pointer";
		tb.hitArea = new Rectangle(0, 0, toolSize, toolSize);
		tb.on("pointertap", () => state.onTool(idx));
		wrapper.addChild(tb);
		buttons.push(tb);
	}
	const flow = flowRow(buttons, { maxWidth: contentMaxW, gap: 8, rowGap: 6 });
	wrapper.position.set(sx, SECTION_TOP);
	section.addChild(wrapper);
	return SECTION_TOP + flow.height;
}

function drawLayerRows(section: Container, sx: number, contentMaxW: number, tokens: PixiUITokens, state: GalleryState): number {
	const rowH = 42;
	const rowGap = 6;
	const rowW = Math.max(120, contentMaxW);
	for (let i = 0; i < LAYER_LABELS.length; i++) {
		const active = i === state.activeLayer;
		const row = new Container();
		row.addChild(new Surface({
			width: rowW,
			height: rowH,
			tone: active ? "buttonActive" : "panel",
			tokens,
			accentEdge: active ? "left" : "none",
		}));
		const labelMaxW = Math.max(0, rowW - 24);
		const lbl = label(LAYER_LABELS[i] ?? "", tokens.type.sizeSm, active ? tokens.color.accent : tokens.color.text, tokens);
		truncateText(lbl, labelMaxW);
		row.addChild(at(lbl, 12, 7));
		const sub = label(`tile  ord ${i}`, tokens.type.sizeXs, tokens.color.textMuted, tokens);
		truncateText(sub, labelMaxW);
		row.addChild(at(sub, 12, 24));
		row.eventMode = "static";
		row.cursor = "pointer";
		row.hitArea = new Rectangle(0, 0, rowW, rowH);
		row.on("pointertap", () => state.onLayer(i));
		section.addChild(at(row, sx, SECTION_TOP - 1 + i * (rowH + rowGap)));
	}
	return SECTION_TOP + LAYER_LABELS.length * (rowH + rowGap) - rowGap;
}

function drawSection(parent: Container, title: string, y: number, width: number, tokens: PixiUITokens, fill: (section: Container, startX: number, contentMaxW: number) => number): number {
	const startX = Math.min(140, Math.max(92, Math.floor(width * 0.28)));
	const contentMaxW = Math.max(40, width - startX - 12);
	const section = new Container();
	section.position.set(0, y);
	const surface = new Surface({ width, height: SECTION_MIN_H, tone: "sunken", tokens });
	section.addChild(surface);
	const titleLbl = label(title, tokens.type.sizeSm, tokens.color.textMuted, tokens);
	truncateText(titleLbl, Math.max(0, startX - 20));
	section.addChild(at(titleLbl, 12, 16));
	const contentEnd = fill(section, startX, contentMaxW);
	const heightNeeded = Math.max(SECTION_MIN_H, contentEnd + SECTION_BOTTOM);
	surface.resize(width, heightNeeded);
	parent.addChild(section);
	return y + heightNeeded + SECTION_GAP;
}

function drawScrollDemo(parent: Container, x: number, y: number, width: number, height: number, tokens: PixiUITokens, state: GalleryState): void {
	const w = Math.max(80, Math.floor(width));
	const h = Math.max(40, Math.floor(height));
	const demo = new Container();
	demo.position.set(x, y);
	demo.addChild(new Surface({ width: w, height: h, tone: "sunken", tokens }));
	const titleLbl = label("Scroll Panel", tokens.type.sizeSm, tokens.color.textMuted, tokens);
	truncateText(titleLbl, Math.max(0, w - 24));
	demo.addChild(at(titleLbl, 12, 10));

	const contentTop = 34;
	const contentH = Math.max(1, h - contentTop - 10);
	const trackW = 10;
	const trackInset = 10;
	const innerW = Math.max(1, w - (trackInset + trackW + 8) - 14);

	const itemH = 18;
	const items = 14;
	const totalContentH = items * itemH + 6;

	const mask = new Graphics();
	mask.rect(14, contentTop, innerW, contentH).fill(0xffffff);
	demo.addChild(mask);

	const content = new Container();
	for (let i = 0; i < items; i++) {
		const lbl = label(`Inspector field ${i + 1}`, tokens.type.sizeXs, tokens.color.text, tokens);
		truncateText(lbl, innerW - 4);
		content.addChild(at(lbl, 14, contentTop + i * itemH));
	}
	content.mask = mask;
	demo.addChild(content);

	const trackTop = 10;
	const trackH = Math.max(24, h - 20);
	const trackX = w - trackInset - trackW;
	const track = new Surface({ width: trackW, height: trackH, tone: "scrollTrack", tokens });
	demo.addChild(at(track, trackX, trackTop));

	const maxScroll = Math.max(0, totalContentH - contentH);
	const thumbH = maxScroll > 0
		? Math.max(24, Math.floor(trackH * (contentH / totalContentH)))
		: trackH;
	const thumb = new Surface({ width: trackW, height: thumbH, tone: "scrollThumb", tokens });
	demo.addChild(thumb);

	let scrollY = Math.max(0, Math.min(state.scrollOffset, maxScroll));
	const apply = (): void => {
		content.y = -Math.round(scrollY);
		const range = trackH - thumbH;
		const t = maxScroll > 0 ? scrollY / maxScroll : 0;
		thumb.position.set(trackX, trackTop + Math.round(range * t));
	};
	apply();

	if (maxScroll <= 0) {
		parent.addChild(demo);
		return;
	}

	demo.eventMode = "static";
	demo.hitArea = new Rectangle(0, 0, w, h);
	demo.on("wheel", (e: FederatedWheelEvent) => {
		const next = Math.max(0, Math.min(maxScroll, scrollY + e.deltaY));
		if (next === scrollY) return;
		scrollY = next;
		apply();
		state.onScroll(scrollY);
	});

	thumb.eventMode = "static";
	thumb.cursor = "grab";
	let dragging = false;
	let dragStartGlobalY = 0;
	let dragStartScroll = 0;
	const range = (): number => Math.max(1, trackH - thumbH);
	thumb.on("pointerdown", (e: FederatedPointerEvent) => {
		dragging = true;
		dragStartGlobalY = e.global.y;
		dragStartScroll = scrollY;
		thumb.cursor = "grabbing";
	});
	thumb.on("globalpointermove", (e: FederatedPointerEvent) => {
		if (!dragging) return;
		const dy = e.global.y - dragStartGlobalY;
		const next = Math.max(0, Math.min(maxScroll, dragStartScroll + (dy / range()) * maxScroll));
		if (next === scrollY) return;
		scrollY = next;
		apply();
		state.onScroll(scrollY);
	});
	const endDrag = (): void => {
		if (!dragging) return;
		dragging = false;
		thumb.cursor = "grab";
	};
	thumb.on("pointerup", endDrag);
	thumb.on("pointerupoutside", endDrag);

	parent.addChild(demo);
}

function makeSchemeChip(color: number, border: number, active: boolean, tokens: PixiUITokens): Container {
	const chip = new Container();
	const g = new Graphics();
	g.rect(0, 0, 26, 26)
		.fill(color)
		.rect(0, 0, 26, 26)
		.stroke({ color: active ? tokens.color.text : border, width: active ? 2 : 1, alignment: 0 });
	chip.addChild(g);
	return chip;
}

function makeToolButton(textValue: string, size: number, tokens: PixiUITokens, active: boolean): Container {
	const btn = new Container();
	btn.addChild(new Surface({ width: size, height: size, tone: active ? "toolActive" : "button", tokens }));
	const txt = label(textValue, 18, active ? tokens.color.accentText : tokens.color.text, tokens);
	txt.x = Math.round((size - txt.width) / 2);
	txt.y = Math.round((size - txt.height) / 2);
	btn.addChild(txt);
	return btn;
}

function makeButton(theme: Theme, textValue: string, role: string, width: number, height: number, tokens: PixiUITokens, disabled = false): Container {
	const palette = surfacePalette(roleTone(role), tokens);
	const text = new Text({
		text: textValue,
		style: {
			fontFamily: tokens.type.family,
			fontSize: textValue.length <= 2 ? 17 : tokens.type.sizeSm,
			fontWeight: tokens.type.weightBold,
			fill: disabled ? tokens.color.textSubtle : (palette.text ?? tokens.color.text),
			letterSpacing: 0,
		},
	});
	const btn = new FancyButton({
		defaultView: new NineSlice({ theme, role, width, height, tokens }),
		hoverView: new NineSlice({ theme, role: Roles.ButtonSmPressA, width, height, tokens }),
		pressedView: new NineSlice({ theme, role: Roles.ButtonSmPressA, width, height, tokens }),
		disabledView: new NineSlice({ theme, role: Roles.ButtonSmLockA, width, height, tokens }),
		text,
		padding: 5,
		animations: {
			hover: { props: { scale: { x: 1, y: 1 } }, duration: 1 },
			pressed: { props: { scale: { x: 1, y: 1 } }, duration: 1 },
		},
	});
	btn.enabled = !disabled;
	return btn as unknown as Container;
}

function label(textValue: string, size: number, fill: number, tokens: PixiUITokens): Text {
	return new Text({
		text: textValue,
		style: {
			fontFamily: tokens.type.family,
			fontSize: size,
			fontWeight: tokens.type.weightBold,
			fill,
			letterSpacing: 0,
		},
	});
}

function at<T extends Container | Text>(display: T, x: number, y: number): T {
	display.position.set(Math.round(x), Math.round(y));
	return display;
}

if (typeof document !== "undefined") {
	void bootPixiUIGallery().catch((err: unknown) => {
		const host = document.querySelector("[data-bx-pixi-ui-gallery]") as HTMLElement | null;
		if (!host) throw err;
		host.textContent = err instanceof Error ? err.message : String(err);
		host.style.color = "#ff5a6a";
		host.style.padding = "16px";
	});
}
