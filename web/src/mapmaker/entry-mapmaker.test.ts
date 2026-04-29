// @vitest-environment jsdom
//
// Boot-config reader smoke tests. Following the sandbox/level-editor
// pattern: verify the entry script gracefully handles missing host
// elements + missing data-* attributes. The full bootMapmaker() path
// (EditorApp + EditorWire + WS) needs a real browser to exercise
// meaningfully — covered by manual smoke.

import { describe, expect, it } from "vitest";
import { bootMapmaker } from "./entry-mapmaker";

describe("mapmaker boot guards", () => {
	it("returns null when no host element is present", async () => {
		document.body.innerHTML = "";
		const result = await bootMapmaker();
		expect(result).toBeNull();
	});

	it("rejects when host is missing required data-* attributes", async () => {
		document.body.innerHTML = `<main data-bx-mapmaker-host></main>`;
		await expect(bootMapmaker()).rejects.toThrow(/missing/);
	});
});
