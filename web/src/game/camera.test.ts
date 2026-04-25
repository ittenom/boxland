import { describe, it, expect } from "vitest";

import { CommandBus } from "@command-bus";
import {
	GameCamera,
	buildCameraToggleCommand,
	CAMERA_TOGGLE_COMMAND_ID,
	CAMERA_PAN_SUB_PER_MS,
} from "./camera";

describe("GameCamera", () => {
	it("starts in follow mode and snapshot returns the target", () => {
		const c = new GameCamera();
		expect(c.getMode()).toBe("follow");
		expect(c.snapshot({ cx: 100, cy: 200 })).toEqual({ cx: 100, cy: 200 });
	});

	it("setMode + snapshot returns the free position once mode is free-cam", () => {
		const c = new GameCamera();
		c.syncFreeFrom({ cx: 50, cy: 75 });
		c.setMode("free-cam");
		expect(c.snapshot({ cx: 1000, cy: 2000 })).toEqual({ cx: 50, cy: 75 });
	});

	it("toggleMode flips between modes", () => {
		const c = new GameCamera();
		expect(c.toggleMode()).toBe("free-cam");
		expect(c.toggleMode()).toBe("follow");
	});

	it("pan() is a no-op in follow mode", () => {
		const c = new GameCamera();
		c.pan(1000, 0, 100);
		expect(c.snapshot({ cx: 0, cy: 0 })).toEqual({ cx: 0, cy: 0 });
	});

	it("pan() moves the free position by intent * speed * dt", () => {
		const c = new GameCamera();
		c.setMode("free-cam");
		c.pan(1000, 0, 100);
		const expected = (1000 * CAMERA_PAN_SUB_PER_MS * 100 / 1000) | 0;
		expect(c.snapshot({ cx: 0, cy: 0 }).cx).toBe(expected);
	});

	it("syncFreeFrom positions the free-cam at the given coords", () => {
		const c = new GameCamera();
		c.syncFreeFrom({ cx: 500, cy: 500 });
		c.setMode("free-cam");
		expect(c.snapshot({ cx: 0, cy: 0 })).toEqual({ cx: 500, cy: 500 });
	});

	it("setFreePos teleports the camera in free-cam mode", () => {
		const c = new GameCamera();
		c.setMode("free-cam");
		c.setFreePos(123, 456);
		expect(c.snapshot({ cx: 0, cy: 0 })).toEqual({ cx: 123, cy: 456 });
	});

	it("zero intent does not change free position", () => {
		const c = new GameCamera();
		c.setMode("free-cam");
		c.syncFreeFrom({ cx: 100, cy: 100 });
		c.pan(0, 0, 1000);
		expect(c.snapshot({ cx: 0, cy: 0 })).toEqual({ cx: 100, cy: 100 });
	});
});

describe("buildCameraToggleCommand", () => {
	it("registers under CAMERA_TOGGLE_COMMAND_ID and toggles on do()", () => {
		const cam = new GameCamera();
		const bus = new CommandBus();
		const seen: string[] = [];
		bus.register(buildCameraToggleCommand({
			camera: cam,
			onToggle: (m) => seen.push(m),
		}));
		expect(bus.get(CAMERA_TOGGLE_COMMAND_ID)).toBeDefined();
		void bus.dispatch(CAMERA_TOGGLE_COMMAND_ID, undefined);
		expect(cam.getMode()).toBe("free-cam");
		void bus.dispatch(CAMERA_TOGGLE_COMMAND_ID, undefined);
		expect(cam.getMode()).toBe("follow");
		expect(seen).toEqual(["free-cam", "follow"]);
	});
});
