// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import {
    initialState, reduce, orderedSlots, layered, commonAnimations, toRecipePayload,
    activeStatSet, previewStatValues, pointBuySpend, pointBuyRemaining, talentSpend,
    type Catalog, type CatalogStatSet, type CatalogTalentTree, type State,
} from "./state";

// ---- Fixture builders ----------------------------------------------------

function makeCatalog(): Catalog {
    return {
        stat_sets: [],
        talent_trees: [],
        slots: [
            {
                id: 1, key: "body", label: "Body", required: true,
                order_index: 10, default_layer_order: 100, allows_palette: false,
                parts: [
                    { id: 100, name: "Body A", asset_id: 1, asset_url: "/a.png", layer_order: null, frame_map: { idle: [0, 0], walk: [1, 4] } },
                    { id: 101, name: "Body B", asset_id: 2, asset_url: "/b.png", layer_order: null, frame_map: { idle: [0, 0], walk: [1, 4] } },
                ],
            },
            {
                id: 2, key: "hair_front", label: "Hair (front)", required: false,
                order_index: 20, default_layer_order: 300, allows_palette: false,
                parts: [
                    { id: 200, name: "Hair A", asset_id: 3, asset_url: "/h.png", layer_order: null, frame_map: { idle: [0, 0], walk: [1, 4] } },
                ],
            },
            {
                id: 3, key: "weapon", label: "Weapon", required: false,
                order_index: 30, default_layer_order: 500, allows_palette: false,
                parts: [
                    { id: 300, name: "Sword", asset_id: 4, asset_url: "/s.png", layer_order: 600, frame_map: { idle: [0, 0] } },
                ],
            },
        ],
    };
}

function withCatalog(): State {
    return reduce(initialState(), { kind: "set-catalog", catalog: makeCatalog() });
}

// ---- Reducer cases -------------------------------------------------------

describe("reducer", () => {
    it("set-catalog populates the catalog and recomputes validation", () => {
        const s = withCatalog();
        expect(s.catalog?.slots).toHaveLength(3);
        // body is required and unfilled -> one validation error.
        expect(s.validation.some((v) => v.message.includes("Body"))).toBe(true);
    });

    it("select-part fills the slot and clears its required-missing error", () => {
        let s = withCatalog();
        s = reduce(s, { kind: "select-part", slotKey: "body", partId: 100 });
        expect(s.selections.get("body")).toBe(100);
        expect(s.validation.some((v) => v.message.includes("Body"))).toBe(false);
        expect(s.bakeStatus).toBe("unsaved");
    });

    it("clear-slot removes a previously-selected part", () => {
        let s = withCatalog();
        s = reduce(s, { kind: "select-part", slotKey: "body", partId: 100 });
        s = reduce(s, { kind: "clear-slot", slotKey: "body" });
        expect(s.selections.has("body")).toBe(false);
        expect(s.validation.some((v) => v.message.includes("Body"))).toBe(true);
    });

    it("randomize is deterministic for a given seed and respects required slots", () => {
        const a = reduce(withCatalog(), { kind: "randomize", seed: 12345 });
        const b = reduce(withCatalog(), { kind: "randomize", seed: 12345 });
        // Same seed -> identical selection map.
        expect([...a.selections]).toEqual([...b.selections]);
        // Required slot must always be filled.
        expect(a.selections.get("body")).toBeGreaterThan(0);
    });

    it("randomize on a second seed produces a different result (high probability)", () => {
        const a = reduce(withCatalog(), { kind: "randomize", seed: 1 });
        const b = reduce(withCatalog(), { kind: "randomize", seed: 999_999 });
        expect([...a.selections]).not.toEqual([...b.selections]);
    });

    it("reset clears selections but keeps the catalog and recipe name", () => {
        let s = withCatalog();
        s = reduce(s, { kind: "set-name", name: "My hero" });
        s = reduce(s, { kind: "select-part", slotKey: "body", partId: 100 });
        s = reduce(s, { kind: "reset" });
        expect(s.selections.size).toBe(0);
        expect(s.recipeName).toBe("My hero");
        expect(s.catalog?.slots).toHaveLength(3);
    });

    it("load-recipe rebuilds selections from a recipe payload", () => {
        let s = withCatalog();
        s = reduce(s, {
            kind: "load-recipe",
            recipe: {
                name: "Test",
                appearance: { slots: [{ slot_key: "body", part_id: 101 }] },
            },
        });
        expect(s.selections.get("body")).toBe(101);
        expect(s.recipeName).toBe("Test");
        expect(s.bakeStatus).toBe("saved");
    });
});

