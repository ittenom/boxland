// @vitest-environment jsdom
import { describe, it, expect } from "vitest";

import { CommandBus, type Command } from "@command-bus";
import { attachMouse, canvasCoords, canvasToWorld, INPUT_COMMAND_IDS, type CameraReader, type ClickToMoveArgs, type InteractArgs } from "./index";

function makeHost(width = 200, height = 100): HTMLElement {
	const el = document.createElement("div");
	// jsdom doesn't compute layout so we override getBoundingClientRect.
	el.getBoundingClientRect = () => ({
		x: 0, y: 0, top: 0, left: 0, right: width, bottom: height, width, height,
		toJSON: () => ({}),
	});
	document.body.appendChild(el);
	return el;
}

function makeCmdBus(): { bus: CommandBus; clicks: ClickToMoveArgs[]; interacts: InteractArgs[] } {
	const bus = new CommandBus();
	const clicks: ClickToMoveArgs[] = [];
	const interacts: InteractArgs[] = [];
	const click: Command<ClickToMoveArgs> = {
		id: INPUT_COMMAND_IDS.clickToMove,
		description: "click",
		do: (a) => { clicks.push(a); },
	};
	const inter: Command<InteractArgs> = {
		id: INPUT_COMMAND_IDS.interactAt,
		description: "interact",
		do: (a) => { interacts.push(a); },
	};
	bus.register(click as unknown as Command<unknown>);
	bus.register(inter as unknown as Command<unknown>);
	return { bus, clicks, interacts };
}

function ptr(host: HTMLElement, opts: { clientX: number; clientY: number; button: number; type?: string }): PointerEvent {
	const e = new MouseEvent(opts.type ?? "pointerdown", {
		clientX: opts.clientX, clientY: opts.clientY, button: opts.button, bubbles: true,
	}) as unknown as PointerEvent;
	Object.defineProperty(e, "pointerType", { value: "mouse" });
	host.dispatchEvent(e);
	return e;
}

describe("canvasCoords", () => {
	it("subtracts the host's bounding-rect origin", () => {
		const host = makeHost();
		const got = canvasCoords(host, { clientX: 50, clientY: 25 });
		expect(got).toEqual({ x: 50, y: 25 });
	});
});

describe("canvasToWorld", () => {
	it("maps centre pixel to camera centre in world coords", () => {
		const cam: CameraReader = {
			cx: () => 1000, cy: () => 2000,
			subPerCanvasPx: () => 1,
			canvasW: () => 200, canvasH: () => 100,
		};
		const w = canvasToWorld(cam, makeHost(), 100, 50);
		expect(w).toEqual({ worldX: 1000, worldY: 2000 });
	});

	it("scales by subPerCanvasPx", () => {
		const cam: CameraReader = {
			cx: () => 0, cy: () => 0,
			subPerCanvasPx: () => 4,
			canvasW: () => 200, canvasH: () => 100,
		};
		// 50 px right of centre -> 50 * 4 = 200 sub-px right.
		const w = canvasToWorld(cam, makeHost(), 150, 50);
		expect(w).toEqual({ worldX: 200, worldY: 0 });
	});
});

describe("attachMouse", () => {
	it("dispatches click-to-move on left button", async () => {
		const { bus, clicks } = makeCmdBus();
		const host = makeHost();
		attachMouse(bus, host);
		ptr(host, { clientX: 30, clientY: 40, button: 0 });
		await Promise.resolve();
		expect(clicks).toHaveLength(1);
		expect(clicks[0]?.pixelX).toBe(30);
		expect(clicks[0]?.button).toBe(0);
	});

	it("dispatches interact-at on right button", async () => {
		const { bus, interacts } = makeCmdBus();
		const host = makeHost();
		attachMouse(bus, host);
		ptr(host, { clientX: 70, clientY: 80, button: 2 });
		await Promise.resolve();
		expect(interacts).toHaveLength(1);
		expect(interacts[0]?.pixelX).toBe(70);
	});

	it("computes world coords when a camera is supplied", async () => {
		const { bus, clicks } = makeCmdBus();
		const host = makeHost();
		const cam: CameraReader = {
			cx: () => 5000, cy: () => 5000,
			subPerCanvasPx: () => 2,
			canvasW: () => 200, canvasH: () => 100,
		};
		attachMouse(bus, host, { camera: cam });
		ptr(host, { clientX: 100, clientY: 50, button: 0 }); // dead centre
		await Promise.resolve();
		expect(clicks[0]?.worldX).toBe(5000);
		expect(clicks[0]?.worldY).toBe(5000);
	});

	it("middle button is ignored", async () => {
		const { bus, clicks, interacts } = makeCmdBus();
		const host = makeHost();
		attachMouse(bus, host);
		ptr(host, { clientX: 10, clientY: 10, button: 1 });
		await Promise.resolve();
		expect(clicks).toHaveLength(0);
		expect(interacts).toHaveLength(0);
	});

	it("disposer detaches the listener", async () => {
		const { bus, clicks } = makeCmdBus();
		const host = makeHost();
		const off = attachMouse(bus, host);
		off();
		ptr(host, { clientX: 1, clientY: 1, button: 0 });
		await Promise.resolve();
		expect(clicks).toHaveLength(0);
	});

	it("preventDefault'd contextmenu when preventContextMenu is true", () => {
		const { bus } = makeCmdBus();
		const host = makeHost();
		attachMouse(bus, host);
		const ev = new MouseEvent("contextmenu", { bubbles: true, cancelable: true });
		host.dispatchEvent(ev);
		expect(ev.defaultPrevented).toBe(true);
	});
});
