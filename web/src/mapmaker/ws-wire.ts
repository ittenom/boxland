// Boxland — mapmaker WS-backed wire.
//
// Shape mirrors the level editor's WSPlacementWire: same EditorWire,
// different opcodes. The mapmaker's existing state/tools modules
// already group their effects into "place tile" / "erase tile"
// batches so the per-stroke Promise we return resolves once the
// inbound diffs carry the matching changes.
//
// v1 supports the canonical tile placement + erase opcodes plus
// the lock / unlock / set-tile-rotation extras. fill / sample /
// procedural-mode interactions land in a later iteration; for v1
// they'd dispatch a series of PlaceTiles / EraseTiles batches the
// same way.

import * as flatbuffers from "flatbuffers";

import type { EditorWire } from "@render";
import { DesignerOpcode } from "@proto/designer-opcode.js";
import { PlaceTilesPayload } from "@proto/place-tiles-payload.js";
import { EraseTilesPayload } from "@proto/erase-tiles-payload.js";
import { TilePlacement } from "@proto/tile-placement.js";
import { EraseTilePoint } from "@proto/erase-tile-point.js";

import type { MapTile } from "./types";

export class WSMapmakerWire {
	constructor(
		private readonly wire: EditorWire,
		private readonly mapId: number,
	) {}

	/** Place one or more tiles. v1: fire-and-forget. The server
	 *  applies + broadcasts a TilePlaced diff per cell which the
	 *  state's diff handler picks up and reconciles. */
	placeTiles(tiles: readonly MapTile[]): void {
		this.dispatchPlace(DesignerOpcode.PlaceTiles, tiles);
	}

	/** Erase one or more tiles by (layer_id, x, y). */
	eraseTiles(points: ReadonlyArray<{ layerId: number; x: number; y: number }>): void {
		this.dispatchErase(DesignerOpcode.EraseTiles, points);
	}

	/** Lock one or more tiles. The server re-uses the
	 *  PlaceTilesPayload shape, so this looks identical to
	 *  placeTiles but routes to the LockTiles opcode. */
	lockTiles(tiles: readonly MapTile[]): void {
		this.dispatchPlace(DesignerOpcode.LockTiles, tiles);
	}

	/** Unlock one or more tiles by (layer_id, x, y). */
	unlockTiles(points: ReadonlyArray<{ layerId: number; x: number; y: number }>): void {
		this.dispatchErase(DesignerOpcode.UnlockTiles, points);
	}

	private dispatchPlace(opcode: number, tiles: readonly MapTile[]): void {
		if (tiles.length === 0) return;
		const b = new flatbuffers.Builder(64 + tiles.length * 32);
		const tileOffsets = tiles.map((t) => {
			TilePlacement.startTilePlacement(b);
			TilePlacement.addLayerId(b, t.layerId);
			TilePlacement.addX(b, t.x);
			TilePlacement.addY(b, t.y);
			TilePlacement.addEntityTypeId(b, BigInt(t.entityTypeId));
			return TilePlacement.endTilePlacement(b);
		});
		PlaceTilesPayload.startTilesVector(b, tileOffsets.length);
		for (let i = tileOffsets.length - 1; i >= 0; i--) b.addOffset(tileOffsets[i]!);
		const tilesVec = b.endVector();

		PlaceTilesPayload.startPlaceTilesPayload(b);
		PlaceTilesPayload.addMapId(b, this.mapId);
		PlaceTilesPayload.addTiles(b, tilesVec);
		const root = PlaceTilesPayload.endPlaceTilesPayload(b);
		b.finish(root);
		this.wire.dispatch(opcode, b.asUint8Array());
	}

	private dispatchErase(opcode: number, points: ReadonlyArray<{ layerId: number; x: number; y: number }>): void {
		if (points.length === 0) return;
		const b = new flatbuffers.Builder(64 + points.length * 16);
		const pointOffsets = points.map((p) => {
			EraseTilePoint.startEraseTilePoint(b);
			EraseTilePoint.addLayerId(b, p.layerId);
			EraseTilePoint.addX(b, p.x);
			EraseTilePoint.addY(b, p.y);
			return EraseTilePoint.endEraseTilePoint(b);
		});
		EraseTilesPayload.startPointsVector(b, pointOffsets.length);
		for (let i = pointOffsets.length - 1; i >= 0; i--) b.addOffset(pointOffsets[i]!);
		const pointsVec = b.endVector();

		EraseTilesPayload.startEraseTilesPayload(b);
		EraseTilesPayload.addMapId(b, this.mapId);
		EraseTilesPayload.addPoints(b, pointsVec);
		const root = EraseTilesPayload.endEraseTilesPayload(b);
		b.finish(root);
		this.wire.dispatch(opcode, b.asUint8Array());
	}
}
