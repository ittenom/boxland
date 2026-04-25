// Boxland — settings/rebinder.ts
//
// "Press a key to rebind" UI helper. Mounts onto a row in the
// Settings page table; on click, captures the next keydown and
// updates the bindings map. Esc cancels.

import { canonicalizeCombo, comboFromEvent, type CommandBus } from "@command-bus";

export interface RebinderOptions {
	bus: CommandBus;
	onChange: (combo: string, commandId: string) => void;
}

/** Render the rebinder table body. One row per registered command;
 *  each shows the current combo (if any) and a "rebind" button that
 *  starts a capture. */
export function renderRebinderRows(
	tbody: HTMLElement,
	bus: CommandBus,
	bindings: Record<string, string>,
	onChange: (combo: string | null, commandId: string) => void,
): void {
	tbody.innerHTML = "";
	const cmds = bus.all();
	if (cmds.length === 0) {
		tbody.innerHTML = `<tr class="bx-muted"><td colspan="3" class="bx-small">No commands registered.</td></tr>`;
		return;
	}
	const inverted: Record<string, string> = {};
	for (const [combo, id] of Object.entries(bindings)) inverted[id] = combo;

	for (const cmd of cmds) {
		const tr = document.createElement("tr");
		const id = cmd.id;
		// Prefer the user's binding from settings if any; fall back to
		// whatever the bus currently has (the surface's defaults).
		const combo = inverted[id] ?? bus.hotkeyFor(id) ?? "";

		const tdAction = document.createElement("td");
		tdAction.textContent = cmd.description;
		tr.appendChild(tdAction);

		const tdBinding = document.createElement("td");
		tdBinding.className = "bx-mono bx-small";
		tdBinding.textContent = combo || "—";
		tdBinding.dataset.bxRebinderCombo = id;
		tr.appendChild(tdBinding);

		const tdAction2 = document.createElement("td");
		const btn = document.createElement("button");
		btn.type = "button";
		btn.className = "bx-btn bx-btn--ghost bx-small";
		btn.textContent = combo ? "Rebind" : "Bind";
		btn.addEventListener("click", () => {
			const startText = btn.textContent;
			btn.textContent = "Press key…";
			btn.disabled = true;
			captureNext({ except: btn })
				.then((next) => {
					if (!next) {
						btn.textContent = startText;
						btn.disabled = false;
						return;
					}
					if (next === "__clear__") {
						onChange(null, id);
						tdBinding.textContent = "—";
						btn.textContent = "Bind";
					} else {
						onChange(next, id);
						tdBinding.textContent = next;
						btn.textContent = "Rebind";
					}
					btn.disabled = false;
				});
		});
		tdAction2.appendChild(btn);
		tr.appendChild(tdAction2);

		tbody.appendChild(tr);
	}
}

/** Listen for the next keydown that produces a canonical combo.
 *  Returns the combo, or "__clear__" if the user pressed Backspace
 *  (clears the binding), or null if Esc cancels. */
function captureNext(opts: { except: HTMLElement }): Promise<string | "__clear__" | null> {
	return new Promise((resolve) => {
		const onKey = (e: KeyboardEvent): void => {
			if (e.key === "Escape") {
				cleanup();
				resolve(null);
				return;
			}
			if (e.key === "Backspace" || e.key === "Delete") {
				e.preventDefault();
				cleanup();
				resolve("__clear__");
				return;
			}
			const combo = comboFromEvent(e);
			if (!combo) return;
			const c = canonicalizeCombo(combo);
			if (!c) return;
			e.preventDefault();
			cleanup();
			resolve(c);
		};
		const onClickAway = (e: MouseEvent): void => {
			if (e.target === opts.except) return;
			cleanup();
			resolve(null);
		};
		const cleanup = (): void => {
			window.removeEventListener("keydown", onKey, true);
			window.removeEventListener("mousedown", onClickAway, true);
		};
		window.addEventListener("keydown", onKey, true);
		window.addEventListener("mousedown", onClickAway, true);
	});
}
