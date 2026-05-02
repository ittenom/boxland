// Boxland — renderer UI layout primitives.
//
// Three small helpers callers compose to keep Pixi widgets inside their
// containers regardless of host size. None of them allocate Containers
// of their own; they mutate the displays handed to them.

import { Graphics } from "pixi.js";
import type { Container, Text } from "pixi.js";

import type { PixiUITokens } from "./tokens";

const ELLIPSIS = "…";

/** Trim a Text to a single line that fits inside `maxWidth`, replacing
 *  the trailing characters with an ellipsis. No-op when the text
 *  already fits. Sets the text to "" when even the ellipsis is wider
 *  than maxWidth. */
export function truncateText(text: Text, maxWidth: number): Text {
	if (maxWidth <= 0) {
		text.text = "";
		return text;
	}
	if (text.width <= maxWidth) return text;
	const original = text.text;
	text.text = ELLIPSIS;
	if (text.width > maxWidth) {
		text.text = "";
		return text;
	}
	let lo = 0;
	let hi = original.length;
	let best = 0;
	while (lo <= hi) {
		const mid = (lo + hi) >> 1;
		text.text = original.slice(0, mid) + ELLIPSIS;
		if (text.width <= maxWidth) {
			best = mid;
			lo = mid + 1;
		} else {
			hi = mid - 1;
		}
	}
	text.text = original.slice(0, best) + ELLIPSIS;
	return text;
}

/** Enable Pixi's word-wrap for a Text. Use when wrapping is preferable
 *  to truncation (paragraphs, multi-line captions). The caller is
 *  responsible for sizing whatever container holds the wrapped text:
 *  read text.height after the call. */
export function wrapText(text: Text, maxWidth: number): Text {
	text.style.wordWrap = true;
	text.style.wordWrapWidth = Math.max(1, maxWidth);
	return text;
}

export interface FlowRowOptions {
	maxWidth: number;
	gap?: number;
	rowGap?: number;
}

export interface FlowRowResult {
	width: number;
	height: number;
	rows: number;
}

/** Pack `items` into rows that fit inside `maxWidth`, positioning each
 *  in turn (top-left = 0,0). Items wider than maxWidth get their own
 *  row regardless. Returns the bounding size + row count so the parent
 *  surface can resize to fit. */
export function flowRow(items: readonly Container[], opts: FlowRowOptions): FlowRowResult {
	const gap = opts.gap ?? 8;
	const rowGap = opts.rowGap ?? gap;
	const maxWidth = Math.max(1, opts.maxWidth);
	let x = 0;
	let y = 0;
	let rowH = 0;
	let maxRowW = 0;
	let rows = items.length === 0 ? 0 : 1;
	for (const item of items) {
		const w = readSize(item, "width");
		const h = readSize(item, "height");
		if (x > 0 && x + w > maxWidth) {
			maxRowW = Math.max(maxRowW, x - gap);
			x = 0;
			y += rowH + rowGap;
			rowH = 0;
			rows++;
		}
		item.position.set(Math.round(x), Math.round(y));
		x += w + gap;
		if (h > rowH) rowH = h;
	}
	maxRowW = Math.max(maxRowW, x - gap);
	return { width: Math.max(0, maxRowW), height: y + rowH, rows };
}

function readSize(item: Container, axis: "width" | "height"): number {
	const v = (item as unknown as Record<string, unknown>)[axis];
	return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

export interface DotGridOptions {
	width: number;
	height: number;
	tokens: PixiUITokens;
	spacing?: number;
	radius?: number;
	color?: number;
}

/** Draw a grid of small dots in `color` (defaults to tokens.color.dotGrid)
 *  across the given width/height. Returns the Graphics so the caller can
 *  position / cache / clear it. Used as a page-background pattern under
 *  paper-style cards. */
export function drawDotGrid(opts: DotGridOptions): Graphics {
	const spacing = Math.max(4, opts.spacing ?? opts.tokens.space.dotGrid);
	const radius = Math.max(0.5, opts.radius ?? 1.25);
	const color = opts.color ?? opts.tokens.color.dotGrid;
	const g = new Graphics();
	const startOffset = spacing / 2;
	for (let y = startOffset; y < opts.height; y += spacing) {
		for (let x = startOffset; x < opts.width; x += spacing) {
			g.circle(x, y, radius);
		}
	}
	g.fill({ color, alpha: 0.7 });
	return g;
}
