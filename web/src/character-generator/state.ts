// Boxland — Character Generator pure state reducer.
//
// Headless module: no DOM, no network. The reducer takes a State + an
// Action and returns the next State. Validation is computed in the same
// pass so the UI can render error chips immediately on each change.
//
// Testable directly with Vitest; see state.test.ts. The page boot in
// entry-character-generator.ts wires DOM events -> dispatch -> render.

// ----- Catalog shapes (server payload from /design/characters/catalog).

export interface CatalogPart {
    id: number;
    name: string;
    asset_id: number;
    asset_url: string;
    layer_order: number | null;
    frame_map: Record<string, [number, number] | number> | undefined;
}

export interface CatalogSlot {
    id: number;
    key: string;
    label: string;
    required: boolean;
    order_index: number;
    default_layer_order: number;
    allows_palette: boolean;
    parts: CatalogPart[];
}

export type StatKind = "core" | "derived" | "resource" | "hidden";

export interface CatalogStatDef {
    key: string;
    label: string;
    kind: StatKind;
    default: number;
    min: number;
    max: number;
    creation_cost: number;
    display_order: number;
    formula?: string;
    cap?: number;
}

export interface CatalogStatSet {
    id: number;
    key: string;
    name: string;
    stats: CatalogStatDef[];
    creation_rules: { method: "" | "fixed" | "point_buy" | "freeform"; pool?: number };
}

export interface CatalogTalentNode {
    key: string;
    name: string;
    description: string;
    max_rank: number;
    cost: Record<string, number>;
    prerequisites: { node_key: string; min_rank: number }[];
    mutex_group?: string;
}

export interface CatalogTalentTree {
    id: number;
    key: string;
    name: string;
    description: string;
    currency_key: string;
    layout_mode: "tree" | "tiered" | "free_list" | "web";
    nodes: CatalogTalentNode[];
}

export interface Catalog {
    slots: CatalogSlot[];
    stat_sets: CatalogStatSet[];
    talent_trees: CatalogTalentTree[];
}

// ----- Recipe payload (mirrors server recipePayload).

export interface AppearanceSlot {
    slot_key: string;
    part_id: number;
    layer_order?: number;
    palette?: Record<string, string>;
}

export interface StatSelection {
    set_id?: number;
    allocations?: Record<string, number>;
}

export interface TalentSelection {
    /** Picks keyed by "<tree_key>.<node_key>" -> rank. */
    picks?: Record<string, number>;
}

export interface RecipePayload {
    id?: number;
    name: string;
    appearance: { slots: AppearanceSlot[] };
    stats?: StatSelection;
    talents?: TalentSelection;
}

// ----- State.

export interface ValidationItem {
    kind: "error" | "info";
    message: string;
}

export interface State {
    catalog: Catalog | null;
    recipeName: string;
    /** slot_key -> part_id (0 means "not selected"). Empty entries are omitted on save. */
    selections: Map<string, number>;
    /** Active animation key shown in the preview. Driven by user. */
    animation: string;
    /** Selected stat set id (0 = none). */
    statSetID: number;
    /** stat_key -> point-buy allocation (signed; refunds are -). */
    statAllocations: Map<string, number>;
    /** "<tree_key>.<node_key>" -> rank. */
    talentPicks: Map<string, number>;
    /** Validation results, recomputed on every action that touches selections/catalog. */
    validation: ValidationItem[];
    /** Bake status string from the server, or "" / "unsaved" / "saving". */
    bakeStatus: string;
}

// ----- Actions.

export type Action =
    | { kind: "set-catalog"; catalog: Catalog }
    | { kind: "set-name"; name: string }
    | { kind: "select-part"; slotKey: string; partId: number }
    | { kind: "clear-slot"; slotKey: string }
    | { kind: "load-recipe"; recipe: RecipePayload }
    | { kind: "set-animation"; animation: string }
    | { kind: "set-bake-status"; status: string }
    | { kind: "randomize"; seed: number }
    | { kind: "reset" }
    | { kind: "select-stat-set"; setID: number }
    | { kind: "set-stat-alloc"; statKey: string; delta: number }
    | { kind: "set-talent-rank"; treeKey: string; nodeKey: string; rank: number };

// ----- Reducer.

export function initialState(): State {
    return {
        catalog: null,
        recipeName: "",
        selections: new Map(),
        animation: "idle",
        statSetID: 0,
        statAllocations: new Map(),
        talentPicks: new Map(),
        validation: [],
        bakeStatus: "unsaved",
    };
}

