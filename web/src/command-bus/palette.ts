// Boxland — Cmd-K command palette UI.
//
// Lists every command on a CommandBus, filtered by a fuzzy substring match.
// Up/Down navigates, Enter dispatches, Esc closes. Designed to drop onto
// any surface that has a CommandBus — Sandbox is the primary user (per
// PLAN.md §6g) but Mapmaker and the design-tool shell can mount it too.
//
// CSS lives in /static/css/pixel.css under .bx-cmdk.

import type { CommandBus } from "./bus";
import type { Command } from "./types";

export interface PaletteOptions {
	/**
	 * Filter which commands the palette exposes. Default: every registered
	 * command (callers can hide e.g. designer-only commands when in the
	 * player realm).
	 */
	filter?: (cmd: Command<unknown>) => boolean;

	/** Placeholder text for the search input. Defaults to Lorem-Ipsum. */
	placeholder?: string;
}

export class CommandPalette {
	private root: HTMLElement | null = null;
	private input: HTMLInputElement | null = null;
	private list: HTMLOListElement | null = null;
	private items: Command<unknown>[] = [];
	private highlightedIdx = 0;
	private readonly opts: Required<Pick<PaletteOptions, "placeholder">> & PaletteOptions;
	private previouslyFocused: HTMLElement | null = null;

	constructor(private readonly bus: CommandBus, options: PaletteOptions = {}) {
		this.opts = {
			placeholder: "Lorem ipsum search…",
			...options,
		};
	}

	/** True iff the palette is currently mounted in the DOM. */
	isOpen(): boolean { return this.root !== null; }

	/** Toggle visibility. */
	toggle(): void {
		this.isOpen() ? this.close() : this.open();
	}

	/**
	 * Mount the palette onto the document (if not already open) and focus
	 * the search input.
	 */
	open(): void {
		if (this.isOpen()) return;

		this.previouslyFocused = (document.activeElement as HTMLElement | null) ?? null;
		this.root = document.createElement("div");
		this.root.className = "bx-cmdk";
		this.root.setAttribute("role", "dialog");
		this.root.setAttribute("aria-label", "Command palette");
		this.root.setAttribute("data-bx-dismissible", "");

		this.input = document.createElement("input");
		this.input.type = "text";
		this.input.placeholder = this.opts.placeholder;
		this.input.setAttribute("aria-label", this.opts.placeholder);
		this.input.autocomplete = "off";
		this.input.addEventListener("input", () => this.refresh());
		this.input.addEventListener("keydown", (e) => this.onKeyDown(e));

		this.list = document.createElement("ol");
		this.list.setAttribute("role", "listbox");

		this.root.appendChild(this.input);
		this.root.appendChild(this.list);
		this.root.addEventListener("bx:dismiss", () => this.close());
		document.body.appendChild(this.root);

		this.refresh();
		this.input.focus();
	}

	/** Unmount and restore focus to the previously focused element. */
	close(): void {
		if (!this.root) return;
		this.root.remove();
		this.root = null;
		this.input = null;
		this.list = null;
		this.items = [];
		this.highlightedIdx = 0;
		this.previouslyFocused?.focus?.();
		this.previouslyFocused = null;
	}

	/** Recompute the visible list from the current input value. */
	private refresh(): void {
		if (!this.input || !this.list) return;
		const query = this.input.value.trim().toLowerCase();
		const all = this.bus.all();
		const filtered = (this.opts.filter ? all.filter(this.opts.filter) : all)
			.filter((c) => fuzzyMatch(query, c.id) || fuzzyMatch(query, c.description));

		this.items = filtered.slice(0, 40); // cap visible to keep dispatch snappy
		this.highlightedIdx = 0;
		this.renderList();
	}

	private renderList(): void {
		if (!this.list) return;
		this.list.replaceChildren(...this.items.map((cmd, idx) => this.renderItem(cmd, idx)));
	}

	private renderItem(cmd: Command<unknown>, idx: number): HTMLLIElement {
		const li = document.createElement("li");
		li.setAttribute("role", "option");
		li.setAttribute("data-cmd-id", cmd.id);
		if (idx === this.highlightedIdx) li.setAttribute("aria-selected", "true");

		const label = document.createElement("span");
		label.textContent = cmd.description;
		li.appendChild(label);

		const combo = this.bus.hotkeyFor(cmd.id);
		if (combo) {
			const kbd = document.createElement("kbd");
			kbd.className = "bx-small bx-muted";
			kbd.textContent = combo;
			kbd.style.float = "right";
			li.appendChild(kbd);
		}

		li.addEventListener("click", () => this.invoke(idx));
		return li;
	}

	private async invoke(idx: number): Promise<void> {
		const cmd = this.items[idx];
		if (!cmd) return;
		this.close();
		await this.bus.dispatch(cmd.id, undefined);
	}

	private onKeyDown(e: KeyboardEvent): void {
		switch (e.key) {
			case "ArrowDown":
				e.preventDefault();
				this.move(1);
				break;
			case "ArrowUp":
				e.preventDefault();
				this.move(-1);
				break;
			case "Enter":
				e.preventDefault();
				void this.invoke(this.highlightedIdx);
				break;
			case "Escape":
				e.preventDefault();
				this.close();
				break;
		}
	}

	private move(delta: number): void {
		if (this.items.length === 0) return;
		const next = (this.highlightedIdx + delta + this.items.length) % this.items.length;
		this.highlightedIdx = next;
		this.renderList();
		const sel = this.list?.querySelector('[aria-selected="true"]') as HTMLElement | null;
		sel?.scrollIntoView({ block: "nearest" });
	}
}

/**
 * Cheap fuzzy substring match. Returns true if every character of `query`
 * appears in `target` in order (not necessarily adjacent). Empty queries
 * always match.
 */
export function fuzzyMatch(query: string, target: string): boolean {
	if (!query) return true;
	const t = target.toLowerCase();
	let qi = 0;
	for (let i = 0; i < t.length && qi < query.length; i++) {
		if (t[i] === query[qi]) qi++;
	}
	return qi === query.length;
}
