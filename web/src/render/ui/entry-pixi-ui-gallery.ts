import { Application, Container, Text } from "pixi.js";
import { FancyButton } from "@pixi/ui";

import { NineSlice } from "./nine-slice";
import { Roles, Theme } from "./theme";
import { Surface } from "./surface";
import { pixiUITokens, surfacePalette, type SurfaceTone } from "./tokens";

const TOKENS = pixiUITokens;

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

	const render = (): void => {
		const width = Math.max(320, Math.floor(host.clientWidth || 960));
		const height = Math.max(280, Math.floor(host.clientHeight || 640));
		app.renderer.resize(width, height);
		renderGallery(root, width, height);
	};
	render();

	const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(render);
	observer?.observe(host);

	return { app, root, host, observer };
}

function renderGallery(root: Container, width: number, height: number): void {
	root.removeChildren().forEach((c) => c.destroy({ children: true }));
	const theme = Theme.fromEntries([]);

	const header = addSurface(root, 0, 0, width, 74, "raised", 0);
	void header;
	root.addChild(at(label("Pixi UI Gallery", TOKENS.type.sizeLg, TOKENS.color.text), 16, 14));
	root.addChild(at(label("Canonical editor components built with @pixi/ui and @pixi/layout", TOKENS.type.sizeSm, TOKENS.color.textMuted), 16, 42));

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

	drawPanel(root, "Surfaces", pad, bodyY, leftW, leftH, (panel) => {
		let y = 42;
		for (const tone of ["panel", "raised", "sunken", "button", "buttonActive", "buttonDisabled", "slot", "slotSelected", "scrollTrack", "scrollThumb", "input"] as SurfaceTone[]) {
			if (y + 28 > leftH - 10) break;
			const swatch = new Surface({ width: 56, height: 24, tone });
			panel.addChild(at(swatch, 14, y));
			const p = surfacePalette(tone);
			panel.addChild(at(label(`${tone}  #${p.fill.toString(16).padStart(6, "0")}`, TOKENS.type.sizeXs, TOKENS.color.textMuted), 82, y + 5));
			y += 31;
		}
	});

	drawPanel(root, "Editor Composition", rightX, rightY, rightW, rightH, (panel) => {
		let y = 42;
		y = drawSection(panel, "Buttons", y, rightW - 28, (section, sx) => {
			section.addChild(at(makeButton(theme, "Default", Roles.ButtonSmReleaseA, 104, 30), sx, 9));
			section.addChild(at(makeButton(theme, "Active", Roles.ButtonSmPressA, 104, 30), sx + 112, 9));
			if (rightW > 520) {
				section.addChild(at(makeButton(theme, "Disabled", Roles.ButtonSmLockA, 104, 30, true), sx + 224, 9));
			}
		});
		y = drawSection(panel, "Toolbar", y, rightW - 28, (section, sx) => {
			let x = sx;
			for (const icon of ["✎", "▣", "◇", "↶", "↷", "⧉"]) {
				section.addChild(at(makeButton(theme, icon, Roles.ButtonSmReleaseA, 34, 30), x, 9));
				x += 42;
			}
		});
		y = drawSection(panel, "Layer Rows", y, rightW - 28, (section, sx) => {
			for (let i = 0; i < 3; i++) {
				const active = i === 0;
				const rowW = Math.max(180, rightW - sx - 42);
				const row = new Container();
				row.addChild(new Surface({
					width: rowW,
					height: 42,
					tone: active ? "buttonActive" : "panel",
					accentEdge: active ? "left" : "none",
				}));
				row.addChild(at(label(i === 0 ? "base" : i === 1 ? "decoration" : "lighting", TOKENS.type.sizeSm, active ? TOKENS.color.accent : TOKENS.color.text), 12, 7));
				row.addChild(at(label(`tile  ord ${i}`, TOKENS.type.sizeXs, TOKENS.color.textMuted), 12, 24));
				section.addChild(at(row, sx, 8 + i * 48));
			}
		}, 164);
		if (y < rightH - 70) {
			drawScrollDemo(panel, 14, y + 8, rightW - 28, Math.min(130, rightH - y - 24));
		}
	});
}

function drawPanel(root: Container, title: string, x: number, y: number, width: number, height: number, fill: (panel: Container) => void): void {
	const panel = new Container();
	panel.position.set(x, y);
	panel.addChild(new Surface({ width, height, tone: "panel" }));
	panel.addChild(at(label(title, TOKENS.type.sizeMd, TOKENS.color.accent), 14, 14));
	fill(panel);
	root.addChild(panel);
}

function drawSection(parent: Container, title: string, y: number, width: number, fill: (section: Container, startX: number) => void, height = 50): number {
	const section = new Container();
	section.position.set(14, y);
	section.addChild(new Surface({ width, height, tone: "sunken" }));
	section.addChild(at(label(title, TOKENS.type.sizeSm, TOKENS.color.textMuted), 12, 17));
	fill(section, Math.min(150, Math.max(98, Math.floor(width * 0.28))));
	parent.addChild(section);
	return y + height + 10;
}

function drawScrollDemo(parent: Container, x: number, y: number, width: number, height: number): void {
	const demo = new Container();
	demo.position.set(x, y);
	demo.addChild(new Surface({ width, height, tone: "sunken" }));
	demo.addChild(at(label("Scroll Panel", TOKENS.type.sizeSm, TOKENS.color.textMuted), 12, 10));
	for (let i = 0; i < 4; i++) {
		demo.addChild(at(label(`Inspector field ${i + 1}`, TOKENS.type.sizeXs, TOKENS.color.text), 14, 36 + i * 22));
	}
	const track = new Surface({ width: 10, height: height - 20, tone: "scrollTrack", radius: 3 });
	demo.addChild(at(track, width - 20, 10));
	const thumb = new Surface({ width: 10, height: Math.max(32, Math.floor((height - 20) * 0.45)), tone: "scrollThumb", radius: 3 });
	demo.addChild(at(thumb, width - 20, 24));
	parent.addChild(demo);
}

function addSurface(parent: Container, x: number, y: number, width: number, height: number, tone: SurfaceTone, radius?: number): Surface {
	const opts = radius === undefined ? { width, height, tone } : { width, height, tone, radius };
	const s = new Surface(opts);
	s.position.set(x, y);
	parent.addChild(s);
	return s;
}

function makeButton(theme: Theme, textValue: string, role: string, width: number, height: number, disabled = false): Container {
	const text = new Text({
		text: textValue,
		style: {
			fontFamily: TOKENS.type.family,
			fontSize: textValue.length <= 2 ? 17 : TOKENS.type.sizeSm,
			fontWeight: TOKENS.type.weightBold,
			fill: disabled ? TOKENS.color.textSubtle : TOKENS.color.text,
			letterSpacing: 0,
		},
	});
	const btn = new FancyButton({
		defaultView: new NineSlice({ theme, role, width, height }),
		hoverView: new NineSlice({ theme, role: Roles.ButtonSmPressA, width, height }),
		pressedView: new NineSlice({ theme, role: Roles.ButtonSmPressA, width, height }),
		disabledView: new NineSlice({ theme, role: Roles.ButtonSmLockA, width, height }),
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

function label(textValue: string, size: number, fill: number): Text {
	return new Text({
		text: textValue,
		style: {
			fontFamily: TOKENS.type.family,
			fontSize: size,
			fontWeight: TOKENS.type.weightBold,
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
