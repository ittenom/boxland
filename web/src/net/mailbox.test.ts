import { describe, it, expect } from "vitest";
import * as flatbuffers from "flatbuffers";

import { Mailbox } from "./mailbox";
import { decodeDiff } from "./codec";
import { buildTestDiff } from "./codec.test";

import { Diff } from "@proto/diff.js";
import { ChunkVersion } from "@proto/chunk-version.js";
import { ProtocolVersion } from "@proto/protocol-version.js";
import { EntityState } from "@proto/entity-state.js";
import { Tile } from "@proto/tile.js";
import { LightingCell } from "@proto/lighting-cell.js";
import { AudioEvent } from "@proto/audio-event.js";

/** Like buildTestDiff but exposes more knobs for the mailbox suite. */
function buildRichDiff(opts: {
	mapId: number;
	tick: bigint;
	added?: Array<{ id: bigint; x: number; y: number; nameplate?: string }>;
	moved?: Array<{ id: bigint; x: number; y: number }>;
	removed?: bigint[];
	tiles?: Array<{ layerId: number; gx: number; gy: number; assetId: number }>;
	lighting?: Array<{ gx: number; gy: number; color: number; intensity: number }>;
	audio?: Array<{ soundId: number; x?: number; y?: number; hasPosition?: boolean }>;
	chunks?: Array<{ chunkId: bigint; version: bigint }>;
}): Diff {
	const b = new flatbuffers.Builder(512);

	ProtocolVersion.startProtocolVersion(b);
	ProtocolVersion.addMajor(b, 1);
	ProtocolVersion.addMinor(b, 0);
	const pvOff = ProtocolVersion.endProtocolVersion(b);

	function entOff(e: { id: bigint; x: number; y: number; nameplate?: string }): number {
		const nameOff = b.createString(e.nameplate ?? "");
		EntityState.startEntityState(b);
		EntityState.addId(b, e.id);
		EntityState.addX(b, e.x);
		EntityState.addY(b, e.y);
		EntityState.addNameplate(b, nameOff);
		return EntityState.endEntityState(b);
	}

	const addedOffs = (opts.added ?? []).map(entOff);
	const movedOffs = (opts.moved ?? []).map((e) => entOff({ ...e }));

	const tileOffs = (opts.tiles ?? []).map((t) => {
		Tile.startTile(b);
		Tile.addLayerId(b, t.layerId);
		Tile.addGx(b, t.gx);
		Tile.addGy(b, t.gy);
		Tile.addAssetId(b, t.assetId);
		return Tile.endTile(b);
	});

	const lightOffs = (opts.lighting ?? []).map((l) => {
		LightingCell.startLightingCell(b);
		LightingCell.addGx(b, l.gx);
		LightingCell.addGy(b, l.gy);
		LightingCell.addColor(b, l.color);
		LightingCell.addIntensity(b, l.intensity);
		return LightingCell.endLightingCell(b);
	});

	const audioOffs = (opts.audio ?? []).map((a) => {
		AudioEvent.startAudioEvent(b);
		AudioEvent.addSoundId(b, a.soundId);
		AudioEvent.addHasPosition(b, a.hasPosition ?? false);
		AudioEvent.addX(b, a.x ?? 0);
		AudioEvent.addY(b, a.y ?? 0);
		return AudioEvent.endAudioEvent(b);
	});

	const chunkOffs = (opts.chunks ?? []).map((c) => {
		ChunkVersion.startChunkVersion(b);
		ChunkVersion.addChunkId(b, c.chunkId);
		ChunkVersion.addVersion(b, c.version);
		return ChunkVersion.endChunkVersion(b);
	});

	const addedVec = addedOffs.length ? Diff.createAddedVector(b, addedOffs) : 0;
	const movedVec = movedOffs.length ? Diff.createMovedVector(b, movedOffs) : 0;
	const removedVec = (opts.removed?.length)
		? Diff.createRemovedVector(b, opts.removed)
		: 0;
	const tileVec = tileOffs.length ? Diff.createTileChangesVector(b, tileOffs) : 0;
	const lightVec = lightOffs.length ? Diff.createLightingChangesVector(b, lightOffs) : 0;
	const audioVec = audioOffs.length ? Diff.createAudioVector(b, audioOffs) : 0;
	const chunksVec = chunkOffs.length ? Diff.createChunksVector(b, chunkOffs) : 0;

	Diff.startDiff(b);
	Diff.addProtocolVersion(b, pvOff);
	Diff.addMapId(b, opts.mapId);
	Diff.addTick(b, opts.tick);
	if (addedVec)   Diff.addAdded(b, addedVec);
	if (movedVec)   Diff.addMoved(b, movedVec);
	if (removedVec) Diff.addRemoved(b, removedVec);
	if (tileVec)    Diff.addTileChanges(b, tileVec);
	if (lightVec)   Diff.addLightingChanges(b, lightVec);
	if (audioVec)   Diff.addAudio(b, audioVec);
	if (chunksVec)  Diff.addChunks(b, chunksVec);
	const root = Diff.endDiff(b);
	b.finish(root);

	return decodeDiff(b.asUint8Array())!;
}

