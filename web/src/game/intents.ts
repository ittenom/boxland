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
 *  axis at the held direction (mirrors how WASD games normally feel).
 *
 *  Gamepad sticks contribute through setStickVector; the resulting
 *  vector is the union of digital (key/button) input and analog
 *  (stick) input -- whichever has higher magnitude on each axis wins.
 */
export class MovementIntent {
	private up = false;
	private down = false;
	private left = false;
	private right = false;
	private stickVx = 0;
	private stickVy = 0;

	/** Snapshot intent vector, scaled to ±1000 like MovePayload expects.
	 *  When a stick is active its analog magnitude wins on each axis;
	 *  digital keys override only when no stick deflection is present.
	 *  This means a player hammering WASD while bumping the stick
	 *  doesn't get veto'd by the stick's null state. */
	vector(): { vx: number; vy: number } {
		const keyVx = (this.right ? 1000 : 0) + (this.left ? -1000 : 0);
		const keyVy = (this.down ? 1000 : 0) + (this.up ? -1000 : 0);
		const vx = Math.abs(this.stickVx) > Math.abs(keyVx) ? this.stickVx : keyVx;
		const vy = Math.abs(this.stickVy) > Math.abs(keyVy) ? this.stickVy : keyVy;
		return { vx, vy };
	}

	/** Drop every held key + reset stick. Called on blur or
	 *  HUD-modal-open to avoid the "stuck moving forward while a
	 *  dialog is up" trap. */
	clear(): void {
		this.up = this.down = this.left = this.right = false;
		this.stickVx = this.stickVy = 0;
	}

	/** Setters; commands wire to these. */
	setUp(v: boolean): void    { this.up = v; }
	setDown(v: boolean): void  { this.down = v; }
	setLeft(v: boolean): void  { this.left = v; }
	setRight(v: boolean): void { this.right = v; }

	/** Update the analog stick vector. Components are clamped here so
	 *  upstream sources (gamepad pump, future touch joystick) can
	 *  pass un-normalized values without re-clamping. */
	setStickVector(vx: number, vy: number): void {
		this.stickVx = clamp(vx, -1000, 1000) | 0;
		this.stickVy = clamp(vy, -1000, 1000) | 0;
	}
}

function clamp(n: number, lo: number, hi: number): number {
	if (n < lo) return lo;
	if (n > hi) return hi;
	return n;
}

/** Per-axis hold Commands. Each command exposes both `do` (called on
 *  keydown / button-down) and `release` (keyup / button-up) so a single
 *  binding combo drives the full press-and-hold cycle. The Settings
 *  rebinder rebinds the combo; press + release stay paired. */
export function buildMovementCommands(intent: MovementIntent): Command<void>[] {
	const hold = (
		id: string,
		description: string,
		setter: (v: boolean) => void,
	): Command<void> => ({
		id,
		description,
		category: "Game > Move",
		whileTyping: false,
		do:      () => { setter(true); },
		release: () => { setter(false); },
	});
	return [
		hold("game.move.up",    "Move up (hold)",    (v) => intent.setUp(v)),
		hold("game.move.down",  "Move down (hold)",  (v) => intent.setDown(v)),
		hold("game.move.left",  "Move left (hold)",  (v) => intent.setLeft(v)),
		hold("game.move.right", "Move right (hold)", (v) => intent.setRight(v)),
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
	bus.bindHotkey("ArrowUp",    "game.move.up");
	bus.bindHotkey("ArrowDown",  "game.move.down");
	bus.bindHotkey("ArrowLeft",  "game.move.left");
	bus.bindHotkey("ArrowRight", "game.move.right");
	bus.bindHotkey("W", "game.move.up");
	bus.bindHotkey("S", "game.move.down");
	bus.bindHotkey("A", "game.move.left");
	bus.bindHotkey("D", "game.move.right");
	return intent;
}
