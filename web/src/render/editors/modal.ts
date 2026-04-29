// Boxland — editor modal helper.
//
// Drawn on the modal layer over the entire scene with a focus-trapped
// panel. Pixi-rendered through and through; the layer is hidden by
// default and the open() call shows it with a body Container the
// caller provides.
//
// Keyboard:
//   * Esc — calls onClose + dismisses
//   * Enter — clicks the primary button (if any)
//
// We don't trap pointer events globally yet (the modal layer's
// fullscreen scrim absorbs clicks below it); that's good enough
// for v1. Tab cycling between buttons is a future polish item.

import "./layout-init";
import { Container, Graphics, Text } from "pixi.js";
import { FancyButton } from "@pixi/ui";

import { NineSlice, Theme, Roles } from "../ui";
import type { ModalSpec, ModalButton } from "./types";

export interface ModalManagerOptions {
	theme: Theme;
	layer: Container;
}

export class ModalManager {
	private readonly theme: Theme;
	private readonly layer: Container;
	private active: ModalSpec | null = null;
	private root: Container | null = null;
	private keyHandler: ((e: KeyboardEvent) => void) | null = null;

	constructor(opts: ModalManagerOptions) {
		this.theme = opts.theme;
		this.layer = opts.layer;
	}

	/** Open a modal. Replaces any currently-open modal in place
	 *  (rare, but cleanly defined: caller is responsible for not
	 *  stacking modals). */
	open(spec: ModalSpec): void {
		if (this.active) this.close();
		this.active = spec;
		this.root = this.buildModal(spec);
		this.layer.addChild(this.root);
		this.layer.visible = true;
		this.bindKeys();
	}

	/** Close the current modal. */
	close(): void {
		if (!this.active) return;
		const spec = this.active;
		const root = this.root;
		this.active = null;
		this.root = null;
		this.layer.visible = false;
		if (root) {
			this.layer.removeChild(root);
			root.destroy({ children: true });
		}
		this.unbindKeys();
		spec.onClose?.();
	}

	private buildModal(spec: ModalSpec): Container {
		const root = new Container();
		root.layout = {
			width: "100%",
			height: "100%",
			justifyContent: "center",
			alignItems: "center",
		};

		// Scrim — fullscreen translucent overlay so the layout
		// underneath dims while the modal is open. Pure Graphics;
		// not interactive (clicks fall through to the outer
		// modal-layer container which absorbs them via Pixi's
		// hit-test on a non-empty bounds).
		const scrim = new Graphics();
		scrim.layout = { position: "absolute", top: 0, left: 0, width: "100%", height: "100%" };
		// We don't yet know the scene dims; rely on the layout
		// system to size the scrim by setting its bounds
		// post-mount via a generous rect.
		scrim.rect(0, 0, 4096, 4096).fill({ color: 0x000000, alpha: 0.4 });
		root.addChild(scrim);

		// Panel — NineSlice frame with header / body / footer
		// stacked vertically.
		const w = spec.width ?? 480;
		const h = spec.height ?? 320;
		const panel = new Container();
		panel.layout = {
			width: w, height: h,
			flexDirection: "column",
			padding: 12,
			gap: 12,
		};
		panel.addChild(new NineSlice({ theme: this.theme, role: Roles.FrameStandard, width: w, height: h }));

		// Header — title text.
		const header = new Container();
		header.layout = { width: "100%", height: 22, alignItems: "center" };
		const title = new Text({
			text: spec.title,
			style: {
				fontFamily: "ui-sans-serif, system-ui, sans-serif",
				fontSize: 14,
				fill: 0xe8ecf2,
			},
		});
		header.addChild(title);
		panel.addChild(header);

		// Body — the caller's container.
		const body = new Container();
		body.layout = { width: "100%", flex: 1, minHeight: 0 };
		body.addChild(spec.body);
		panel.addChild(body);

		// Footer — button row.
		const footer = new Container();
		footer.layout = {
			width: "100%",
			height: 32,
			flexDirection: "row",
			justifyContent: "flex-end",
			gap: 8,
		};
		for (const b of spec.buttons) {
			footer.addChild(this.makeButton(spec, b));
		}
		panel.addChild(footer);

		root.addChild(panel);
		return root;
	}

	private makeButton(spec: ModalSpec, b: ModalButton): Container {
		const role = b.disabled
			? Roles.ButtonSmLockA
			: b.primary
				? Roles.ButtonSmPressA
				: Roles.ButtonSmReleaseA;
		void role;
		const w = 96;
		const h = 28;
		const btn = new FancyButton({
			defaultView: new NineSlice({ theme: this.theme, role: Roles.ButtonSmReleaseA, width: w, height: h }),
			hoverView:   new NineSlice({ theme: this.theme, role: Roles.ButtonSmReleaseA, width: w, height: h }),
			pressedView: new NineSlice({ theme: this.theme, role: Roles.ButtonSmPressA,   width: w, height: h }),
			disabledView: new NineSlice({ theme: this.theme, role: Roles.ButtonSmLockA,   width: w, height: h }),
			text: b.label,
			textOffset: { x: 0, y: -2 },
		});
		btn.enabled = !b.disabled;
		btn.layout = { width: w, height: h, alignSelf: "center" };
		btn.onPress.connect(() => {
			void Promise.resolve(b.onPress?.()).finally(() => {
				if (b.closesModal && this.active === spec) {
					this.close();
				}
			});
		});
		return btn as unknown as Container;
	}

	private bindKeys(): void {
		const handler = (e: KeyboardEvent): void => {
			if (!this.active) return;
			if (e.key === "Escape") {
				e.preventDefault();
				this.close();
				return;
			}
			if (e.key === "Enter") {
				const primary = this.active.buttons.find((b) => b.primary && !b.disabled);
				if (primary) {
					e.preventDefault();
					void Promise.resolve(primary.onPress?.()).finally(() => {
						if (primary.closesModal && this.active) this.close();
					});
				}
			}
		};
		this.keyHandler = handler;
		document.addEventListener("keydown", handler);
	}

	private unbindKeys(): void {
		if (this.keyHandler) {
			document.removeEventListener("keydown", this.keyHandler);
			this.keyHandler = null;
		}
	}
}