describe("Mailbox.applyDiff entity cache", () => {
	it("inserts added entities + reports their ids", () => {
		const m = new Mailbox();
		const d = buildRichDiff({
			mapId: 1, tick: 5n,
			added: [
				{ id: 10n, x: 100, y: 200, nameplate: "Alice" },
				{ id: 11n, x: 300, y: 400 },
			],
		});
		const applied = m.applyDiff(d);
		expect(applied.addedIds).toEqual([10n, 11n]);
		expect(m.entityCount()).toBe(2);
		expect(m.getEntity(10n)?.x).toBe(100);
		expect(m.getEntity(10n)?.nameplate).toBe("Alice");
		expect(m.getEntity(11n)?.y).toBe(400);
	});

	it("moved entities overwrite the cache + report moved ids", () => {
		const m = new Mailbox();
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 1n,
			added: [{ id: 7n, x: 0, y: 0 }],
		}));
		const applied = m.applyDiff(buildRichDiff({
			mapId: 1, tick: 2n,
			moved: [{ id: 7n, x: 50, y: 80 }],
		}));
		expect(applied.movedIds).toEqual([7n]);
		expect(m.getEntity(7n)?.x).toBe(50);
		expect(m.getEntity(7n)?.y).toBe(80);
	});

	it("removed entities drop from cache + report removed ids", () => {
		const m = new Mailbox();
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 1n,
			added: [{ id: 7n, x: 0, y: 0 }, { id: 8n, x: 0, y: 0 }],
		}));
		const applied = m.applyDiff(buildRichDiff({
			mapId: 1, tick: 2n,
			removed: [7n],
		}));
		expect(applied.removedIds).toEqual([7n]);
		expect(m.getEntity(7n)).toBeUndefined();
		expect(m.getEntity(8n)).toBeDefined();
		expect(m.entityCount()).toBe(1);
	});
});

describe("Mailbox.applyDiff tile + lighting + audio caches", () => {
	it("tile changes overwrite by (layer,gx,gy)", () => {
		const m = new Mailbox();
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 1n,
			tiles: [
				{ layerId: 1, gx: 2, gy: 3, assetId: 10 },
				{ layerId: 1, gx: 5, gy: 5, assetId: 20 },
			],
		}));
		expect(m.tileCount()).toBe(2);
		expect(m.getTile(1, 2, 3)?.assetId).toBe(10);

		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 2n,
			tiles: [{ layerId: 1, gx: 2, gy: 3, assetId: 99 }],
		}));
		expect(m.getTile(1, 2, 3)?.assetId).toBe(99);
		expect(m.tileCount()).toBe(2); // (5,5) still there
	});

	it("lighting intensity 0 clears the cell (matches server erase semantics)", () => {
		const m = new Mailbox();
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 1n,
			lighting: [{ gx: 4, gy: 4, color: 0xffffffff, intensity: 200 }],
		}));
		expect(m.getLighting(4, 4)?.intensity).toBe(200);
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 2n,
			lighting: [{ gx: 4, gy: 4, color: 0, intensity: 0 }],
		}));
		expect(m.getLighting(4, 4)).toBeUndefined();
	});

	it("audio queues and drains exactly once", () => {
		const m = new Mailbox();
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 1n,
			audio: [
				{ soundId: 100 },
				{ soundId: 200, x: 50, y: 60, hasPosition: true },
			],
		}));
		const drained = m.drainAudio();
		expect(drained).toHaveLength(2);
		expect(drained[0]?.soundId).toBe(100);
		expect(drained[1]?.hasPosition).toBe(true);
		expect(m.drainAudio()).toHaveLength(0);
	});
});

