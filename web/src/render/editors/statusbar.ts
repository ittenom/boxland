// Boxland — editor statusbar.
//
// Bottom-of-screen status strip. Renders one Text per slot in a
// flex row. Surface-specific entry scripts push slot updates as
// state changes (cursor cell, active tool, dirty indicator, etc.).

import "./layout-init";
import { Container, Text } from "pixi.js";

import type { Theme } from "../ui";
import type { StatusbarSlot } from "./types";

export interface StatusbarOptions {
	theme: Theme;
	slot: Container;
}

const FONT_SIZE = 11;

export class Statusbar {
	private readonly slot: Container;
	private readonly textBySlot = new Map<string, Text>();

	constructor(opts: StatusbarOptions) {
		void opts.theme; // reserved for future theme-driven text styles
		this.slot = opts.slot;
	}

	/** Render the slot list. Called whenever the surface's status
	 *  state changes; we update existing Text objects in place
	 *  rather than rebuild — keeps GC pressure flat across frames. */
	render(slots: readonly StatusbarSlot[]): void {
		const seen = new Set<string>();
		for (const s of slots) {
			seen.add(s.id);
			let t = this.textBySlot.get(s.id);
			if (!t) {
				t = new Text({
					text: s.text,
					style: {
						fontFamily: "ui-sans-serif, system-ui, sans-serif",
						fontSize: FONT_SIZE,
						fill: 0xa9b0c0,
					},
				});
				t.layout = { alignSelf: "center" };
				this.textBySlot.set(s.id, t);
				this.slot.addChild(t);
			}
			t.text = s.text;
			if (s.color !== undefined) {
				(t.style as { fill: number }).fill = s.color;
			} else {
				(t.style as { fill: number }).fill = 0xa9b0c0;
			}
		}
		// Drop slots that disappeared.
		for (const [id, t] of this.textBySlot) {
			if (!seen.has(id)) {
				t.destroy();
				this.slot.removeChild(t);
				this.textBySlot.delete(id);
			}
		}
	}
}
