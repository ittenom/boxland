// Boxland — editor toolbar.
//
// Top-of-screen action strip. Renders one button per ToolbarAction
// in a flex row inside the toolbar slot. Pixi-rendered through and
// through; uses `@pixi/ui` FancyButton with theme-skinned states.

import "./layout-init";
import { Container, Text } from "pixi.js";
import { FancyButton } from "@pixi/ui";

import { NineSlice, Theme, Roles } from "../ui";
import type { ToolbarAction } from "./types";

export interface ToolbarOptions {
	theme: Theme;
	slot: Container;
	height?: number;
}

/** Toolbar wraps a slot Container and lets the surface push action
 *  buttons. Re-render-friendly: pushing a new action set replaces
 *  the previous buttons in place. */
export class Toolbar {
	private readonly theme: Theme;
	private readonly slot: Container;
	private readonly height: number;
	private readonly handlers = new Map<string, () => void>();

	constructor(opts: ToolbarOptions) {
		this.theme = opts.theme;
		this.slot = opts.slot;
		this.height = opts.height ?? 28;
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
		const entry = this.theme.get(role);
		const label = action.hotkey ? `${action.label} ${action.hotkey}` : action.label;
		const w = Math.max(entry?.width ?? 64, 18 + label.length * 7);
		const h = entry?.height ?? this.height;
		const text = new Text({
			text: label,
			style: {
				fontFamily: "DM Mono, Consolas, monospace",
				fontSize: 11,
				fontWeight: "700",
				fill: action.disabled ? 0x6f7b91 : action.active ? 0x10131c : 0xe8ecf2,
				letterSpacing: 0,
			},
		});

		// FancyButton expects defaultView/pressedView/hoverView/
		// disabledView Containers. We give it three NineSlice bgs
		// keyed off the theme so each state has its own art.
		const btn = new FancyButton({
			defaultView: this.bg(role, w, h),
			hoverView: this.bg(Roles.ButtonSmReleaseA, w, h),
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
		return btn as unknown as Container;
	}

	private bg(role: string, w: number, h: number): Container {
		return new NineSlice({ theme: this.theme, role, width: w, height: h });
	}
}