// ---- Derivations ---------------------------------------------------------

describe("derivations", () => {
    it("orderedSlots returns slots sorted by order_index", () => {
        const cat = makeCatalog();
        // Reverse to test stable ordering.
        cat.slots.reverse();
        const ordered = orderedSlots(cat).map((s) => s.key);
        expect(ordered).toEqual(["body", "hair_front", "weapon"]);
    });

    it("layered respects part.layer_order override over slot.default_layer_order", () => {
        let s = withCatalog();
        s = reduce(s, { kind: "select-part", slotKey: "body", partId: 100 });        // layer 100
        s = reduce(s, { kind: "select-part", slotKey: "weapon", partId: 300 });      // layer override 600
        s = reduce(s, { kind: "select-part", slotKey: "hair_front", partId: 200 });  // layer 300
        const order = layered(s).map((l) => l.slot.key);
        expect(order).toEqual(["body", "hair_front", "weapon"]);
    });

    it("commonAnimations is the intersection across selected parts", () => {
        let s = withCatalog();
        s = reduce(s, { kind: "select-part", slotKey: "body", partId: 100 });
        s = reduce(s, { kind: "select-part", slotKey: "hair_front", partId: 200 });
        expect(commonAnimations(s)).toEqual(["idle", "walk"]);
        // Adding the weapon (which only has 'idle') drops 'walk'.
        s = reduce(s, { kind: "select-part", slotKey: "weapon", partId: 300 });
        expect(commonAnimations(s)).toEqual(["idle"]);
    });

    it("validation surfaces empty-intersection errors", () => {
        // Manufacture a catalog where two parts have no shared anim.
        const cat: Catalog = {
            stat_sets: [],
            talent_trees: [],
            slots: [
                {
                    id: 1, key: "body", label: "Body", required: true,
                    order_index: 10, default_layer_order: 100, allows_palette: false,
                    parts: [{ id: 100, name: "x", asset_id: 1, asset_url: "/a", layer_order: null, frame_map: { idle: [0, 0] } }],
                },
                {
                    id: 2, key: "hair_front", label: "Hair", required: false,
                    order_index: 20, default_layer_order: 300, allows_palette: false,
                    parts: [{ id: 200, name: "y", asset_id: 2, asset_url: "/b", layer_order: null, frame_map: { walk: [0, 0] } }],
                },
            ],
        };
        let s = reduce(initialState(), { kind: "set-catalog", catalog: cat });
        s = reduce(s, { kind: "select-part", slotKey: "body", partId: 100 });
        s = reduce(s, { kind: "select-part", slotKey: "hair_front", partId: 200 });
        expect(s.validation.some((v) => v.message.includes("no animation in common"))).toBe(true);
    });

    it("toRecipePayload emits one slot entry per selection", () => {
        let s = withCatalog();
        s = reduce(s, { kind: "set-name", name: "Hero" });
        s = reduce(s, { kind: "select-part", slotKey: "body", partId: 100 });
        const payload = toRecipePayload(s, null);
        expect(payload.name).toBe("Hero");
        expect(payload.appearance.slots).toEqual([{ slot_key: "body", part_id: 100 }]);
        expect(payload.id).toBeUndefined();

        const withID = toRecipePayload(s, 42);
        expect(withID.id).toBe(42);
    });
});

