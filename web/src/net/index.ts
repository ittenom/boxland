// Boxland — net/ barrel.
//
// Surface for game client + mapmaker + sandbox: a single import for the
// WS client + mailbox + codec helpers + shared types. Per-surface code
// should import from "@net" rather than reaching into individual files.

export * from "./types";
export * from "./codec";
export { Mailbox } from "./mailbox";
export type {
	CachedEntity,
	CachedTile,
	CachedLighting,
	CachedAudio,
} from "./mailbox";
export { NetClient } from "./client";
export type {
	NetClientOptions,
	WSLike,
	WSConstructor,
	Scheduler,
} from "./client";
