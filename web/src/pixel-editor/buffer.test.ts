// @vitest-environment jsdom
import "./_test_setup";
import { describe, expect, it } from "vitest";
import { PixelBuffer } from "./buffer";
import { TRANSPARENT } from "./types";

describe("PixelBuffer", () => {
	it("starts fully transparent", () => {
		const b = new PixelBuffer(4, 4);
		expect(b.get(0, 0)).toEqual(TRANSPARENT);
		expect(b.get(3, 3)).toEqual(TRANSPARENT);
	});

	it("set + get round-trips", () => {
		const b = new PixelBuffer(8, 8);
		b.set(2, 3, { r: 255, g: 100, b: 50, a: 200 });
		expect(b.get(2, 3)).toEqual({ r: 255, g: 100, b: 50, a: 200 });
	});

	it("returns transparent for out-of-bounds reads", () => {
		const b = new PixelBuffer(4, 4);
		expect(b.get(-1, 0)).toEqual(TRANSPARENT);
		expect(b.get(0, 99)).toEqual(TRANSPARENT);
	});

	it("ignores out-of-bounds writes", () => {
		const b = new PixelBuffer(2, 2);
		b.set(99, 99, { r: 1, g: 2, b: 3, a: 4 });
		expect(b.get(0, 0)).toEqual(TRANSPARENT);
	});

	it("toImageData returns a fresh copy", () => {
		const b = new PixelBuffer(2, 2);
		b.set(0, 0, { r: 1, g: 2, b: 3, a: 4 });
		const id = b.toImageData();
		expect(id.data[0]).toBe(1);
		// Mutating the ImageData copy must not affect the buffer.
		id.data[0] = 99;
		expect(b.get(0, 0).r).toBe(1);
	});
});
