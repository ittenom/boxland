// Boxland — editor toolbar.
//
// Top-of-screen action strip. Renders one button per ToolbarAction
// in a flex row inside the toolbar slot. Pixi-rendered through and
// through; uses `@pixi/ui` FancyButton with theme-skinned states.

import "./layout-init";
import { Container, Graphics, Text } from "pixi.js";
import { FancyButton } from "@pixi/ui";

import { NineSlice, Theme, Roles } from "../ui";
import type { ToolbarAction } from "./types";

export interface ToolbarOptions {
	theme: Theme;
	slot: Container;
	height?: number;
	buttonWidth?: number;
}

/** Toolbar wraps a slot Container and lets the surface push action
 *  buttons. Re-render-friendly: pushing a new action set replaces
 *  the previous buttons in place. */
export class Toolbar {
	private readonly theme: Theme;
	private readonly slot: Container;
	private readonly height: number;
	private readonly buttonWidth: number;
	private readonly handlers = new Map<string, () => void>();
	private tooltip: Container | null = null;

	constructor(opts: ToolbarOptions) {
		this.theme = opts.theme;
		this.slot = opts.slot;
		this.height = opts.height ?? 30;
		this.buttonWidth = opts.buttonWidth ?? 104;
	}

	/** Bind a handler that fires when the action's button is
	 *  pressed. Idempotent; re-binding replaces. */
	onAction(id: string, handler: () => void): void {
		this.handlers.set(id, handler);
	}

	/** Replace the toolbar's button row. We rebuild every time
	 *  the surface state changes — toolbar action sets are short
	 *  (5–10 entries) so this is cheap. */
	render(actions: readonly ToolbarAction[]): void {
		// Clear previous buttons but keep the slot's background
		// (child index 0).
		while (this.slot.children.length > 1) {
			const c = this.slot.children[this.slot.children.length - 1];
			if (!c) break;
			this.slot.removeChild(c);
			c.destroy();
		}
		for (const a of actions) {
			this.slot.addChild(this.makeButton(a));
		}
	}

	private makeButton(action: ToolbarAction): Container {
		const role = action.disabled
			? Roles.ButtonSmLockA
			: action.active
				? Roles.ButtonSmPressA
				: Roles.ButtonSmReleaseA;
		const display = action.icon ?? (action.hotkey ? `${action.label} ${action.hotkey}` : action.label);
		const tooltip = action.tooltip ?? (action.hotkey ? `${action.label} - ${action.hotkey}` : action.label);
		const isIcon = Boolean(action.icon);
		const w = isIcon ? this.height : this.buttonWidth;
		const h = this.height;
		const text = new Text({
			text: display,
			style: {
				fontFamily: "DM Mono, Consolas, monospace",
				fontSize: isIcon ? 18 : 11,
				fontWeight: "700",
				fill: action.disabled ? 0x738096 : action.active ? 0xffd84a : 0xe8ecf2,
				letterSpacing: 0,
			},
		});

		// FancyButton expects defaultView/pressedView/hoverView/
		// disabledView Containers. We give it three NineSlice bgs
		// keyed off the theme so each state has its own art.
		const btn = new FancyButton({
			defaultView: this.bg(role, w, h),
			hoverView: this.bg(Roles.ButtonSmPressA, w, h),
			pressedView: this.bg(Roles.ButtonSmPressA, w, h),
			disabledView: this.bg(Roles.ButtonSmLockA, w, h),
			text,
			padding: 5,
			textOffset: { x: 0, y: -1 },
			animations: {
				hover: { props: { scale: { x: 1, y: 1 } }, duration: 1 },
				pressed: { props: { scale: { x: 1, y: 1 } }, duration: 1 },
			},
		});
		btn.enabled = !action.disabled;
		btn.layout = {
			width: w, height: h,
			alignSelf: "center",
		};
		btn.onPress.connect(() => {
			const fn = this.handlers.get(action.id);
			if (fn) fn();
		});
		const out = btn as unknown as Container;
		out.on("pointerover", () => this.showTooltip(out, tooltip));
		out.on("pointerout", () => this.hideTooltip());
		out.on("pointerdown", () => this.hideTooltip());
		return out;
	}

	private bg(role: string, w: number, h: number): Container {
		return new NineSlice({ theme: this.theme, role, width: w, height: h });
	}

	private showTooltip(anchor: Container, textValue: string): void {
		this.hideTooltip();
		const label = new Text({
			text: textValue,
			style: {
				fontFamily: "DM Mono, Consolas, monospace",
				fontSize: 10,
				fontWeight: "700",
				fill: 0xffd84a,
				letterSpacing: 0,
			},
		});
		const padX = 8;
		const padY = 5;
		const w = Math.ceil(label.width) + padX * 2;
		const h = Math.ceil(label.height) + padY * 2;
		const tip = new Container();
		const bg = new Graphics();
		bg.rect(0, 0, w, h)
			.fill(0x101827)
			.rect(0, 0, w, h)
			.stroke({ color: 0x6ea0ff, width: 1, alignment: 1 });
		tip.addChild(bg);
		label.position.set(padX, padY - 1);
		tip.addChild(label);
		const x = anchor.position.x;
		const y = anchor.position.y + this.height + 6;
		tip.position.set(x, y);
		tip.zIndex = 1000;
		this.tooltip = tip;
		this.slot.sortableChildren = true;
		this.slot.addChild(tip);
	}

	private hideTooltip(): void {
		if (!this.tooltip) return;
		this.slot.removeChild(this.tooltip);
		this.tooltip.destroy();
		this.tooltip = null;
	}
}