// ---- Stat fixtures + cases ----------------------------------------------

function makeStatSet(): CatalogStatSet {
    return {
        id: 1, key: "default", name: "Default",
        stats: [
            { key: "might", label: "Might", kind: "core", default: 1, min: 1, max: 10, creation_cost: 1, display_order: 1 },
            { key: "wit", label: "Wit", kind: "core", default: 1, min: 1, max: 10, creation_cost: 1, display_order: 2 },
            { key: "talent_points", label: "Talent points", kind: "resource", default: 5, min: 0, max: 0, creation_cost: 0, display_order: 99 },
        ],
        creation_rules: { method: "point_buy", pool: 4 },
    };
}

function withStatCatalog(): State {
    const cat = makeCatalog();
    cat.stat_sets = [makeStatSet()];
    let s = reduce(initialState(), { kind: "set-catalog", catalog: cat });
    s = reduce(s, { kind: "select-stat-set", setID: 1 });
    return s;
}

describe("stat allocator", () => {
    it("select-stat-set populates statSetID and clears prior allocations", () => {
        let s = withStatCatalog();
        s = reduce(s, { kind: "set-stat-alloc", statKey: "might", delta: 2 });
        expect(s.statAllocations.get("might")).toBe(2);
        // Switching to another set wipes allocations.
        s = reduce(s, { kind: "select-stat-set", setID: 0 });
        expect(s.statSetID).toBe(0);
        expect(s.statAllocations.size).toBe(0);
    });

    it("set-stat-alloc accumulates delta", () => {
        let s = withStatCatalog();
        s = reduce(s, { kind: "set-stat-alloc", statKey: "might", delta: 3 });
        s = reduce(s, { kind: "set-stat-alloc", statKey: "might", delta: -1 });
        expect(s.statAllocations.get("might")).toBe(2);
    });

    it("activeStatSet returns the selected set", () => {
        const s = withStatCatalog();
        const set = activeStatSet(s);
        expect(set?.id).toBe(1);
    });

    it("previewStatValues sums core defaults + allocations", () => {
        let s = withStatCatalog();
        s = reduce(s, { kind: "set-stat-alloc", statKey: "might", delta: 3 });
        const vals = previewStatValues(s);
        expect(vals["might"]).toBe(4); // default 1 + alloc 3
        expect(vals["wit"]).toBe(1);   // unchanged
        expect(vals["talent_points"]).toBe(5); // resource default
    });

    it("pointBuySpend / pointBuyRemaining track the pool", () => {
        let s = withStatCatalog();
        s = reduce(s, { kind: "set-stat-alloc", statKey: "might", delta: 2 });
        s = reduce(s, { kind: "set-stat-alloc", statKey: "wit", delta: 1 });
        expect(pointBuySpend(s)).toBe(3);
        expect(pointBuyRemaining(s)).toBe(1); // pool 4 - 3
    });

    it("validation surfaces overspend and remaining points", () => {
        let s = withStatCatalog();
        // 0 spent of 4 -> "4 points still to spend".
        expect(s.validation.some((v) => v.message.includes("4 points still"))).toBe(true);
        // Overspend: 5 of 4.
        s = reduce(s, { kind: "set-stat-alloc", statKey: "might", delta: 5 });
        expect(s.validation.some((v) => v.message.includes("over budget"))).toBe(true);
    });

    it("validation surfaces above-max", () => {
        let s = withStatCatalog();
        // might max=10; default=1; +20 -> 21 > 10
        s = reduce(s, { kind: "set-stat-alloc", statKey: "might", delta: 20 });
        expect(s.validation.some((v) => v.message.includes("above max"))).toBe(true);
    });
});

// ---- Talent fixtures + cases --------------------------------------------

