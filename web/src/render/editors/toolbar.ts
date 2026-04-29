// Boxland — editor toolbar.
//
// Top-of-screen action strip. Renders one button per ToolbarAction
// in a flex row inside the toolbar slot. Pixi-rendered through and
// through; uses `@pixi/ui` FancyButton with theme-skinned states.

import "./layout-init";
import { Container } from "pixi.js";
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
	private readonly title = new Container(); // optional title bar item; reserved for future

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
		this.slot.addChild(this.title);
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
		const w = entry?.width ?? 64;
		const h = entry?.height ?? this.height;

		// FancyButton expects defaultView/pressedView/hoverView/
		// disabledView Containers. We give it three NineSlice bgs
		// keyed off the theme so each state has its own art.
		const btn = new FancyButton({
			defaultView: this.bg(Roles.ButtonSmReleaseA, w, h),
			hoverView: this.bg(Roles.ButtonSmReleaseA, w, h),
			pressedView: this.bg(Roles.ButtonSmPressA, w, h),
			disabledView: this.bg(Roles.ButtonSmLockA, w, h),
			text: action.label,
			textOffset: { x: 0, y: -2 },
			animations: {
				hover: { props: { scale: { x: 1.02, y: 1.02 } }, duration: 80 },
				pressed: { props: { scale: { x: 0.98, y: 0.98 } }, duration: 60 },
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
