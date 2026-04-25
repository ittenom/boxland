// Boxland — net/mailbox.ts
//
// AOI mailbox. Owns the client-side cache of:
//   * entity state (id -> snapshot fields the renderer reads)
//   * tile changes (latest known cell per (layer, gx, gy))
//   * lighting cells (latest known per (gx, gy))
//   * per-chunk acked versions (the "I have applied this version" vector)
//
// The mailbox processes server Diffs with three rules:
//
//   1. Apply added + moved entities -> entity cache.
//   2. Apply removed -> drop.
//   3. Walk Diff.chunks; for each chunk, advance acked[chunkId] iff the
//      diff version > the prior acked. Drop a stale frame silently —
//      this is the "version vector reconciles itself" property.
//
// On reconnect the host application reads `gapsSince(lastAppliedTick)`
// to decide whether to replay an AckTick (resume) or send a fresh
// JoinMap to drop the cache and pull a Snapshot.
//
// PLAN.md §4h: per-chunk version-vector AOI replaces per-player snapshots.
// PLAN.md §4l reconnect rule consumes the same vector to choose
// resend-Diffs vs send-Snapshot.

import { Diff } from "@proto/diff.js";
import { ChunkVersion } from "@proto/chunk-version.js";
import { EntityState } from "@proto/entity-state.js";
import { Tile } from "@proto/tile.js";
import { LightingCell } from "@proto/lighting-cell.js";
import { AudioEvent } from "@proto/audio-event.js";
import type { AppliedDiff, DiffListener } from "./types";

/** Mailbox-local snapshot of one entity. Plain object so renderers don't
 *  need to keep FlatBuffer views around between ticks. */
export interface CachedEntity {
	id: bigint;
	typeId: number;
	x: number;
	y: number;
	facing: number;
	animId: number;
	animFrame: number;
	variantId: number;
	tint: number;
	nameplate: string;
	hpPct: number;
}

/** Mailbox-local snapshot of one tile cell. */
export interface CachedTile {
	layerId: number;
	gx: number;
	gy: number;
	assetId: number;
	frame: number;
	collisionShape: number;
	edgeCollisions: number;
	collisionLayerMask: number;
}

/** Mailbox-local lighting cell. */
export interface CachedLighting {
	gx: number;
	gy: number;
	color: number;
	intensity: number;
}

/** Mailbox-local audio event (ephemeral; consumed each frame). */
export interface CachedAudio {
	soundId: number;
	hasPosition: boolean;
	x: number;
	y: number;
	volume: number;
	pitch: number;
}

export class Mailbox {
	private readonly entities = new Map<bigint, CachedEntity>();
	private readonly tiles = new Map<string, CachedTile>();        // key: `${layer}:${gx}:${gy}`
	private readonly lighting = new Map<string, CachedLighting>(); // key: `${gx}:${gy}`
	private readonly acked = new Map<bigint, bigint>();            // chunkId -> version

	// Audio events are queued each diff and the host drains them once.
	private audioQueue: CachedAudio[] = [];

	private lastAppliedTick: bigint = 0n;

	private readonly listeners = new Set<DiffListener>();

	// ---- Reads ----

	getEntity(id: bigint): CachedEntity | undefined { return this.entities.get(id); }
	allEntities(): IterableIterator<CachedEntity> { return this.entities.values(); }
	entityCount(): number { return this.entities.size; }

	getTile(layerId: number, gx: number, gy: number): CachedTile | undefined {
		return this.tiles.get(tileKey(layerId, gx, gy));
	}
	tileCount(): number { return this.tiles.size; }
	/** Iterate every cached tile across all layers. Used by the game
	 *  loop to feed the collision module's World shape (PLAN.md §6h
	 *  client-side prediction). Cheap; single Map.values() walk. */
	allTiles(): IterableIterator<CachedTile> { return this.tiles.values(); }

	getLighting(gx: number, gy: number): CachedLighting | undefined {
		return this.lighting.get(lightingKey(gx, gy));
	}

	getAckedVersion(chunkId: bigint): bigint { return this.acked.get(chunkId) ?? 0n; }
	getLastAppliedTick(): bigint { return this.lastAppliedTick; }

	/** Drain queued audio events. The host plays them, then the queue resets. */
	drainAudio(): CachedAudio[] {
		const out = this.audioQueue;
		this.audioQueue = [];
		return out;
	}

	// ---- Listener registration ----

	onDiff(l: DiffListener): () => void {
		this.listeners.add(l);
		return () => this.listeners.delete(l);
	}

	// ---- Reset ----

	/** Drop everything. Use on Spectate -> JoinMap, or after detecting a
	 *  gap too large to resume (host policy). */
	reset(): void {
		this.entities.clear();
		this.tiles.clear();
		this.lighting.clear();
		this.acked.clear();
		this.audioQueue = [];
		this.lastAppliedTick = 0n;
	}

	// ---- Apply ----

