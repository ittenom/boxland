// @vitest-environment jsdom
//
// We exercise the small surface of the entry module that doesn't
// need Pixi: the boot-config reader. The rest of bootLevelEditor()
// spins up a BoxlandApp + EditorHarness — covered by their per-
// module tests under @render — plus pointer + keyboard wiring that
// would need a full DOM integration harness we don't have yet.

import { describe, expect, it } from "vitest";
import { bootLevelEditor } from "./entry-level-editor";

describe("level-editor boot config reader", () => {
	it("rejects when host is missing data-* attributes", async () => {
		document.body.innerHTML = `
			<main data-bx-level-editor></main>
			<div data-bx-level-canvas-host></div>
		`;
		const host = document.querySelector("[data-bx-level-editor]") as HTMLElement;
		const canvasHost = document.querySelector("[data-bx-level-canvas-host]") as HTMLElement;
		await expect(bootLevelEditor(host, canvasHost)).rejects.toThrow(/missing data-/);
	});

	it("returns null when the host elements aren't in the DOM", async () => {
		document.body.innerHTML = "";
		const result = await bootLevelEditor(
			document.querySelector("[data-bx-level-editor]") as HTMLElement,
			document.querySelector("[data-bx-level-canvas-host]") as HTMLElement,
		);
		expect(result).toBeNull();
	});
});
