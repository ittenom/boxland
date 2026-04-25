// Boxland — sandbox/entry-sandbox.ts
//
// Page boot for /design/sandbox/launch/{id}. Reuses web/src/game/ for
// the actual viewport + prediction + audio + input pumps (PLAN.md
// §131: "live game view (reuses web/src/game/)"); adds the designer
// HUD overlay + the Cmd-K palette wired to the sandbox-only opcodes.

import { BoxlandApp } from "@render";
import { attachInput } from "@input";
import { SoundEngine, type SoundCatalog, type AudioCameraReader } from "@audio";
import { applyAudio, loadLocal } from "@settings";
import {
	GameLoop, PlaceholderCatalog,
	type RendererLike, type HudLike, type CameraMode,
} from "@game";
import {
	NetClient, Mailbox, Realm, ClientKind,
	envelopeJoinMap, encodeClientMessage, Verb,
	type AuthParams, type CachedEntity, type ConnState,
} from "@net";
import { CommandBus, CommandPalette, type Command } from "@command-bus";

import type { Renderable } from "@render";
import * as flatbuffers from "flatbuffers";
import { DesignerCommandPayload } from "@proto/designer-command-payload.js";

// DesignerOpcode constants mirror schemas/input.fbs DesignerOpcode enum.
// Importing the proto value would also work, but keeping a small local
// table is clearer + avoids the circular impression of importing from
// @proto in business code.
const OP = {
	SpawnAny:           1,
	SetResource:        2,
	TakeControlEntity:  3,
	ReleaseControl:     4,
	Teleport:           5,
	FreezeTick:         6,
	StepTick:           7,
	Godmode:            8,
} as const;

interface SandboxBoot {
	mapId: number;
	mapName: string;
	mapWidth: number;
	mapHeight: number;
	wsURL: string;
	wsTicket: string;
	instanceID: string;
}

function readBoot(host: HTMLElement): SandboxBoot {
	const ds = host.dataset;
	const need = (k: string): string => {
		const v = ds[k];
		if (!v) throw new Error(`sandbox boot: missing data-bx-${k.replace(/[A-Z]/g, (c) => "-" + c.toLowerCase())}`);
		return v;
	};
	return {
		mapId:      Number(need("bxMapId")),
		mapName:    ds.bxMapName ?? "",
		mapWidth:   Number(need("bxMapWidth")),
		mapHeight:  Number(need("bxMapHeight")),
		wsURL:      need("bxWsUrl"),
		wsTicket:   need("bxAccessToken"),
		instanceID: need("bxInstanceId"),
	};
}

function adaptRenderer(app: BoxlandApp): RendererLike {
	return {
		updateFrame({ entities, hostId, hostX, hostY, cameraX, cameraY }) {
			const renderables: Renderable[] = entities.map((e: CachedEntity) => {
				const isHost = e.id === hostId;
				return {
					id: Number(e.id),
					asset_id: e.typeId,
					anim_id: e.animId,
					anim_frame: e.animFrame,
					x: isHost ? hostX : e.x,
					y: isHost ? hostY : e.y,
					variant_id: e.variantId,
					tint: e.tint,
					layer: 0,
					nameplate: e.nameplate,
					hpPct: e.hpPct,
				};
			});
			void app.update(renderables, { cx: cameraX, cy: cameraY });
		},
	};
}

function buildHud(): HudLike {
	const badge  = document.querySelector("[data-bx-state-badge]") as HTMLElement | null;
	const tickEl = document.querySelector("[data-bx-tick]") as HTMLElement | null;
	return {
		setState(s: ConnState) {
			if (badge) {
				badge.textContent = s;
				badge.dataset.bxState = s;
			}
		},
		setTick(tick) {
			if (tickEl) tickEl.textContent = `tick ${tick.toString()}`;
		},
		setCameraMode(mode: CameraMode) {
			// Sandbox can show camera mode in the inspector; v1 wires
			// nothing here (designers always free-cam in practice).
			void mode;
		},
	};
}

/**
 * Build a DesignerCommand envelope. The schema's
 * DesignerCommandPayload carries an opcode + an inner data byte vector;
 * for the runtime opcodes (Freeze/Step/Spawn) we pack the instance id
 * as ASCII -- see ws/sandbox_ops.go's targetInstanceID pattern.
 */
function sendDesignerCommand(net: NetClient, opcode: number, instanceID: string): void {
	const data = new TextEncoder().encode(instanceID);
	const b = new flatbuffers.Builder(64 + data.length);
	const dataOff = DesignerCommandPayload.createDataVector(b, data);
	DesignerCommandPayload.startDesignerCommandPayload(b);
	DesignerCommandPayload.addOpcode(b, opcode);
	DesignerCommandPayload.addData(b, dataOff);
	const root = DesignerCommandPayload.endDesignerCommandPayload(b);
	b.finish(root);
	const inner = b.asUint8Array();
	net.sendRaw(encodeClientMessage(Verb.DesignerCommand, inner));
}