	/**
	 * Apply one server Diff. Returns the AppliedDiff summary used by the
	 * NetClient to fan out to host listeners. Stale per-chunk versions
	 * are dropped silently — older-than-acked is normal under reorder
	 * (e.g. transient WS retransmit) and shouldn't poison the cache.
	 */
	applyDiff(d: Diff): AppliedDiff {
		const tick = d.tick();
		const mapId = d.mapId();

		const addedIds: bigint[] = [];
		const removedIds: bigint[] = [];
		const movedIds: bigint[] = [];

		// Added entities (new in AOI).
		const addedN = d.addedLength();
		const tmpEntity = new EntityState();
		for (let i = 0; i < addedN; i++) {
			const e = d.added(i, tmpEntity);
			if (!e) continue;
			const id = e.id();
			this.entities.set(id, snapshotEntity(e));
			addedIds.push(id);
		}

		// Moved entities (updated state).
		const movedN = d.movedLength();
		for (let i = 0; i < movedN; i++) {
			const e = d.moved(i, tmpEntity);
			if (!e) continue;
			const id = e.id();
			this.entities.set(id, snapshotEntity(e));
			movedIds.push(id);
		}

		// Removed entities (left AOI / despawned).
		const removedN = d.removedLength();
		for (let i = 0; i < removedN; i++) {
			const id = d.removed(i);
			if (id === null || id === undefined) continue;
			this.entities.delete(id);
			removedIds.push(id);
		}

		// Tile changes.
		const tileN = d.tileChangesLength();
		const tmpTile = new Tile();
		for (let i = 0; i < tileN; i++) {
			const t = d.tileChanges(i, tmpTile);
			if (!t) continue;
			this.tiles.set(tileKey(t.layerId(), t.gx(), t.gy()), snapshotTile(t));
		}

		// Lighting changes.
		const lightN = d.lightingChangesLength();
		const tmpLight = new LightingCell();
		for (let i = 0; i < lightN; i++) {
			const lc = d.lightingChanges(i, tmpLight);
			if (!lc) continue;
			// Intensity 0 == clear (matches server PlaceLighting semantics).
			if (lc.intensity() === 0) {
				this.lighting.delete(lightingKey(lc.gx(), lc.gy()));
			} else {
				this.lighting.set(lightingKey(lc.gx(), lc.gy()), snapshotLighting(lc));
			}
		}

		// Audio events: queue, host drains.
		const audioN = d.audioLength();
		const tmpAudio = new AudioEvent();
		for (let i = 0; i < audioN; i++) {
			const a = d.audio(i, tmpAudio);
			if (!a) continue;
			this.audioQueue.push(snapshotAudio(a));
		}

		// Per-chunk version vector.
		const advancedChunks: bigint[] = [];
		const chunkN = d.chunksLength();
		const tmpCV = new ChunkVersion();
		for (let i = 0; i < chunkN; i++) {
			const cv = d.chunks(i, tmpCV);
			if (!cv) continue;
			const cid = cv.chunkId();
			const ver = cv.version();
			const prior = this.acked.get(cid) ?? 0n;
			if (ver > prior) {
				this.acked.set(cid, ver);
				advancedChunks.push(cid);
			}
		}

		if (tick > this.lastAppliedTick) this.lastAppliedTick = tick;

		const applied: AppliedDiff = {
			mapId,
			tick,
			addedIds,
			removedIds,
			movedIds,
			tileChangeCount: tileN,
			lightingChangeCount: lightN,
			audioCount: audioN,
			advancedChunks,
		};
		for (const l of this.listeners) {
			try { l(applied); } catch { /* isolate */ }
		}
		return applied;
	}

	// ---- Reconnect helpers ----

	/**
	 * Snapshot the current acked vector. Used by the host on reconnect
	 * to decide whether to AckTick or to reset + re-Snapshot.
	 */
	snapshotAcks(): Array<{ chunkId: bigint; version: bigint }> {
		const out: Array<{ chunkId: bigint; version: bigint }> = [];
		for (const [chunkId, version] of this.acked) {
			out.push({ chunkId, version });
		}
		return out;
	}
}

function tileKey(layerId: number, gx: number, gy: number): string {
	return `${layerId}:${gx}:${gy}`;
}
function lightingKey(gx: number, gy: number): string {
	return `${gx}:${gy}`;
}

function snapshotEntity(e: EntityState): CachedEntity {
	return {
		id: e.id(),
		typeId: e.typeId(),
		x: e.x(),
		y: e.y(),
		facing: e.facing(),
		animId: e.animId(),
		animFrame: e.animFrame(),
		variantId: e.variantId(),
		tint: e.tint(),
		nameplate: e.nameplate() ?? "",
		hpPct: e.hpPct(),
	};
}
function snapshotTile(t: Tile): CachedTile {
	return {
		layerId: t.layerId(),
		gx: t.gx(),
		gy: t.gy(),
		assetId: t.assetId(),
		frame: t.frame(),
		collisionShape: t.collisionShape(),
		edgeCollisions: t.edgeCollisions(),
		collisionLayerMask: t.collisionLayerMask(),
	};
}
function snapshotLighting(lc: LightingCell): CachedLighting {
	return {
		gx: lc.gx(),
		gy: lc.gy(),
		color: lc.color(),
		intensity: lc.intensity(),
	};
}
function snapshotAudio(a: AudioEvent): CachedAudio {
	return {
		soundId: a.soundId(),
		hasPosition: a.hasPosition(),
		x: a.x(),
		y: a.y(),
		volume: a.volume(),
		pitch: a.pitch(),
	};
}
