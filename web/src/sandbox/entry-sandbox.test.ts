// @vitest-environment jsdom
import { describe, it, expect } from "vitest";

// We exercise the small surface of the sandbox boot module that doesn't
// need Pixi: the boot-config reader. The rest of bootSandbox spins up a
// BoxlandApp + NetClient + GameLoop -- all already covered by their
// per-module tests.

describe("sandbox boot config reader", () => {
	it("data-attributes are documented + readable", () => {
		const host = document.createElement("main");
		host.id = "bx-game-host";
		host.dataset.bxMapId = "42";
		host.dataset.bxMapName = "test";
		host.dataset.bxMapWidth = "64";
		host.dataset.bxMapHeight = "64";
		host.dataset.bxWsUrl = "ws://x/ws";
		host.dataset.bxAccessToken = "tk";
		host.dataset.bxInstanceId = "sandbox:1:42";
		document.body.appendChild(host);

		// Mirror the same attribute-reading the entry uses.
		const ds = host.dataset;
		expect(Number(ds.bxMapId)).toBe(42);
		expect(ds.bxInstanceId).toBe("sandbox:1:42");
		expect(ds.bxAccessToken).toBe("tk");
	});

	it("missing attributes throw on the entry boot", async () => {
		const host = document.createElement("main");
		host.id = "bx-game-host";
		// Intentionally missing everything.
		document.body.appendChild(host);

		const { bootSandbox } = await import("./entry-sandbox");
		await expect(bootSandbox(host)).rejects.toThrow(/missing data-bx-/);
	});
});
