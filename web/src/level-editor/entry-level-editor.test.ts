// @vitest-environment jsdom
//
// We exercise the small surface of the entry module that doesn't
// need Pixi or a WS server: the boot-config reader. The rest of
// bootLevelEditor() spins up an EditorApp + EditorWire + WS join,
// covered by their per-module tests under @render and @net.

import { describe, expect, it } from "vitest";
import { bootLevelEditor } from "./entry-level-editor";

describe("level-editor boot config reader", () => {
	it("rejects when host is missing data-* attributes", async () => {
		document.body.innerHTML = `<main data-bx-level-editor></main>`;
		const host = document.querySelector("[data-bx-level-editor]") as HTMLElement;
		await expect(bootLevelEditor(host)).rejects.toThrow(/missing data-/);
	});

	it("returns null when the host element isn't in the DOM", async () => {
		document.body.innerHTML = "";
		const result = await bootLevelEditor(
			document.querySelector("[data-bx-level-editor]") as HTMLElement,
		);
		expect(result).toBeNull();
	});
});