export function reduce(s: State, a: Action): State {
    switch (a.kind) {
        case "set-catalog": {
            const next = { ...s, catalog: a.catalog };
            next.validation = computeValidation(next);
            // Pick the first available animation from any selected part.
            // No-op if nothing's selected; the user changes it via the
            // animation dropdown (which is populated from the same data).
            next.animation = pickInitialAnimation(next) ?? next.animation;
            return next;
        }
        case "set-name":
            return { ...s, recipeName: a.name };
        case "select-part": {
            const sel = new Map(s.selections);
            sel.set(a.slotKey, a.partId);
            const next = { ...s, selections: sel, bakeStatus: "unsaved" };
            next.validation = computeValidation(next);
            return next;
        }
        case "clear-slot": {
            const sel = new Map(s.selections);
            sel.delete(a.slotKey);
            const next = { ...s, selections: sel, bakeStatus: "unsaved" };
            next.validation = computeValidation(next);
            return next;
        }
        case "load-recipe": {
            const sel = new Map<string, number>();
            for (const e of a.recipe.appearance.slots ?? []) {
                if (e.part_id > 0) sel.set(e.slot_key, e.part_id);
            }
            const stats = a.recipe.stats ?? {};
            const allocs = new Map<string, number>();
            for (const [k, v] of Object.entries(stats.allocations ?? {})) {
                allocs.set(k, v);
            }
            const talents = a.recipe.talents ?? {};
            const picks = new Map<string, number>();
            for (const [k, v] of Object.entries(talents.picks ?? {})) {
                if (v > 0) picks.set(k, v);
            }
            const next = {
                ...s,
                recipeName: a.recipe.name,
                selections: sel,
                statSetID: stats.set_id ?? 0,
                statAllocations: allocs,
                talentPicks: picks,
                bakeStatus: "saved",
            };
            next.validation = computeValidation(next);
            next.animation = pickInitialAnimation(next) ?? next.animation;
            return next;
        }
        case "set-animation":
            return { ...s, animation: a.animation };
        case "set-bake-status":
            return { ...s, bakeStatus: a.status };
        case "randomize": {
            if (!s.catalog) return s;
            const sel = randomizeSelections(s.catalog, a.seed);
            const next = { ...s, selections: sel, bakeStatus: "unsaved" };
            next.validation = computeValidation(next);
            next.animation = pickInitialAnimation(next) ?? next.animation;
            return next;
        }
        case "reset":
            return {
                ...initialState(),
                catalog: s.catalog,
                recipeName: s.recipeName,
            };
        case "select-stat-set": {
            // Switching stat sets clears the per-stat allocations and
            // the picked talents (different sets have different stat
            // keys, so a stale allocation makes no sense).
            const next = {
                ...s,
                statSetID: a.setID,
                statAllocations: new Map<string, number>(),
                talentPicks: new Map<string, number>(),
                bakeStatus: "unsaved",
            };
            next.validation = computeValidation(next);
            return next;
        }
        case "set-stat-alloc": {
            const allocs = new Map(s.statAllocations);
            const cur = allocs.get(a.statKey) ?? 0;
            allocs.set(a.statKey, cur + a.delta);
            const next = { ...s, statAllocations: allocs, bakeStatus: "unsaved" };
            next.validation = computeValidation(next);
            return next;
        }
        case "set-talent-rank": {
            const picks = new Map(s.talentPicks);
            const k = `${a.treeKey}.${a.nodeKey}`;
            if (a.rank <= 0) picks.delete(k);
            else picks.set(k, a.rank);
            const next = { ...s, talentPicks: picks, bakeStatus: "unsaved" };
            next.validation = computeValidation(next);
            return next;
        }
    }
}

// ----- Derived selectors (pure).

/** Slots in DOM order = sort by order_index then id. */
export function orderedSlots(catalog: Catalog | null): CatalogSlot[] {
    if (!catalog) return [];
    return [...catalog.slots].sort((a, b) => {
        if (a.order_index !== b.order_index) return a.order_index - b.order_index;
        return a.id - b.id;
    });
}

/** The animation keys every selected part covers (canonical intersection). */
export function commonAnimations(s: State): string[] {
    if (!s.catalog) return [];
    const selectedParts: CatalogPart[] = [];
    for (const slot of s.catalog.slots) {
        const pid = s.selections.get(slot.key);
        if (!pid) continue;
        const part = slot.parts.find((p) => p.id === pid);
        if (part) selectedParts.push(part);
    }
    if (selectedParts.length === 0) return [];
    let inter: Set<string> | null = null;
    for (const part of selectedParts) {
        const keys = new Set<string>(Object.keys(part.frame_map ?? {}));
        if (inter === null) {
            inter = keys;
        } else {
            const next = new Set<string>();
            for (const k of inter) if (keys.has(k)) next.add(k);
            inter = next;
        }
    }
    return inter ? [...inter].sort() : [];
}

