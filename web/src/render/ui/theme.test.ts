// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { Theme, Roles } from "./theme";

const sample = [
	{
		role: Roles.FrameStandard,
		entityTypeId: 1,
		assetUrl: "/design/assets/blob/1",
		nineSlice: { left: 8, top: 8, right: 8, bottom: 8 },
		width: 24, height: 24,
	},
	{
		role: Roles.ButtonMdReleaseA,
		entityTypeId: 2,
		assetUrl: "/design/assets/blob/2",
		nineSlice: { left: 6, top: 6, right: 6, bottom: 6 },
		width: 48, height: 24,
	},
];

describe("Theme.fromEntries", () => {
	it("indexes entries by role", () => {
		const t = Theme.fromEntries(sample);
		expect(t.size()).toBe(2);
		expect(t.get(Roles.FrameStandard)?.entityTypeId).toBe(1);
		expect(t.get(Roles.ButtonMdReleaseA)?.entityTypeId).toBe(2);
	});

	it("returns null for unknown roles", () => {
		const t = Theme.fromEntries(sample);
		expect(t.get("does-not-exist")).toBeNull();
	});

	it("roles() lists every bound role", () => {
		const t = Theme.fromEntries(sample);
		const roles = t.roles();
		expect(roles).toContain(Roles.FrameStandard);
		expect(roles).toContain(Roles.ButtonMdReleaseA);
		expect(roles).toHaveLength(2);
	});

	it("textureFor returns null when the role is unknown", () => {
		const t = Theme.fromEntries(sample);
		expect(t.textureFor("does-not-exist")).toBeNull();
	});

	it("textureFor returns null when the role has an empty url", () => {
		const t = Theme.fromEntries([{
			role: "test",
			entityTypeId: 99,
			assetUrl: "",
			nineSlice: { left: 1, top: 1, right: 1, bottom: 1 },
			width: 1, height: 1,
		}]);
		expect(t.textureFor("test")).toBeNull();
	});
});

describe("Theme texture coalescing", () => {
	it("collapses concurrent textureFor() calls for the same url to one Promise", () => {
		const t = Theme.fromEntries(sample);
		const a = t.textureFor(Roles.FrameStandard);
		const b = t.textureFor(Roles.FrameStandard);
		// Same URL should yield the same cached Promise reference.
		expect(a).toBe(b);
	});

	it("returns distinct promises for distinct urls", () => {
		const t = Theme.fromEntries(sample);
		const a = t.textureFor(Roles.FrameStandard);
		const b = t.textureFor(Roles.ButtonMdReleaseA);
		expect(a).not.toBe(b);
	});
});
