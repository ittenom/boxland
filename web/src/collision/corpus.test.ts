// @vitest-environment node
//
// Cross-runtime collision determinism corpus.
//
// Loads /shared/test-vectors/collision.json (authored by task #40 and
// thereafter) and runs each vector through the web `move` implementation,
// asserting the resolved delta matches the expected value byte-for-byte.
// The Go server runs the same corpus against its own implementation; both
// MUST produce identical results.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import { buildWorld, move } from "./index";
import type { AABB, Tile } from "./types";

interface VectorWorld {
	tiles: Array<{
		gx: number;
		gy: number;
		edge_collisions: number;
		collision_layer_mask: number;
	}>;
}

interface Vector {
	name: string;
	world: VectorWorld;
	entity: { aabb: [number, number, number, number]; mask: number };
	delta: [number, number];
	expected_resolved_delta: [number, number];
}

interface Corpus {
	$schema_version: number;
	description?: string;
	vectors: Vector[];
}

function loadCorpus(): Corpus {
	const here = fileURLToPath(new URL(".", import.meta.url));
	const path = resolve(here, "../../../shared/test-vectors/collision.json");
	const raw = readFileSync(path, "utf-8");
	return JSON.parse(raw) as Corpus;
}

function aabbFromArray(arr: [number, number, number, number]): AABB {
	return { left: arr[0]!, top: arr[1]!, right: arr[2]!, bottom: arr[3]! };
}

function tilesFromVector(vw: VectorWorld): Tile[] {
	return vw.tiles.map((t) => ({
		gx: t.gx,
		gy: t.gy,
		edge_collisions: t.edge_collisions,
		collision_layer_mask: t.collision_layer_mask,
	}));
}

describe("collision corpus (shared/test-vectors/collision.json)", () => {
	const corpus = loadCorpus();

	it("schema version is supported", () => {
		expect(corpus.$schema_version).toBe(1);
	});

	it("file is loadable and is an array", () => {
		expect(Array.isArray(corpus.vectors)).toBe(true);
	});

	if (corpus.vectors.length === 0) {
		// Authored in task #40. Don't fail before then.
		it.skip("vectors authored in task #40", () => undefined);
		return;
	}

	for (const v of corpus.vectors) {
		it(`vector: ${v.name}`, () => {
			const world = buildWorld(tilesFromVector(v.world));
			const entity = { aabb: aabbFromArray(v.entity.aabb), mask: v.entity.mask };
			const result = move(entity, v.delta[0], v.delta[1], world);
			expect([result.resolvedDx, result.resolvedDy]).toEqual(v.expected_resolved_delta);
		});
	}
});