/** Layered render order: parts in (effective layer_order, part.id) order. */
export interface LayeredPart {
    slot: CatalogSlot;
    part: CatalogPart;
    effectiveLayer: number;
}

export function layered(s: State): LayeredPart[] {
    if (!s.catalog) return [];
    const out: LayeredPart[] = [];
    for (const slot of s.catalog.slots) {
        const pid = s.selections.get(slot.key);
        if (!pid) continue;
        const part = slot.parts.find((p) => p.id === pid);
        if (!part) continue;
        const eff = part.layer_order ?? slot.default_layer_order;
        out.push({ slot, part, effectiveLayer: eff });
    }
    out.sort((a, b) => {
        if (a.effectiveLayer !== b.effectiveLayer) return a.effectiveLayer - b.effectiveLayer;
        return a.part.id - b.part.id;
    });
    return out;
}

/** Build a RecipePayload ready to POST. */
export function toRecipePayload(s: State, recipeID: number | null): RecipePayload {
    const slots: AppearanceSlot[] = [];
    for (const slot of s.catalog?.slots ?? []) {
        const pid = s.selections.get(slot.key);
        if (pid) slots.push({ slot_key: slot.key, part_id: pid });
    }
    const allocations: Record<string, number> = {};
    for (const [k, v] of s.statAllocations) {
        if (v !== 0) allocations[k] = v;
    }
    const picks: Record<string, number> = {};
    for (const [k, v] of s.talentPicks) {
        if (v > 0) picks[k] = v;
    }
    const out: RecipePayload = {
        name: s.recipeName,
        appearance: { slots },
    };
    if (s.statSetID > 0 || Object.keys(allocations).length > 0) {
        out.stats = { set_id: s.statSetID, allocations };
    }
    if (Object.keys(picks).length > 0) {
        out.talents = { picks };
    }
    if (recipeID !== null) out.id = recipeID;
    return out;
}

// ---- Stat helpers -----------------------------------------------------

/** Return the currently-selected stat set, or null. */
export function activeStatSet(s: State): CatalogStatSet | null {
    if (!s.catalog || s.statSetID === 0) return null;
    return s.catalog.stat_sets.find((x) => x.id === s.statSetID) ?? null;
}

/** Compute the final value of every stat (core + derived + resource) given
 *  the current allocation. Pure — uses the same shape as the server's
 *  ResolveStats but skips formula evaluation: the UI shows raw final
 *  values for core/resource and a literal `default` for derived because
 *  evaluating formulas client-side would duplicate the Go evaluator and
 *  drift over time. The server's authoritative values land on the
 *  preview after publish. */
export function previewStatValues(s: State): Record<string, number> {
    const out: Record<string, number> = {};
    const set = activeStatSet(s);
    if (!set) return out;
    for (const def of set.stats) {
        if (def.kind === "core") {
            out[def.key] = def.default + (s.statAllocations.get(def.key) ?? 0);
        } else if (def.kind === "resource" || def.kind === "hidden") {
            out[def.key] = def.default;
        } else {
            // Derived stats deferred to the server; show "—" by omission.
        }
    }
    return out;
}

/** Total point-buy spend across core stats given the current allocation. */
export function pointBuySpend(s: State): number {
    const set = activeStatSet(s);
    if (!set) return 0;
    let total = 0;
    for (const def of set.stats) {
        if (def.kind !== "core") continue;
        const alloc = s.statAllocations.get(def.key) ?? 0;
        if (alloc > 0) total += alloc * def.creation_cost;
    }
    return total;
}

/** Remaining points the player can still spend (signed; negative = over). */
export function pointBuyRemaining(s: State): number {
    const set = activeStatSet(s);
    if (!set || set.creation_rules.method !== "point_buy") return 0;
    return (set.creation_rules.pool ?? 0) - pointBuySpend(s);
}

// ---- Talent helpers ---------------------------------------------------

/** Total talent-budget spend per currency, across every selected node in
 *  every tree. Mirrors the server validator's accounting. */
export function talentSpend(s: State): Record<string, number> {
    const out: Record<string, number> = {};
    if (!s.catalog) return out;
    for (const tree of s.catalog.talent_trees) {
        for (const node of tree.nodes) {
            const k = `${tree.key}.${node.key}`;
            const rank = s.talentPicks.get(k) ?? 0;
            if (rank <= 0) continue;
            for (const [currency, amt] of Object.entries(node.cost ?? {})) {
                out[currency] = (out[currency] ?? 0) + amt * rank;
            }
        }
    }
    return out;
}

// ----- Internal helpers.

