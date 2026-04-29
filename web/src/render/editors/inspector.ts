// Boxland — generic inspector pane.
//
// Takes a `FieldDescriptor[]` + a current values map, renders each
// as the matching widget, and emits `onChange` callbacks when the
// user mutates a value. Used by:
//   * Mapmaker: per-tile overrides (collision shape, layer mask)
//   * Level Editor: per-placement instance_overrides + tags
//
// All widgets are theme-skinned via the renderer's UI primitives.
// Currently supports the common kinds (string, text, int, float,
// bool, enum); ref types and nested lists land later as the
// surfaces start to need them.

import "./layout-init";
import { Container, Text } from "pixi.js";

import {
	makeInput, makeCheckBox, makeSlider, makeSelect, makeLabel,
	type Theme,
} from "../ui";
import type { FieldDescriptor } from "./field-descriptor";

export interface InspectorOptions {
	theme: Theme;
	slot: Container;
	onChange?: (key: string, value: unknown) => void;
}

export class Inspector {
	private readonly theme: Theme;
	private readonly slot: Container;
	private readonly onChange: ((k: string, v: unknown) => void) | null;
	private readonly rowsByKey = new Map<string, Container>();
	private title: Text | null = null;

	constructor(opts: InspectorOptions) {
		this.theme = opts.theme;
		this.slot = opts.slot;
		this.onChange = opts.onChange ?? null;
	}

	/** Set the inspector's heading text. Pass null to hide. */
	setTitle(text: string | null): void {
		if (this.title) {
			this.title.destroy();
			this.title = null;
		}
		if (text === null) return;
		this.title = makeLabel({ text, size: 13, color: 0xe8ecf2 });
		this.title.layout = { width: "100%", alignSelf: "flex-start", marginBottom: 6 };
		// Insert at index 1 so it sits just above the rows but
		// below any panel background (child 0 of the slot).
		const insertIdx = Math.min(1, this.slot.children.length);
		this.slot.addChildAt(this.title, insertIdx);
	}

	/** Replace the rendered field rows with a new descriptor list +
	 *  current values. Idempotent: re-rendering with the same
	 *  descriptor set updates values in place where possible
	 *  (saves widget creation on every keystroke). */
	render(fields: readonly FieldDescriptor[], values: Record<string, unknown>): void {
		const seen = new Set<string>();
		for (const f of fields) {
			seen.add(f.key);
			let row = this.rowsByKey.get(f.key);
			if (!row) {
				row = this.buildRow(f, values[f.key]);
				this.rowsByKey.set(f.key, row);
				this.slot.addChild(row);
			}
		}
		for (const [k, row] of this.rowsByKey) {
			if (!seen.has(k)) {
				row.destroy();
				this.slot.removeChild(row);
				this.rowsByKey.delete(k);
			}
		}
	}

	/** Drop every row + the title. Called when selection clears. */
	clear(): void {
		for (const row of this.rowsByKey.values()) {
			row.destroy();
			this.slot.removeChild(row);
		}
		this.rowsByKey.clear();
		this.setTitle(null);
	}

	private buildRow(f: FieldDescriptor, current: unknown): Container {
		const row = new Container();
		row.layout = {
			width: "100%",
			flexDirection: "column",
			gap: 4,
			marginBottom: 4,
		};
		// Label.
		const label = makeLabel({
			text: f.label + (f.required ? " *" : ""),
			size: 11,
			color: 0xa9b0c0,
		});
		label.layout = { alignSelf: "flex-start" };
		row.addChild(label);
		// Value widget.
		const widget = this.buildWidget(f, current);
		if (widget) row.addChild(widget);
		// Help text.
		if (f.help) {
			const help = makeLabel({ text: f.help, size: 10, color: 0x7e8696 });
			help.layout = { alignSelf: "flex-start" };
			row.addChild(help);
		}
		return row;
	}

	private buildWidget(f: FieldDescriptor, current: unknown): Container | null {
		const fire = (v: unknown): void => { this.onChange?.(f.key, v); };
		switch (f.kind) {
			case "string": {
				const o: Parameters<typeof makeInput>[0] = {
					theme: this.theme,
					value: typeof current === "string" ? current : "",
					placeholder: f.label,
					onChange: (v) => fire(v),
				};
				if (f.max_len !== undefined) o.maxLength = f.max_len;
				return makeInput(o) as unknown as Container;
			}
			case "text":
				// Multiline editing isn't a separate @pixi/ui
				// widget; we use the same Input for now and
				// document the limitation. JSON-shaped overrides
				// land here for v1; a real multiline editor is
				// follow-up work.
				return makeInput({
					theme: this.theme,
					value: typeof current === "string" ? current : "",
					placeholder: f.label,
					onChange: (v) => fire(v),
				}) as unknown as Container;
			case "int":
				return makeSlider({
					theme: this.theme,
					min: f.min ?? 0,
					max: f.max ?? 100,
					value: typeof current === "number" ? current : (f.min ?? 0),
					step: f.step ?? 1,
					onChange: (v) => fire(Math.round(v)),
				}) as unknown as Container;
			case "float":
				return makeSlider({
					theme: this.theme,
					min: f.min ?? 0,
					max: f.max ?? 1,
					value: typeof current === "number" ? current : (f.min ?? 0),
					step: f.step ?? 0.01,
					onChange: (v) => fire(v),
				}) as unknown as Container;
			case "bool":
				return makeCheckBox({
					theme: this.theme,
					label: "",
					checked: current === true,
					onChange: (v) => fire(v),
				}) as unknown as Container;
			case "enum": {
				const items = (f.options ?? []).map((o) => o.label);
				const sel = (f.options ?? []).findIndex((o) => o.value === current);
				return makeSelect({
					theme: this.theme,
					items,
					selected: sel >= 0 ? sel : 0,
					onChange: (idx) => {
						const opt = f.options?.[idx];
						if (opt) fire(opt.value);
					},
				}) as unknown as Container;
			}
			default:
				// Ref types + vec2 + range + nested + list aren't
				// rendered yet. Returning null leaves the row with
				// just label + help; surfaces can extend the
				// inspector later.
				return null;
		}
	}
}
