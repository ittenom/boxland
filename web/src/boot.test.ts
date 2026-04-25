import { describe, expect, it } from "vitest";
import { BOOT_VERSION, detectSurface } from "./boot";

describe("boot", () => {
  it("exposes a non-empty version sentinel", () => {
    expect(BOOT_VERSION).not.toBe("");
  });

  it("returns 'unknown' when the body has no data-surface attribute", () => {
    const fakeDoc = { body: { dataset: {} as DOMStringMap } } as Document;
    expect(detectSurface(fakeDoc)).toBe("unknown");
  });

  it("returns the body data-surface attribute when set", () => {
    const fakeDoc = {
      body: { dataset: { surface: "mapmaker" } as DOMStringMap },
    } as Document;
    expect(detectSurface(fakeDoc)).toBe("mapmaker");
  });
});