function computeValidation(s: State): ValidationItem[] {
    const out: ValidationItem[] = [];
    if (!s.catalog) return out;
    // ---- Slot rules ----
    for (const slot of s.catalog.slots) {
        if (slot.required && !s.selections.get(slot.key)) {
            out.push({ kind: "error", message: `Missing required slot: ${slot.label}` });
        }
    }
    let selectedCount = 0;
    for (const slot of s.catalog.slots) {
        if (s.selections.get(slot.key)) selectedCount++;
    }
    if (selectedCount > 0 && commonAnimations(s).length === 0) {
        out.push({
            kind: "error",
            message: "Selected parts have no animation in common — bake will fail.",
        });
    }

    // ---- Stat rules ----
    const set = activeStatSet(s);
    if (set) {
        // Per-stat range.
        for (const def of set.stats) {
            if (def.kind !== "core") continue;
            const alloc = s.statAllocations.get(def.key) ?? 0;
            const final = def.default + alloc;
            if (final < def.min) {
                out.push({ kind: "error", message: `${def.label} (${final}) is below min ${def.min}` });
            }
            if (def.max !== 0 && final > def.max) {
                out.push({ kind: "error", message: `${def.label} (${final}) is above max ${def.max}` });
            }
        }
        // Point-buy budget.
        if (set.creation_rules.method === "point_buy") {
            const remaining = pointBuyRemaining(s);
            if (remaining < 0) {
                out.push({ kind: "error", message: `Spent ${-remaining} points over budget` });
            } else if (remaining > 0) {
                out.push({ kind: "error", message: `${remaining} points still to spend` });
            }
        }
    }

    // ---- Talent rules ----
    if (s.catalog.talent_trees.length > 0) {
        // Build budgets per tree from the current preview stat values.
        const stats = previewStatValues(s);
        for (const tree of s.catalog.talent_trees) {
            // Per-node sanity: rank <= max_rank, prereqs satisfied,
            // mutex group respected. We track ranked picks per tree
            // to reuse the prereq/mutex logic in one pass.
            const ranks: Record<string, number> = {};
            for (const node of tree.nodes) {
                const k = `${tree.key}.${node.key}`;
                const rank = s.talentPicks.get(k) ?? 0;
                if (rank > 0) ranks[node.key] = rank;
            }
            const mutexUsed = new Map<string, string>();
            for (const node of tree.nodes) {
                const rank = ranks[node.key] ?? 0;
                if (rank === 0) continue;
                if (rank > node.max_rank) {
                    out.push({ kind: "error", message: `${tree.name} / ${node.name}: rank ${rank} exceeds max ${node.max_rank}` });
                }
                for (const pr of node.prerequisites) {
                    const have = ranks[pr.node_key] ?? 0;
                    if (have < pr.min_rank) {
                        out.push({ kind: "error", message: `${tree.name} / ${node.name}: needs ${pr.node_key} rank ${pr.min_rank}` });
                    }
                }
                if (node.mutex_group) {
                    const taken = mutexUsed.get(node.mutex_group);
                    if (taken && taken !== node.key) {
                        out.push({ kind: "error", message: `${tree.name}: ${node.name} conflicts with ${taken} in mutex group ${node.mutex_group}` });
                    }
                    mutexUsed.set(node.mutex_group, node.key);
                }
            }
            // Budget check (per currency).
            const budget = stats[tree.currency_key] ?? 0;
            const spend = talentSpend(s)[tree.currency_key] ?? 0;
            if (spend > budget) {
                out.push({ kind: "error", message: `${tree.name}: spent ${spend} ${tree.currency_key}, have ${budget}` });
            }
        }
    }

    return out;
}

function pickInitialAnimation(s: State): string | null {
    const anims = commonAnimations(s);
    if (anims.length === 0) return null;
    if (anims.includes("idle")) return "idle";
    return anims[0] ?? null;
}

/** Tiny seeded LCG. Deterministic for a given seed; not crypto-grade. */
function lcg(seed: number): () => number {
    let x = seed >>> 0;
    return () => {
        x = (x * 1664525 + 1013904223) >>> 0;
        return x / 0x1_0000_0000;
    };
}

function randomizeSelections(catalog: Catalog, seed: number): Map<string, number> {
    const rand = lcg(seed);
    const sel = new Map<string, number>();
    for (const slot of catalog.slots) {
        if (slot.parts.length === 0) {
            // Skip empty slots (required slots without parts will surface
            // as a validation error). Don't try to fabricate an entry.
            continue;
        }
        // Required slots always get filled; optional slots get filled
        // 70% of the time so a randomized character isn't every-slot
        // crowded by default.
        if (!slot.required && rand() < 0.3) continue;
        const idx = Math.floor(rand() * slot.parts.length);
        const pick = slot.parts[idx];
        if (pick) sel.set(slot.key, pick.id);
    }
    return sel;
}