function makeTalentTree(): CatalogTalentTree {
    return {
        id: 1, key: "warrior", name: "Warrior", description: "",
        currency_key: "talent_points", layout_mode: "tree",
        nodes: [
            { key: "cleave", name: "Cleave", description: "", max_rank: 1, cost: { talent_points: 1 }, prerequisites: [] },
            { key: "sweep", name: "Sweep", description: "", max_rank: 3, cost: { talent_points: 1 }, prerequisites: [{ node_key: "cleave", min_rank: 1 }] },
            { key: "shield", name: "Shield", description: "", max_rank: 1, cost: { talent_points: 1 }, prerequisites: [], mutex_group: "weapon" },
            { key: "two_hand", name: "Two-Hand", description: "", max_rank: 1, cost: { talent_points: 1 }, prerequisites: [], mutex_group: "weapon" },
        ],
    };
}

function withTalentCatalog(): State {
    const cat = makeCatalog();
    cat.stat_sets = [makeStatSet()];
    cat.talent_trees = [makeTalentTree()];
    let s = reduce(initialState(), { kind: "set-catalog", catalog: cat });
    s = reduce(s, { kind: "select-stat-set", setID: 1 });
    return s;
}

describe("talent picker", () => {
    it("set-talent-rank stores 'tree.node' -> rank and removes when rank<=0", () => {
        let s = withTalentCatalog();
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "cleave", rank: 1 });
        expect(s.talentPicks.get("warrior.cleave")).toBe(1);
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "cleave", rank: 0 });
        expect(s.talentPicks.has("warrior.cleave")).toBe(false);
    });

    it("validation flags missing prereq", () => {
        let s = withTalentCatalog();
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "sweep", rank: 1 });
        expect(s.validation.some((v) => v.message.includes("needs cleave"))).toBe(true);
    });

    it("validation flags mutex group conflict", () => {
        let s = withTalentCatalog();
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "shield", rank: 1 });
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "two_hand", rank: 1 });
        expect(s.validation.some((v) => v.message.includes("mutex group weapon"))).toBe(true);
    });

    it("validation flags exceed max_rank", () => {
        let s = withTalentCatalog();
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "cleave", rank: 5 });
        expect(s.validation.some((v) => v.message.includes("exceeds max"))).toBe(true);
    });

    it("validation flags budget overspend (talent_points = 5 stat resource)", () => {
        let s = withTalentCatalog();
        // Spend 6 (cleave 1 + sweep 3 + shield 1 + two_hand 1, but mutex...
        // Use cleave 1 + sweep 3 + shield 1 = 5 -> spends exactly 5, ok.
        // To overspend: cleave 1 + sweep 3 + shield 1 + ??? Only 4 nodes.
        // Add an extra rank: spend cleave 1 + sweep 3 + shield 1 + two_hand 1 = 6
        // (mutex will also fire; we expect both errors.)
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "cleave", rank: 1 });
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "sweep", rank: 3 });
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "shield", rank: 1 });
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "two_hand", rank: 1 });
        expect(s.validation.some((v) => v.message.includes("spent 6 talent_points"))).toBe(true);
    });

    it("talentSpend tallies across nodes correctly", () => {
        let s = withTalentCatalog();
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "cleave", rank: 1 });
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "sweep", rank: 2 });
        expect(talentSpend(s)["talent_points"]).toBe(3); // 1 + 2
    });

    it("toRecipePayload emits stats and talents only when populated", () => {
        let s = withTalentCatalog();
        // Empty stat allocations but a stat set is selected -> stats payload exists.
        let payload = toRecipePayload(s, null);
        expect(payload.stats?.set_id).toBe(1);
        expect(payload.talents).toBeUndefined();

        // Add a talent pick.
        s = reduce(s, { kind: "set-talent-rank", treeKey: "warrior", nodeKey: "cleave", rank: 1 });
        payload = toRecipePayload(s, null);
        expect(payload.talents?.picks).toEqual({ "warrior.cleave": 1 });
    });
});
