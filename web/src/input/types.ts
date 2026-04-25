// Boxland — input/types.ts
//
// Public types for the input module. Every input source produces a
// `Command` dispatch on the shared CommandBus; this file declares the
// argument shapes for the non-trivial commands.

/** Argument shape for the click-to-move command. World coords are
 *  in sub-pixels (matches the rest of the engine's coordinate
 *  convention); pixel coords are the raw canvas-space values for
 *  callers that prefer to do their own world conversion. */
export interface ClickToMoveArgs {
	/** Canvas-space pixel coordinates relative to the game viewport. */
	pixelX: number;
	pixelY: number;
	/** World-space sub-pixels, computed by the mouse pump from the
	 *  current camera. May be undefined if the caller didn't provide
	 *  a CameraReader. */
	worldX?: number;
	worldY?: number;
	/** Mouse button: 0=left, 1=middle, 2=right. Most callers only act
	 *  on left-click; the bus binding controls dispatch. */
	button: number;
	/** Modifier keys at click time. */
	shift: boolean;
	ctrl: boolean;
	alt: boolean;
	meta: boolean;
}

/** Argument shape for the contextual interact command. Fired by mouse
 *  right-click or by gamepad face button. World coords mirror
 *  ClickToMoveArgs for handlers that want to ray-cast on click. */
export interface InteractArgs {
	pixelX: number;
	pixelY: number;
	worldX?: number;
	worldY?: number;
}

/** Continuous axis vector emitted by gamepad sticks (and any future
 *  touch joystick). Components are normalized to [-1000..1000] to
 *  match the network MovePayload range. */
export interface AxisVector {
	vx: number;
	vy: number;
}

/** A camera reader gives the mouse pump enough info to convert canvas
 *  pixels into world sub-pixels. Provided by the game loop's camera
 *  state; keep the shape minimal so test stubs are trivial. */
export interface CameraReader {
	/** World position of the canvas centre, in sub-pixels. */
	cx(): number;
	cy(): number;
	/** Sub-pixels per canvas pixel. The render layout owns this; the
	 *  input module never computes it. */
	subPerCanvasPx(): number;
	/** Canvas dimensions in pixels, used to map (canvasX - canvasW/2)
	 *  to (cx + delta * subPerCanvasPx). */
	canvasW(): number;
	canvasH(): number;
}

/** Stable command ids the input module dispatches against. Surfaces
 *  bind these to handlers; rebinding only changes the combo. */
export const INPUT_COMMAND_IDS = {
	clickToMove: "game.click-to-move",
	interactAt:  "game.interact-at",
} as const;
