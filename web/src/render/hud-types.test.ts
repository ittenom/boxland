// Tests for the dependency-free HUD layout / binding helpers. Intentionally
// avoids any Pixi imports so it runs fast and headless.

import { describe, expect, it } from "vitest";

import {
	anchorOrigin,
	bindingString,
	emptyLayout,
	makeLookup,
	parseBinding,
	parseTemplateBindings,
	renderTemplate,
	type Anchor,
	type HudValue,
} from "./hud-types";

describe("hud-types: parseBinding", () => {
	it("entity bindings need a sub", () => {
		expect(parseBinding("entity:host:hp_pct")).toEqual({ kind: "entity", key: "host", sub: "hp_pct" });
		expect(() => parseBinding("entity:host")).toThrow();
		expect(() => parseBinding("entity:nobody:hp_pct")).toThrow();
	});

	it("flag bindings need a snake_case key", () => {
		expect(parseBinding("flag:gold")).toEqual({ kind: "flag", key: "gold" });
		expect(() => parseBinding("flag:")).toThrow();
		expect(() => parseBinding("flag:HasQuest")).toThrow();
		expect(() => parseBinding("flag:has-quest")).toThrow();
	});

	it("time bindings only allow realm_clock|wall", () => {
		expect(parseBinding("time:realm_clock")).toEqual({ kind: "time", key: "realm_clock" });
		expect(parseBinding("time:wall")).toEqual({ kind: "time", key: "wall" });
		expect(() => parseBinding("time:sundial")).toThrow();
	});
});

describe("hud-types: bindingString round-trip", () => {
	it("matches the canonical string", () => {
		for (const s of ["entity:host:hp_pct", "flag:gold", "time:realm_clock"]) {
			expect(bindingString(parseBinding(s))).toBe(s);
		}
	});
});

describe("hud-types: parseTemplateBindings", () => {
	it("collects every {kind:key} substitution", () => {
		const refs = parseTemplateBindings("Gold: {flag:gold}  HP: {entity:host:hp_pct}");
		expect(refs).toHaveLength(2);
		expect(refs[0]).toEqual({ kind: "flag", key: "gold" });
		expect(refs[1]).toEqual({ kind: "entity", key: "host", sub: "hp_pct" });
	});

	it("rejects unterminated braces", () => {
		expect(() => parseTemplateBindings("Gold: {flag:gold")).toThrow();
	});

	it("rejects unknown bindings inside the template", () => {
		expect(() => parseTemplateBindings("X: {flag:HasQuest}")).toThrow();
	});
});

describe("hud-types: renderTemplate", () => {
	it("substitutes known bindings and leaves unknowns as literal", () => {
		const values = new Map<string, HudValue>();
		values.set("flag:gold", { kind: "int", value: 42 });
		const out = renderTemplate("Gold: {flag:gold}  HP: {entity:host:hp_pct}", makeLookup(values));
		expect(out).toBe("Gold: 42  HP: ");
	});

	it("preserves text around substitutions", () => {
		const out = renderTemplate("[no bindings here]", makeLookup(new Map()));
		expect(out).toBe("[no bindings here]");
	});

	it("treats malformed substitutions as literal text", () => {
		const out = renderTemplate("Gold: {flag:HasQuest}", makeLookup(new Map()));
		expect(out).toBe("Gold: {flag:HasQuest}");
	});
});

describe("hud-types: anchorOrigin", () => {
	const cases: Array<[Anchor, number, number, { x: number; y: number; signX: 1 | -1; signY: 1 | -1 }]> = [
		["top-left",     480, 320, { x: 0,   y: 0,   signX: 1,  signY: 1  }],
		["top-center",   480, 320, { x: 240, y: 0,   signX: 1,  signY: 1  }],
		["top-right",    480, 320, { x: 480, y: 0,   signX: -1, signY: 1  }],
		["mid-left",     480, 320, { x: 0,   y: 160, signX: 1,  signY: 1  }],
		["mid-center",   480, 320, { x: 240, y: 160, signX: 1,  signY: 1  }],
		["mid-right",    480, 320, { x: 480, y: 160, signX: -1, signY: 1  }],
		["bottom-left",  480, 320, { x: 0,   y: 320, signX: 1,  signY: -1 }],
		["bottom-center",480, 320, { x: 240, y: 320, signX: 1,  signY: -1 }],
		["bottom-right", 480, 320, { x: 480, y: 320, signX: -1, signY: -1 }],
	];
	for (const [anchor, vw, vh, want] of cases) {
		it(`${anchor} -> ${JSON.stringify(want)}`, () => {
			expect(anchorOrigin(anchor, vw, vh)).toEqual(want);
		});
	}
});

describe("hud-types: emptyLayout", () => {
	it("returns the canonical empty layout", () => {
		const l = emptyLayout();
		expect(l.v).toBe(1);
		expect(Object.keys(l.anchors)).toHaveLength(0);
	});
});