export async function bootSandbox(host: HTMLElement = document.getElementById("bx-game-host") as HTMLElement): Promise<GameLoop | null> {
	if (!host) return null;
	const boot = readBoot(host);

	const app = await BoxlandApp.create({
		host,
		worldViewW: 480,
		worldViewH: 320,
		catalog: new PlaceholderCatalog(),
	});

	let cachedLoop: GameLoop | null = null;
	const audioCam: AudioCameraReader = {
		cx: () => {
			const l = cachedLoop;
			if (!l) return 0;
			const t = { cx: l.snapshot().hostX, cy: l.snapshot().hostY };
			return l.camera.snapshot(t).cx;
		},
		cy: () => {
			const l = cachedLoop;
			if (!l) return 0;
			const t = { cx: l.snapshot().hostX, cy: l.snapshot().hostY };
			return l.camera.snapshot(t).cy;
		},
	};
	const soundCatalog: SoundCatalog = { urlFor: () => undefined };
	const audio = new SoundEngine({ catalog: soundCatalog, camera: audioCam });
	const localSettings = loadLocal("designer");
	applyAudio(localSettings, audio);

	// Custom NetClient so the handshake uses Realm.Designer + the
	// WS ticket (instead of the player JWT factory entry-game.ts uses).
	const mailbox = new Mailbox();
	const net = new NetClient(boot.wsURL, {
		mailbox,
		auth: (): AuthParams => ({
			realm: Realm.Designer,
			token: boot.wsTicket,
			clientKind: ClientKind.Web,
			clientVersion: "sandbox-test",
		}),
	});
	// On open, JoinMap with the sandbox: instance hint so the
	// gateway routes to the per-designer sandbox runtime instance.
	net.onState((s) => {
		if (s === "open") {
			net.sendRaw(envelopeJoinMap({
				mapId: boot.mapId,
				instanceHint: boot.instanceID,
			}));
		}
	});

	const loop = new GameLoop({
		config: {
			mapId:       boot.mapId,
			mapName:     boot.mapName,
			mapWidth:    boot.mapWidth,
			mapHeight:   boot.mapHeight,
			wsURL:       boot.wsURL,
			accessToken: boot.wsTicket,
		},
		renderer: adaptRenderer(app),
		hud:      buildHud(),
		audio,
		mailbox,
		netClient: net,
		// Sandbox is "designer free-cam" by default; the camera is
		// independent of any host entity.
		spectator:        true,
		initialCameraMode: "free-cam",
	});
	cachedLoop = loop;
	host.addEventListener("pointerdown", () => audio.resume(), { once: true });

	// Expose for the Settings page rebinder.
	(globalThis as unknown as { boxlandBus?: CommandBus; boxlandAudio?: SoundEngine }).boxlandBus = loop.bus;
	(globalThis as unknown as { boxlandBus?: CommandBus; boxlandAudio?: SoundEngine }).boxlandAudio = audio;

	loop.start();

	// Input pumps -- keyboard + mouse + gamepad onto the same bus.
	attachInput(loop.bus, { mouseHost: host });

	// ---- Designer HUD wiring -------------------------------------

	const hudButtons = document.querySelectorAll<HTMLButtonElement>("[data-bx-sandbox-op]");
	hudButtons.forEach((btn) => {
		btn.addEventListener("click", () => {
			const op = btn.dataset.bxSandboxOp;
			if (!op) return;
			runOp(op, net, boot.instanceID);
		});
	});

	// Designer Cmd-K palette: register each sandbox op as a Command
	// so the existing CommandPalette UI lists them. Combos:
	//   F = freeze, . = step, G = godmode, ~ = spawn (PLAN docs/hotkeys.md)
	registerSandboxCommands(loop.bus, net, boot.instanceID);
	const palette = new CommandPalette(loop.bus);
	loop.bus.bindHotkey("Mod+K", "palette.open");
	loop.bus.register({
		id: "palette.open",
		description: "Open command palette",
		category: "Designer",
		do: () => { palette.open(); },
	});

	return loop;
}

function registerSandboxCommands(bus: CommandBus, net: NetClient, instanceID: string): void {
	const cmd = (id: string, desc: string, opcode: number): Command<void> => ({
		id, description: desc, category: "Sandbox",
		do: () => { sendDesignerCommand(net, opcode, instanceID); },
	});
	const list: Array<{ c: Command<void>; combo?: string }> = [
		{ c: cmd("sandbox.freeze",    "Freeze tick",       OP.FreezeTick), combo: "F" },
		{ c: cmd("sandbox.step",      "Step one tick",     OP.StepTick),   combo: "." },
		{ c: cmd("sandbox.godmode",   "Toggle godmode",    OP.Godmode),    combo: "G" },
		{ c: cmd("sandbox.spawn",     "Spawn (palette…)",  OP.SpawnAny),   combo: "Shift+S" },
		{ c: cmd("sandbox.teleport",  "Teleport host",     OP.Teleport) },
		{ c: cmd("sandbox.set-resource", "Set resource",   OP.SetResource) },
		{ c: cmd("sandbox.take-control", "Take control",   OP.TakeControlEntity) },
		{ c: cmd("sandbox.release-control", "Release control", OP.ReleaseControl) },
	];
	for (const e of list) {
		bus.register(e.c);
		if (e.combo) bus.bindHotkey(e.combo, e.c.id);
	}
}

function runOp(op: string, net: NetClient, instanceID: string): void {
	switch (op) {
		case "freeze":   return sendDesignerCommand(net, OP.FreezeTick, instanceID);
		case "step":     return sendDesignerCommand(net, OP.StepTick,   instanceID);
		case "godmode":  return sendDesignerCommand(net, OP.Godmode,    instanceID);
		case "spawn":    return sendDesignerCommand(net, OP.SpawnAny,   instanceID);
	}
}

// Auto-run when loaded on the sandbox surface.
if (typeof document !== "undefined" && document.body?.dataset.surface === "sandbox-game") {
	void bootSandbox();
}
