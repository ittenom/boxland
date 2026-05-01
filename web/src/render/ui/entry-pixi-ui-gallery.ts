import { Application, Container, Graphics, Text } from "pixi.js";
import { FancyButton } from "@pixi/ui";

import { NineSlice } from "./nine-slice";
import { Roles, Theme } from "./theme";
import { Surface, roleTone } from "./surface";
import { pixiUITokens, surfacePalette, type PixiUIColorTokens, type PixiUITokens, type SurfaceTone } from "./tokens";

const TOKENS = pixiUITokens;
const SURFACE_TONES: readonly SurfaceTone[] = ["panel", "raised", "sunken", "button", "buttonActive", "buttonDisabled", "toolActive", "slot", "slotSelected", "scrollTrack", "scrollThumb", "input"];

interface Scheme {
	name: string;
	color: Partial<PixiUIColorTokens>;
}

const SCHEMES: readonly Scheme[] = [
	{ name: "Boxland", color: {} },
	{
		name: "Forge",
		color: {
			canvas: 0x11130f,
			surface: 0x171b14,
			surfaceRaised: 0x222818,
			surfaceSunken: 0x0c100b,
			surfaceMuted: 0x2c3120,
			border: 0x434a33,
			borderStrong: 0x707a49,
			text: 0xf1efd8,
			textMuted: 0xb8b294,
			textSubtle: 0x7c8168,
			accent: 0xf08f39,
			accentSoft: 0x735136,
			accentText: 0x17120b,
			focus: 0xffc36a,
			disabled: 0x34382a,
		},
	},
	{
		name: "Signal",
		color: {
			canvas: 0x071312,
			surface: 0x0d1d1b,
			surfaceRaised: 0x14302c,
			surfaceSunken: 0x05100f,
			surfaceMuted: 0x1c3c37,
			border: 0x28534d,
			borderStrong: 0x42a097,
			text: 0xe5fff9,
			textMuted: 0x9fc7c0,
			textSubtle: 0x6b8987,
			accent: 0x6fffd6,
			accentSoft: 0x1f6c72,
			accentText: 0x041414,
			focus: 0xb6fff1,
			disabled: 0x213632,
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

export async function bootPixiUIGallery(): Promise<GalleryRuntime | null> {
	const host = document.querySelector("[data-bx-pixi-ui-gallery]") as HTMLElement | null;
	if (!host) return null;
	host.textContent = "";

	const app = new Application();
	await app.init({
		resizeTo: host,
		background: TOKENS.color.canvas,
		antialias: false,
		roundPixels: true,
		autoDensity: true,
		resolution: window.devicePixelRatio || 1,
	});
	app.canvas.style.imageRendering = "pixelated";
	app.canvas.style.display = "block";
	app.canvas.style.width = "100%";
	app.canvas.style.height = "100%";
	host.appendChild(app.canvas);

	const root = new Container();
	app.stage.addChild(root);

	let activeScheme = 0;
	const render = (): void => {
		const width = Math.max(320, Math.floor(host.clientWidth || 960));
		const height = Math.max(280, Math.floor(host.clientHeight || 640));
		const tokens = tokensForScheme(SCHEMES[activeScheme] ?? DEFAULT_SCHEME);
		app.renderer.resize(width, height);
		app.renderer.background.color = tokens.color.canvas;
		renderGallery(root, width, height, tokens, activeScheme, (idx) => {
			activeScheme = idx;
			render();
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
	};
}

function renderGallery(root: Container, width: number, height: number, tokens: PixiUITokens, activeScheme: number, onScheme: (idx: number) => void): void {
	root.removeChildren().forEach((c) => c.destroy({ children: true }));
	const theme = Theme.fromEntries([]);

	const header = addSurface(root, 0, 0, width, 74, "raised", tokens);
	void header;
	root.addChild(at(label("Pixi UI Gallery", tokens.type.sizeLg, tokens.color.text, tokens), 16, 14));
	root.addChild(at(label("Canonical editor components built with @pixi/ui and @pixi/layout", tokens.type.sizeSm, tokens.color.textMuted, tokens), 16, 42));

	const gap = 16;
	const pad = 16;
	const bodyY = 74 + pad;
	const bodyH = Math.max(1, height - bodyY - pad);
	const leftW = width < 760 ? Math.min(300, width - pad * 2) : 300;
	const rightX = width < 760 ? pad : pad + leftW + gap;
	const rightW = width < 760 ? width - pad * 2 : Math.max(260, width - rightX - pad);
	const leftH = width < 760 ? Math.floor(bodyH * 0.44) : bodyH;
	const rightY = width < 760 ? bodyY + leftH + gap : bodyY;
	const rightH = width < 760 ? Math.max(1, height - rightY - pad) : bodyH;

	drawPanel(root, "Surfaces", pad, bodyY, leftW, leftH, tokens, (panel) => {
		let y = 42;
		panel.addChild(at(label("Scheme", tokens.type.sizeXs, tokens.color.textMuted, tokens), 14, y));
		y += 18;
		for (let i = 0; i < SCHEMES.length; i++) {
			const scheme = SCHEMES[i] ?? DEFAULT_SCHEME;
			const schemeTokens = tokensForScheme(scheme);
			const chip = makeSchemeChip(schemeTokens.color.accent, schemeTokens.color.borderStrong, i === activeScheme, tokens);
			chip.eventMode = "static";
			chip.cursor = "pointer";
			chip.on("pointertap", () => onScheme(i));
			panel.addChild(at(chip, 14 + i * 34, y));
		}
		panel.addChild(at(label((SCHEMES[activeScheme] ?? DEFAULT_SCHEME).name, tokens.type.sizeXs, tokens.color.text, tokens), 124, y + 6));
		y += 42;
		for (const tone of SURFACE_TONES) {
			if (y + 24 > leftH - 10) break;
			const swatch = new Surface({ width: 24, height: 24, tone, tokens });
			panel.addChild(at(swatch, 14, y));
			const p = surfacePalette(tone, tokens);
			panel.addChild(at(label(`${tone}  #${p.fill.toString(16).padStart(6, "0")}`, tokens.type.sizeXs, tokens.color.textMuted, tokens), 48, y + 5));
			y += 30;
		}
	});

	drawPanel(root, "Editor Composition", rightX, rightY, rightW, rightH, tokens, (panel) => {
		let y = 42;
		y = drawSection(panel, "Buttons", y, rightW - 28, (section, sx) => {
			section.addChild(at(makeButton(theme, "Default", Roles.ButtonSmReleaseA, 104, 30, tokens), sx, 9));
			section.addChild(at(makeButton(theme, "Active", Roles.ButtonSmPressA, 104, 30, tokens), sx + 112, 9));
			if (rightW > 520) {
				section.addChild(at(makeButton(theme, "Disabled", Roles.ButtonSmLockA, 104, 30, tokens, true), sx + 224, 9));
			}
		}, tokens);
		y = drawSection(panel, "Toolbar", y, rightW - 28, (section, sx) => {
			let x = sx;
			const toolSize = 32;
			for (const [idx, icon] of ["✎", "▣", "◇", "↶", "↷", "⧉"].entries()) {
				section.addChild(at(makeToolButton(icon, toolSize, tokens, idx === 0), x, 9));
				x += toolSize + 8;
			}
		}, tokens);
		y = drawSection(panel, "Layer Rows", y, rightW - 28, (section, sx) => {
			for (let i = 0; i < 3; i++) {
				const active = i === 0;
				const rowW = Math.max(180, rightW - sx - 42);
				const row = new Container();
				row.addChild(new Surface({
					width: rowW,
					height: 42,
					tone: active ? "buttonActive" : "panel",
					tokens,
					accentEdge: active ? "left" : "none",
				}));
				row.addChild(at(label(i === 0 ? "base" : i === 1 ? "decoration" : "lighting", tokens.type.sizeSm, active ? tokens.color.accent : tokens.color.text, tokens), 12, 7));
				row.addChild(at(label(`tile  ord ${i}`, tokens.type.sizeXs, tokens.color.textMuted, tokens), 12, 24));
				section.addChild(at(row, sx, 8 + i * 48));
			}
		}, tokens, 164);
		if (y < rightH - 70) {
			drawScrollDemo(panel, 14, y + 8, rightW - 28, Math.min(130, rightH - y - 24), tokens);
		}
	});
}

function drawPanel(root: Container, title: string, x: number, y: number, width: number, height: number, tokens: PixiUITokens, fill: (panel: Container) => void): void {
	const panel = new Container();
	panel.position.set(x, y);
	panel.addChild(new Surface({ width, height, tone: "panel", tokens }));
	panel.addChild(at(label(title, tokens.type.sizeMd, tokens.color.accent, tokens), 14, 14));
	fill(panel);
	root.addChild(panel);
}

function drawSection(parent: Container, title: string, y: number, width: number, fill: (section: Container, startX: number) => void, tokens: PixiUITokens, height = 50): number {
	const section = new Container();
	section.position.set(14, y);
	section.addChild(new Surface({ width, height, tone: "sunken", tokens }));
	section.addChild(at(label(title, tokens.type.sizeSm, tokens.color.textMuted, tokens), 12, 17));
	fill(section, Math.min(150, Math.max(98, Math.floor(width * 0.28))));
	parent.addChild(section);
	return y + height + 10;
}

function drawScrollDemo(parent: Container, x: number, y: number, width: number, height: number, tokens: PixiUITokens): void {
	const demo = new Container();
	demo.position.set(x, y);
	demo.addChild(new Surface({ width, height, tone: "sunken", tokens }));
	demo.addChild(at(label("Scroll Panel", tokens.type.sizeSm, tokens.color.textMuted, tokens), 12, 10));
	const content = new Container();
	const mask = new Graphics();
	const contentTop = 34;
	const contentH = Math.max(1, height - contentTop - 10);
	mask.rect(10, contentTop, Math.max(1, width - 38), contentH).fill(0xffffff);
	demo.addChild(mask);
	content.mask = mask;
	for (let i = 0; i < 6; i++) {
		content.addChild(at(label(`Inspector field ${i + 1}`, tokens.type.sizeXs, tokens.color.text, tokens), 14, contentTop + i * 18));
	}
	demo.addChild(content);
	const trackH = Math.max(24, height - 20);
	const track = new Surface({ width: 10, height: trackH, tone: "scrollTrack", tokens });
	demo.addChild(at(track, width - 20, 10));
	const thumb = new Surface({ width: 10, height: Math.max(24, Math.floor(trackH * 0.45)), tone: "scrollThumb", tokens });
	demo.addChild(at(thumb, width - 20, 24));
	parent.addChild(demo);
}

function addSurface(parent: Container, x: number, y: number, width: number, height: number, tone: SurfaceTone, tokens: PixiUITokens): Surface {
	const s = new Surface({ width, height, tone, tokens });
	s.position.set(x, y);
	parent.addChild(s);
	return s;
}

function makeSchemeChip(color: number, border: number, active: boolean, tokens: PixiUITokens): Container {
	const chip = new Container();
	const g = new Graphics();
	g.rect(0, 0, 26, 26)
		.fill(color)
		.rect(0, 0, 26, 26)
		.stroke({ color: active ? tokens.color.accentText : border, width: active ? 3 : 1, alignment: 1 });
	chip.addChild(g);
	return chip;
}

function makeToolButton(textValue: string, size: number, tokens: PixiUITokens, active: boolean): Container {
	const btn = new Container();
	btn.addChild(new Surface({ width: size, height: size, tone: active ? "toolActive" : "button", tokens }));
	btn.addChild(at(label(textValue, 17, active ? tokens.color.accentText : tokens.color.text, tokens), Math.floor(size / 2 - 7), 6));
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