describe("Mailbox per-chunk version vector", () => {
	it("advances acked iff diff version > prior", () => {
		const m = new Mailbox();
		const a = m.applyDiff(buildRichDiff({
			mapId: 1, tick: 1n,
			chunks: [
				{ chunkId: 100n, version: 5n },
				{ chunkId: 101n, version: 1n },
			],
		}));
		expect(a.advancedChunks).toEqual([100n, 101n]);
		expect(m.getAckedVersion(100n)).toBe(5n);
		expect(m.getAckedVersion(101n)).toBe(1n);

		// Stale frame: chunk 100 version 3 < acked 5 -> dropped silently.
		const b = m.applyDiff(buildRichDiff({
			mapId: 1, tick: 2n,
			chunks: [
				{ chunkId: 100n, version: 3n },
				{ chunkId: 101n, version: 9n },
			],
		}));
		expect(b.advancedChunks).toEqual([101n]);
		expect(m.getAckedVersion(100n)).toBe(5n);  // unchanged
		expect(m.getAckedVersion(101n)).toBe(9n);  // advanced
	});

	it("absent chunk reads 0 (forces full chunk on first send)", () => {
		const m = new Mailbox();
		expect(m.getAckedVersion(999n)).toBe(0n);
	});

	it("snapshotAcks lists every acked chunk", () => {
		const m = new Mailbox();
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 1n,
			chunks: [
				{ chunkId: 1n, version: 1n },
				{ chunkId: 2n, version: 2n },
			],
		}));
		const snap = m.snapshotAcks();
		expect(snap).toHaveLength(2);
		expect(snap).toContainEqual({ chunkId: 1n, version: 1n });
		expect(snap).toContainEqual({ chunkId: 2n, version: 2n });
	});

	it("lastAppliedTick advances monotonically", () => {
		const m = new Mailbox();
		expect(m.getLastAppliedTick()).toBe(0n);
		m.applyDiff(buildRichDiff({ mapId: 1, tick: 5n }));
		expect(m.getLastAppliedTick()).toBe(5n);
		// Stale tick must not regress.
		m.applyDiff(buildRichDiff({ mapId: 1, tick: 2n }));
		expect(m.getLastAppliedTick()).toBe(5n);
		m.applyDiff(buildRichDiff({ mapId: 1, tick: 7n }));
		expect(m.getLastAppliedTick()).toBe(7n);
	});
});

describe("Mailbox.reset", () => {
	it("clears every cache + acked vector + lastAppliedTick", () => {
		const m = new Mailbox();
		m.applyDiff(buildRichDiff({
			mapId: 1, tick: 9n,
			added: [{ id: 1n, x: 10, y: 20 }],
			tiles: [{ layerId: 0, gx: 0, gy: 0, assetId: 1 }],
			lighting: [{ gx: 0, gy: 0, color: 0xff, intensity: 50 }],
			chunks: [{ chunkId: 1n, version: 4n }],
		}));
		m.reset();
		expect(m.entityCount()).toBe(0);
		expect(m.tileCount()).toBe(0);
		expect(m.getLighting(0, 0)).toBeUndefined();
		expect(m.getAckedVersion(1n)).toBe(0n);
		expect(m.getLastAppliedTick()).toBe(0n);
	});
});

describe("Mailbox.onDiff listener", () => {
	it("fires after applying", () => {
		const m = new Mailbox();
		const seen: bigint[] = [];
		const stop = m.onDiff((d) => seen.push(d.tick));
		m.applyDiff(buildRichDiff({ mapId: 1, tick: 1n }));
		m.applyDiff(buildRichDiff({ mapId: 1, tick: 2n }));
		stop();
		m.applyDiff(buildRichDiff({ mapId: 1, tick: 3n }));
		expect(seen).toEqual([1n, 2n]);
	});

	it("isolates throwing listeners", () => {
		const m = new Mailbox();
		m.onDiff(() => { throw new Error("boom"); });
		const seen: bigint[] = [];
		m.onDiff((d) => seen.push(d.tick));
		// Should not throw.
		m.applyDiff(buildRichDiff({ mapId: 1, tick: 1n }));
		expect(seen).toEqual([1n]);
	});
});
