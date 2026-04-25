// @vitest-environment jsdom
import { describe, it, expect } from "vitest";

import { CommandBus } from "@command-bus";
import { MovementIntent, installMovementBindings, buildMovementCommands } from "./intents";

describe("MovementIntent", () => {
	it("encodes diagonal as the sum of two axes", () => {
		const m = new MovementIntent();
		m.setUp(true);
		m.setRight(true);
		expect(m.vector()).toEqual({ vx: 1000, vy: -1000 });
	});

	it("opposing keys cancel on the axis", () => {
		const m = new MovementIntent();
		m.setLeft(true);
		m.setRight(true);
		expect(m.vector()).toEqual({ vx: 0, vy: 0 });
	});

	it("clear() drops every held key", () => {
		const m = new MovementIntent();
		m.setUp(true); m.setRight(true);
		m.clear();
		expect(m.vector()).toEqual({ vx: 0, vy: 0 });
	});
});

describe("buildMovementCommands", () => {
	it("returns four hold commands keyed under game.move.*", () => {
		const m = new MovementIntent();
		const cmds = buildMovementCommands(m);
		expect(cmds.map((c) => c.id)).toEqual([
			"game.move.up",
			"game.move.down",
			"game.move.left",
			"game.move.right",
		]);
		// All four are non-undoable holds with paired release.
		for (const c of cmds) {
			expect(c.undo).toBeUndefined();
			expect(c.release).toBeDefined();
			expect(c.category).toBe("Game > Move");
		}
	});

	it("press fires the intent setter", () => {
		const m = new MovementIntent();
		const [up] = buildMovementCommands(m);
		up!.do();
		expect(m.vector().vy).toBe(-1000);
	});

	it("release clears the intent setter", () => {
		const m = new MovementIntent();
		const [up] = buildMovementCommands(m);
		up!.do();
		up!.release!();
		expect(m.vector().vy).toBe(0);
	});
});

describe("installMovementBindings", () => {
	it("registers commands + binds default WASD + arrow combos", () => {
		const bus = new CommandBus();
		const intent = installMovementBindings(bus);
		// Each direction has at least two combos (Arrow + WASD letter).
		expect(bus.get("game.move.up")).toBeDefined();
		expect(bus.get("game.move.left")).toBeDefined();
		expect(bus.hotkeyFor("game.move.up")).toBeDefined();
		// Sanity: simulating an ArrowRight press sets right.
		const ev = new KeyboardEvent("keydown", { key: "ArrowRight" });
		void bus.handleKeyEvent(ev, false);
		expect(intent.vector().vx).toBe(1000);
		// And the matching keyup clears it.
		const ev2 = new KeyboardEvent("keyup", { key: "ArrowRight" });
		void bus.handleKeyRelease(ev2, false);
		expect(intent.vector().vx).toBe(0);
	});
});
