import { describe, it, expect } from "vitest";

import { CommandBus, type Command } from "@command-bus";
import { attachGamepad, readStick, type GamepadScheduler } from "./gamepad";

class FrameDriver implements GamepadScheduler {
	private q: Array<() => void> = [];
	requestFrame(cb: () => void): unknown {
		const handle = { cb };
		this.q.push(cb);
		return handle;
	}
	cancelFrame(_h: unknown): void { /* tests just stop scheduling */ }
	step(): void {
		const cbs = this.q;
		this.q = [];
		for (const cb of cbs) cb();
	}
}

function fakePad(opts: { axes?: number[]; buttons?: boolean[] } = {}): Gamepad {
	return {
		id: "fake", index: 0, connected: true, mapping: "standard",
		axes: opts.axes ?? [0, 0, 0, 0],
		buttons: (opts.buttons ?? [false, false, false]).map((p) => ({
			pressed: p, touched: p, value: p ? 1 : 0,
		})) as unknown as readonly GamepadButton[],
		timestamp: 0,
		vibrationActuator: null,
		hapticActuators: [],
	} as unknown as Gamepad;
}

describe("readStick", () => {
	it("zeros within deadzone", () => {
		const pad = fakePad({ axes: [0.1, 0.1, 0, 0] });
		expect(readStick(pad, 0, 0.18)).toEqual({ vx: 0, vy: 0 });
	});

	it("rescales past the deadzone so 0.18 -> 0 and 1.0 -> 1000", () => {
		const pad = fakePad({ axes: [1, 0, 0, 0] });
		expect(readStick(pad, 0, 0.18).vx).toBe(1000);
		expect(readStick(pad, 0, 0.18).vy).toBe(0);
	});

	it("reads stick 1 from axes 2/3", () => {
		const pad = fakePad({ axes: [0, 0, 1, 0] });
		expect(readStick(pad, 1, 0.18).vx).toBe(1000);
	});

	it("clamps to ±1000 if axes report values outside [-1,1]", () => {
		const pad = fakePad({ axes: [2, 0, 0, 0] });
		expect(readStick(pad, 0, 0.18).vx).toBe(1000);
	});
});

describe("attachGamepad", () => {
	it("invokes onAxes with current stick vector each frame", () => {
		const bus = new CommandBus();
		const driver = new FrameDriver();
		const samples: Array<{ vx: number; vy: number }> = [];
		let pad = fakePad({ axes: [0, 0, 0, 0] });
		attachGamepad(bus, (v) => samples.push(v), {
			scheduler: driver,
			gamepadSource: () => [pad],
		});
		driver.step();
		expect(samples[0]).toEqual({ vx: 0, vy: 0 });

		pad = fakePad({ axes: [1, 0, 0, 0] });
		driver.step();
		expect(samples[1]?.vx).toBe(1000);
	});

	it("dispatches button rising + falling edges through the bus", () => {
		const bus = new CommandBus();
		const press: Command<void> = { id: "test.btn", description: "btn",
			do:      () => { events.push("press"); },
			release: () => { events.push("release"); },
		};
		bus.register(press);
		bus.bindGamepad(0, "test.btn");
		const events: string[] = [];
		const driver = new FrameDriver();
		let pad = fakePad({ buttons: [false] });
		attachGamepad(bus, undefined, {
			scheduler: driver,
			gamepadSource: () => [pad],
		});
		driver.step(); // initial sample, no edge
		expect(events).toEqual([]);
		pad = fakePad({ buttons: [true] });
		driver.step();
		// Wait a microtask for the async dispatch.
		return Promise.resolve().then(() => Promise.resolve()).then(() => {
			expect(events).toContain("press");
			pad = fakePad({ buttons: [false] });
			driver.step();
			return Promise.resolve().then(() => Promise.resolve()).then(() => {
				expect(events).toContain("release");
			});
		});
	});

	it("returns an idempotent disposer", () => {
		const bus = new CommandBus();
		const driver = new FrameDriver();
		let polled = 0;
		const off = attachGamepad(bus, () => { polled++; }, {
			scheduler: driver,
			gamepadSource: () => [],
		});
		driver.step();
		expect(polled).toBe(1);
		off();
		driver.step();
		// After disposal, the callback bails on the `stopped` check
		// before incrementing.
		expect(polled).toBe(1);
	});

	it("first-pad-wins: when two pads are connected only one drives axes", () => {
		const bus = new CommandBus();
		const driver = new FrameDriver();
		const samples: Array<{ vx: number; vy: number }> = [];
		const pads = [
			fakePad({ axes: [1, 0, 0, 0] }),
			fakePad({ axes: [-1, 0, 0, 0] }),
		];
		attachGamepad(bus, (v) => samples.push(v), {
			scheduler: driver,
			gamepadSource: () => pads,
		});
		driver.step();
		expect(samples[0]?.vx).toBe(1000);
	});
});
