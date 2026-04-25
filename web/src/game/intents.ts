// Boxland — game/intents.ts
//
// Movement intents. Each Command toggles one axis bit on the host
// LocalState; the orchestrator reads those bits and emits a single
// MovePayload per server tick. PLAN.md §6h: every input is a Command
// on the shared bus, rebindable via Settings.
//
// We expose a small `MovementIntent` controller the orchestrator owns;
// the commands are bound against THAT controller's setters so the
// CommandBus stays oblivious to game state.

import { CommandBus, type Command } from "@command-bus";

/** Press-and-hold intent: each axis tracks {pressedNeg, pressedPos} so
 *  releasing one direction while the other is still held leaves the
 *  axis at the held direction (mirrors how WASD games normally feel). */
export class MovementIntent {
	private up = false;
	private down = false;
	private left = false;
	private right = false;

	/** Snapshot intent vector, scaled to ±1000 like MovePayload expects. */
	vector(): { vx: number; vy: number } {
		const vx = (this.right ? 1000 : 0) + (this.left ? -1000 : 0);
		const vy = (this.down ? 1000 : 0) + (this.up ? -1000 : 0);
		return { vx, vy };
	}

	/** Drop every held key. Called on blur or HUD-modal-open to avoid
	 *  the "stuck moving forward while a dialog is up" trap. */
	clear(): void {
		this.up = this.down = this.left = this.right = false;
	}

	/** Setters; commands wire to these. */
	setUp(v: boolean): void    { this.up = v; }
	setDown(v: boolean): void  { this.down = v; }
	setLeft(v: boolean): void  { this.left = v; }
	setRight(v: boolean): void { this.right = v; }
}

/** Per-axis press + release Commands. The bus only fires "do" on
 *  press; release is a sibling command bound to keyup elsewhere
 *  (the input module in task #117 will own that). For v1 the loop
 *  drives release via the orchestrator's keyup listener. */
export function buildMovementCommands(intent: MovementIntent): Command<void>[] {
	const press = (id: string, description: string, setter: (v: boolean) => void): Command<void> => ({
		id,
		description,
		category: "Game > Move",
		whileTyping: false,
		do: () => { setter(true); },
	});
	return [
		press("game.move.up.press",    "Move up (hold)",    (v) => intent.setUp(v)),
		press("game.move.down.press",  "Move down (hold)",  (v) => intent.setDown(v)),
		press("game.move.left.press",  "Move left (hold)",  (v) => intent.setLeft(v)),
		press("game.move.right.press", "Move right (hold)", (v) => intent.setRight(v)),
	];
}

/** Convenience for the orchestrator: register the four movement
 *  commands + bind the default WASD + arrow-key combos. Returns the
 *  intent controller so the loop can read it each tick. */
export function installMovementBindings(bus: CommandBus): MovementIntent {
	const intent = new MovementIntent();
	for (const cmd of buildMovementCommands(intent)) {
		bus.register(cmd);
	}
	bus.bindHotkey("ArrowUp",    "game.move.up.press");
	bus.bindHotkey("ArrowDown",  "game.move.down.press");
	bus.bindHotkey("ArrowLeft",  "game.move.left.press");
	bus.bindHotkey("ArrowRight", "game.move.right.press");
	bus.bindHotkey("W", "game.move.up.press");
	bus.bindHotkey("S", "game.move.down.press");
	bus.bindHotkey("A", "game.move.left.press");
	bus.bindHotkey("D", "game.move.right.press");
	return intent;
}
