// Boxland — game/camera.ts
//
// Camera state for the player game module. Two modes:
//
//   * follow  -- camera tracks a target entity (default: the host).
//                Per-frame `cx/cy` come straight from the target.
//   * free-cam -- camera position is independent. Spectators (and
//                designers in Sandbox) get this mode; PLAN.md §4m,
//                §6h "spectator UI affordances".
//
// PLAN.md §6h "free-cam vs follow toggle". The Settings spectator
// preference (`spectator.freeCam`) seeds the initial mode; the
// in-game toggle command flips it at runtime.

/** Camera position in world sub-pixels. */
export interface CameraPos {
	cx: number;
	cy: number;
}

export type CameraMode = "follow" | "free-cam";

/** Pan speed in sub-pixels per millisecond at intent magnitude 1000.
 *  Picked to feel about 2x faster than walking so spectators can move
 *  around the map without falling behind a sprinting player. */
export const CAMERA_PAN_SUB_PER_MS = (120 * 256) / 1000;

export class GameCamera {
	private mode: CameraMode = "follow";
	private freeX = 0;
	private freeY = 0;

	/** Snap the free-cam position to whatever follow returned last; useful
	 *  on every mode-flip so toggling doesn't teleport the spectator. */
	syncFreeFrom(pos: CameraPos): void {
		this.freeX = pos.cx;
		this.freeY = pos.cy;
	}

	getMode(): CameraMode { return this.mode; }
	setMode(m: CameraMode): void { this.mode = m; }

	/** Toggle and return the new mode. Wires to the camera.toggle command. */
	toggleMode(): CameraMode {
		this.mode = this.mode === "follow" ? "free-cam" : "follow";
		return this.mode;
	}

	/** Snapshot the current camera position. `target` is the follow
	 *  target's position; ignored in free-cam mode. */
	snapshot(target: CameraPos): CameraPos {
		if (this.mode === "free-cam") return { cx: this.freeX, cy: this.freeY };
		return { cx: target.cx, cy: target.cy };
	}

	/** Apply intent (vx, vy in [-1000..1000]) to the free-cam position
	 *  for `dtMs` ms. No-op in follow mode (the follow target drives
	 *  position there). */
	pan(vx: number, vy: number, dtMs: number): void {
		if (this.mode !== "free-cam") return;
		if (vx === 0 && vy === 0) return;
		this.freeX += ((vx * CAMERA_PAN_SUB_PER_MS * dtMs) / 1000) | 0;
		this.freeY += ((vy * CAMERA_PAN_SUB_PER_MS * dtMs) / 1000) | 0;
	}

	/** Direct setter, used by Sandbox to teleport the camera. */
	setFreePos(cx: number, cy: number): void {
		this.freeX = cx | 0;
		this.freeY = cy | 0;
	}
}

/** Build a single Command<void> that toggles the mode + lets the host
 *  observe via onToggle. The settings page can rebind its hotkey. */
export interface ToggleCommandOpts {
	camera: GameCamera;
	/** Called after the toggle so the HUD can re-paint. */
	onToggle?: (mode: CameraMode) => void;
}

import type { Command } from "@command-bus";

export const CAMERA_TOGGLE_COMMAND_ID = "game.camera.toggle";

export function buildCameraToggleCommand(opts: ToggleCommandOpts): Command<void> {
	return {
		id: CAMERA_TOGGLE_COMMAND_ID,
		description: "Toggle camera (follow / free-cam)",
		category: "Game > Camera",
		whileTyping: false,
		do: () => {
			const next = opts.camera.toggleMode();
			opts.onToggle?.(next);
		},
	};
}
