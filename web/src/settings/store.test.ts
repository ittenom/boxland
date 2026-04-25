// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from "vitest";

import { loadLocal, saveLocal, lsKey, makeRemoteSaver } from "./store";
import { DEFAULT_SETTINGS } from "./types";

beforeEach(() => {
	localStorage.clear();
});

describe("local storage roundtrip", () => {
	it("loadLocal returns DEFAULT_SETTINGS when nothing is stored", () => {
		expect(loadLocal("designer")).toEqual(DEFAULT_SETTINGS);
	});

	it("saveLocal persists per-realm", () => {
		const s = { ...DEFAULT_SETTINGS, font: "Kubasta" as const };
		saveLocal("player", s);
		expect(localStorage.getItem(lsKey("player"))).toContain("Kubasta");
		// Designer realm is unaffected.
		expect(localStorage.getItem(lsKey("designer"))).toBeNull();
	});

	it("loadLocal coerces malformed JSON back to defaults", () => {
		localStorage.setItem(lsKey("designer"), "not-json");
		expect(loadLocal("designer")).toEqual(DEFAULT_SETTINGS);
	});

	it("loadLocal coerces invalid fields", () => {
		localStorage.setItem(lsKey("designer"), JSON.stringify({ font: "Bogus" }));
		expect(loadLocal("designer").font).toBe(DEFAULT_SETTINGS.font);
	});
});

describe("makeRemoteSaver", () => {
	it("debounces PUTs and flushes the latest payload", async () => {
		vi.useFakeTimers();
		const fetched: any[] = [];
		const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation((async (url: string, init?: RequestInit) => {
			fetched.push({ url, body: init?.body });
			return new Response("ok", { status: 200 });
		}) as typeof fetch);
		const saver = makeRemoteSaver("/x/me", 100);
		const a = { ...DEFAULT_SETTINGS, font: "AtariGames" as const };
		const b = { ...DEFAULT_SETTINGS, font: "Kubasta"   as const };
		saver.schedule(a);
		saver.schedule(b);
		// Nothing fired yet.
		expect(fetched).toHaveLength(0);
		// Advance past the debounce.
		await vi.advanceTimersByTimeAsync(150);
		expect(fetched).toHaveLength(1);
		expect(JSON.parse(fetched[0].body)).toMatchObject({ font: "Kubasta" });
		fetchSpy.mockRestore();
		vi.useRealTimers();
	});

	it("flush() forces an immediate PUT", async () => {
		const fetched: any[] = [];
		const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation((async (url: string, init?: RequestInit) => {
			fetched.push({ url, body: init?.body });
			return new Response("ok", { status: 200 });
		}) as typeof fetch);
		const saver = makeRemoteSaver("/x/me", 9999);
		saver.schedule({ ...DEFAULT_SETTINGS, font: "BIOSfontII" });
		await saver.flush();
		expect(fetched).toHaveLength(1);
		fetchSpy.mockRestore();
	});

	it("cancel() drops a pending payload without sending", async () => {
		const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("ok", { status: 200 }));
		const saver = makeRemoteSaver("/x/me", 50);
		saver.schedule(DEFAULT_SETTINGS);
		saver.cancel();
		await new Promise((r) => setTimeout(r, 80));
		expect(fetchSpy).not.toHaveBeenCalled();
		fetchSpy.mockRestore();
	});
});
