// Boxland — pixel editor.
//
// Construct one PixelEditor per modal. The editor owns a PixelBuffer and
// a CommandBus; tools (pencil/eraser/picker) register as Commands so undo
// and redo come for free from the bus.

import { CommandBus, type Command } from "@command-bus";
import { PixelBuffer } from "./buffer";
import type { EditorState, PixelEdit, RGBA, ToolID } from "./types";
import { TRANSPARENT } from "./types";

export interface EditorOptions {
	canvas: HTMLCanvasElement;       // host display canvas
	buffer: PixelBuffer;
	initialColor?: RGBA;
	initialZoom?: number;
}

export class PixelEditor {
	readonly bus = new CommandBus();
	readonly state: EditorState;
	private buffer: PixelBuffer;
	private canvas: HTMLCanvasElement;
	private currentStroke: PixelEdit[] = [];
	private painting = false;

	constructor(opts: EditorOptions) {
		this.canvas = opts.canvas;
		this.buffer = opts.buffer;
		this.state = {
			width: opts.buffer.width,
			height: opts.buffer.height,
			color: opts.initialColor ?? { r: 0, g: 0, b: 0, a: 255 },
			tool: "pencil",
			zoom: opts.initialZoom ?? 8,
		};

		this.registerTools();
		this.attachInput();
		this.render();
	}

	/** Switch active tool. */
	setTool(t: ToolID): void {
		this.state.tool = t;
	}

	/** Update the active paint color. */
	setColor(c: RGBA): void {
		this.state.color = c;
	}

	/** Re-render the buffer to the host canvas. */
	render(): void {
		this.buffer.blitTo(this.canvas, this.state.zoom);
	}

	/** Snapshot the buffer as a PNG Blob for upload. */
	async exportPNG(): Promise<Blob> {
		return this.buffer.toPNGBlob();
	}

	// ---- internals ----

	private registerTools(): void {
		// Pencil: a single command takes the whole stroke and is undone in one
		// step. The mouse handler builds the stroke incrementally by calling
		// `paintAt`, then commits it on mouseup.
		const pencil: Command<PixelEdit[]> = {
			id: "pencil.stroke",
			description: "Paint stroke",
			do: (edits) => {
				for (const e of edits) this.buffer.set(e.x, e.y, e.next);
				this.render();
			},
			undo: (edits) => {
				for (let i = edits.length - 1; i >= 0; i--) {
					const e = edits[i]!;
					this.buffer.set(e.x, e.y, e.prev);
				}
				this.render();
			},
		};
		this.bus.register(pencil);

		const eraser: Command<PixelEdit[]> = {
			id: "eraser.stroke",
			description: "Erase stroke",
			do: (edits) => {
				for (const e of edits) this.buffer.set(e.x, e.y, e.next);
				this.render();
			},
			undo: (edits) => {
				for (let i = edits.length - 1; i >= 0; i--) {
					const e = edits[i]!;
					this.buffer.set(e.x, e.y, e.prev);
				}
				this.render();
			},
		};
		this.bus.register(eraser);

		// Default hotkeys; the rebinder UI can override later.
		this.bus.register({
			id: "tool.pencil",
			description: "Pencil tool",
			do: () => this.setTool("pencil"),
		});
		this.bus.register({
			id: "tool.eraser",
			description: "Eraser tool",
			do: () => this.setTool("eraser"),
		});
		this.bus.register({
			id: "tool.picker",
			description: "Color picker",
			do: () => this.setTool("picker"),
		});
		this.bus.bindHotkey("B", "tool.pencil");
		this.bus.bindHotkey("E", "tool.eraser");
		this.bus.bindHotkey("I", "tool.picker");
		this.bus.register({
			id: "edit.undo",
			description: "Undo",
			do: () => { void this.bus.undo(); },
			whileTyping: false,
		});
		this.bus.register({
			id: "edit.redo",
			description: "Redo",
			do: () => { void this.bus.redo(); },
		});
		this.bus.bindHotkey("Mod+Z", "edit.undo");
		this.bus.bindHotkey("Mod+Shift+Z", "edit.redo");
	}

	private attachInput(): void {
		const c = this.canvas;
		c.addEventListener("mousedown", (e) => this.onPointerDown(e));
		window.addEventListener("mousemove", (e) => this.onPointerMove(e));
		window.addEventListener("mouseup", () => this.onPointerUp());
	}

	private onPointerDown(e: MouseEvent): void {
		const p = this.toBufferCoords(e);
		if (!p) return;
		if (this.state.tool === "picker") {
			this.setColor(this.buffer.get(p.x, p.y));
			return;
		}
		this.painting = true;
		this.currentStroke = [];
		this.paintAt(p.x, p.y);
	}

	private onPointerMove(e: MouseEvent): void {
		if (!this.painting) return;
		const p = this.toBufferCoords(e);
		if (!p) return;
		this.paintAt(p.x, p.y);
	}

	private onPointerUp(): void {
		if (!this.painting) return;
		this.painting = false;
		if (this.currentStroke.length === 0) return;
		const stroke = this.currentStroke;
		this.currentStroke = [];
		// Commit through the bus so undo/redo capture it.
		const id = this.state.tool === "eraser" ? "eraser.stroke" : "pencil.stroke";
		void this.bus.dispatch(id, stroke);
	}

	private paintAt(x: number, y: number): void {
		const next = this.state.tool === "eraser" ? TRANSPARENT : this.state.color;
		const prev = this.buffer.get(x, y);
		// Skip no-ops so undo doesn't get cluttered with redundant entries.
		if (sameColor(prev, next)) return;
		this.buffer.set(x, y, next);
		this.currentStroke.push({ x, y, prev, next });
		this.render();
	}

	private toBufferCoords(e: MouseEvent): { x: number; y: number } | null {
		const rect = this.canvas.getBoundingClientRect();
		const x = Math.floor(((e.clientX - rect.left) / rect.width) * this.state.width);
		const y = Math.floor(((e.clientY - rect.top) / rect.height) * this.state.height);
		if (x < 0 || y < 0 || x >= this.state.width || y >= this.state.height) return null;
		return { x, y };
	}
}

function sameColor(a: RGBA, b: RGBA): boolean {
	return a.r === b.r && a.g === b.g && a.b === b.b && a.a === b.a;
}
