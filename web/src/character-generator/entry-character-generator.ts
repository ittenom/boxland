// Boxland — Character Generator page boot.
//
// Reads boot config from data-bx-* attributes on the host element,
// fetches the catalog + (optionally) the existing recipe, wires DOM
// events to the pure reducer, mounts the live layered preview, and
// drives Save / Randomize / Reset / Copy-JSON.
//
// Save flow:
//   1. POST /design/characters/recipes (or PATCH-style POST {id}) with
//      the typed recipe payload.
//   2. POST /design/characters/npc-templates/{id}/attach-recipe to link
//      the template to its recipe.
//   3. POST /design/characters/npc-templates/{id}/draft to write a
//      drafts row that includes the recipe id, so the next Push to Live
//      runs the bake.

import {
    initialState, reduce, orderedSlots, layered, commonAnimations, toRecipePayload,
    activeStatSet, previewStatValues, pointBuyRemaining, talentSpend,
    type Action, type Catalog, type RecipePayload, type State, type CatalogTalentTree,
} from "./state";
import { LayerPreview } from "./preview";

// ---- Boot config ---------------------------------------------------------

interface BootConfig {
    templateID: number;
    templateName: string;
    recipeID: number; // 0 = no recipe yet
    catalogURL: string;
    recipeBase: string;
    attachBase: string;
    templateDraftBase: string;
}

function readBootConfig(host: HTMLElement): BootConfig {
    const ds = host.dataset;
    const need = (k: string): string => {
        const v = ds[k];
        if (v === undefined) throw new Error(`character generator: missing data-bx-${camelToKebab(k)}`);
        return v;
    };
    return {
        templateID: parseInt(need("templateId"), 10),
        templateName: need("templateName"),
        recipeID: parseInt(need("recipeId"), 10) || 0,
        catalogURL: need("catalogUrl"),
        recipeBase: need("recipeBase"),
        attachBase: need("attachBase"),
        templateDraftBase: need("templateDraftBase"),
    };
}

function camelToKebab(s: string): string {
    return s.replace(/[A-Z]/g, (c) => "-" + c.toLowerCase());
}

// ---- CSRF ----------------------------------------------------------------

function csrfToken(): string {
    const meta = document.querySelector('meta[name="csrf-token"]');
    return meta?.getAttribute("content") ?? "";
}

// ---- Network -------------------------------------------------------------

async function fetchCatalog(url: string): Promise<Catalog> {
    const r = await fetch(url, { credentials: "same-origin" });
    if (!r.ok) throw new Error(`catalog fetch ${r.status}`);
    return r.json();
}

async function fetchRecipe(base: string, id: number): Promise<RecipePayload> {
    const r = await fetch(`${base}/${id}`, { credentials: "same-origin" });
    if (!r.ok) throw new Error(`recipe fetch ${r.status}`);
    return r.json();
}

async function saveRecipe(
    base: string,
    payload: RecipePayload,
    existingID: number,
): Promise<RecipePayload> {
    const url = existingID > 0 ? `${base}/${existingID}` : base;
    const r = await fetch(url, {
        method: "POST",
        credentials: "same-origin",
        headers: {
            "Content-Type": "application/json",
            "X-CSRF-Token": csrfToken(),
        },
        body: JSON.stringify(payload),
    });
    if (!r.ok) throw new Error(`save recipe ${r.status}: ${await r.text()}`);
    return r.json();
}

async function attachRecipe(base: string, recipeID: number): Promise<void> {
    const r = await fetch(base, {
        method: "POST",
        credentials: "same-origin",
        headers: {
            "Content-Type": "application/json",
            "X-CSRF-Token": csrfToken(),
        },
        body: JSON.stringify({ recipe_id: recipeID }),
    });
    if (!r.ok && r.status !== 204) throw new Error(`attach recipe ${r.status}`);
}

async function saveTemplateDraft(
    base: string,
    name: string,
    recipeID: number,
): Promise<void> {
    // The draft endpoint takes form-encoded values, matching every
    // other artifact-draft endpoint in the codebase.
    const form = new URLSearchParams();
    form.set("name", name);
    if (recipeID > 0) form.set("recipe_id", String(recipeID));
    const r = await fetch(base, {
        method: "POST",
        credentials: "same-origin",
        headers: {
            "Content-Type": "application/x-www-form-urlencoded",
            "X-CSRF-Token": csrfToken(),
        },
        body: form.toString(),
    });
    if (!r.ok) throw new Error(`template draft ${r.status}`);
}

// ---- Render --------------------------------------------------------------

interface Refs {
    slotsHost: HTMLElement;
    statsHost: HTMLElement;
    statSetSelect: HTMLSelectElement;
    statBudget: HTMLElement;
    talentsHost: HTMLElement;
    talentBudget: HTMLElement;
    summarySlots: HTMLElement;
    summaryRequired: HTMLElement;
    summaryStatPool: HTMLElement;
    summaryTalentPool: HTMLElement;
    validation: HTMLElement;
    bakeText: HTMLElement;
    animSelect: HTMLSelectElement;
    zoom: HTMLInputElement;
    canvas: HTMLCanvasElement;
    panes: Map<string, HTMLElement>;
    tabs: NodeListOf<HTMLButtonElement>;
}

function findRefs(host: HTMLElement): Refs {
    const grab = <T extends HTMLElement>(sel: string): T => {
        const el = host.querySelector(sel);
        if (!el) throw new Error(`character generator: missing ${sel}`);
        return el as T;
    };
    const panes = new Map<string, HTMLElement>();
    for (const el of host.querySelectorAll<HTMLElement>("[data-bx-character-pane]")) {
        const k = el.dataset.bxCharacterPane;
        if (k) panes.set(k, el);
    }
    return {
        slotsHost: grab<HTMLElement>("[data-bx-character-slots]"),
        statsHost: grab<HTMLElement>("[data-bx-character-stats]"),
        statSetSelect: grab<HTMLSelectElement>("[data-bx-character-stat-set]"),
        statBudget: grab<HTMLElement>("[data-bx-character-stat-budget]"),
        talentsHost: grab<HTMLElement>("[data-bx-character-talents]"),
        talentBudget: grab<HTMLElement>("[data-bx-character-talent-budget]"),
        summarySlots: grab<HTMLElement>("[data-bx-summary-slots]"),
        summaryRequired: grab<HTMLElement>("[data-bx-summary-required]"),
        summaryStatPool: grab<HTMLElement>("[data-bx-summary-stat-pool]"),
        summaryTalentPool: grab<HTMLElement>("[data-bx-summary-talent-pool]"),
        validation: grab<HTMLElement>("[data-bx-character-validation]"),
        bakeText: grab<HTMLElement>("[data-bx-character-bake-text]"),
        animSelect: grab<HTMLSelectElement>("[data-bx-character-anim]"),
        zoom: grab<HTMLInputElement>("[data-bx-character-zoom]"),
        canvas: grab<HTMLCanvasElement>("[data-bx-character-preview]"),
        panes,
        tabs: host.querySelectorAll<HTMLButtonElement>("[data-bx-character-tab]"),
    };
}

function renderSlots(host: HTMLElement, s: State, dispatch: (a: Action) => void): void {
    host.innerHTML = "";
    for (const slot of orderedSlots(s.catalog)) {
        const wrap = document.createElement("div");
        wrap.className = "bx-character-generator__slot";
        if (slot.required) wrap.classList.add("bx-character-generator__slot--required");
        const selected = s.selections.get(slot.key) ?? 0;
        if (selected !== 0) wrap.classList.add("bx-character-generator__slot--filled");

        const label = document.createElement("div");
        label.className = "bx-character-generator__slot-label";
        label.textContent = slot.label;
        wrap.appendChild(label);

        const row = document.createElement("div");
        row.className = "bx-character-generator__slot-row";

        // Thumbnail. Empty when nothing's selected; selected part's
        // sheet otherwise (frame 0).
        const thumb = document.createElement("div");
        thumb.className = "bx-character-generator__slot-thumb";
        const part = slot.parts.find((p) => p.id === selected);
        if (part?.asset_url) {
            thumb.style.backgroundImage = `url("${part.asset_url}")`;
            thumb.style.backgroundSize = "auto";
            thumb.style.backgroundRepeat = "no-repeat";
            thumb.style.backgroundPosition = "0 0";
        }
        row.appendChild(thumb);

        const select = document.createElement("select");
        select.className = "bx-input bx-character-generator__slot-select";
        const empty = document.createElement("option");
        empty.value = "";
        empty.textContent = slot.parts.length === 0 ? "(no parts registered)" : "(none)";
        select.appendChild(empty);
        for (const p of slot.parts) {
            const o = document.createElement("option");
            o.value = String(p.id);
            o.textContent = p.name;
            if (p.id === selected) o.selected = true;
            select.appendChild(o);
        }
        select.disabled = slot.parts.length === 0;
        select.addEventListener("change", () => {
            const v = parseInt(select.value, 10) || 0;
            if (v === 0) dispatch({ kind: "clear-slot", slotKey: slot.key });
            else dispatch({ kind: "select-part", slotKey: slot.key, partId: v });
        });
        row.appendChild(select);

        wrap.appendChild(row);
        host.appendChild(wrap);
    }
}

function renderSummary(refs: Refs, s: State): void {
    let total = 0, filled = 0, requiredMissing: string[] = [];
    for (const slot of s.catalog?.slots ?? []) {
        total++;
        if (s.selections.get(slot.key)) filled++;
        else if (slot.required) requiredMissing.push(slot.label);
    }
    refs.summarySlots.textContent = `${filled} / ${total}`;
    refs.summaryRequired.textContent = requiredMissing.length === 0 ? "none" : requiredMissing.join(", ");

    // Stat pool — point-buy remaining vs total. "—" when no point-buy set.
    const set = activeStatSet(s);
    if (set && set.creation_rules.method === "point_buy") {
        const remaining = pointBuyRemaining(s);
        refs.summaryStatPool.textContent = `${remaining} / ${set.creation_rules.pool ?? 0}`;
    } else {
        refs.summaryStatPool.textContent = "—";
    }

    // Talent pool — sum across trees in the recipe's currency_key,
    // assuming all trees share one currency (typical case).
    const stats = previewStatValues(s);
    const spend = talentSpend(s);
    const trees = s.catalog?.talent_trees ?? [];
    if (trees.length === 0) {
        refs.summaryTalentPool.textContent = "—";
    } else {
        const cur = trees[0]?.currency_key ?? "talent_points";
        const have = stats[cur] ?? 0;
        const used = spend[cur] ?? 0;
        refs.summaryTalentPool.textContent = `${have - used} / ${have}`;
    }

    refs.validation.innerHTML = "";
    if (s.validation.length === 0) {
        const ok = document.createElement("li");
        ok.className = "bx-character-generator__validation--ok";
        ok.textContent = "All checks passing.";
        refs.validation.appendChild(ok);
    } else {
        for (const v of s.validation) {
            const li = document.createElement("li");
            li.textContent = v.message;
            refs.validation.appendChild(li);
        }
    }
    refs.bakeText.textContent = s.bakeStatus;
}

function renderStatSetSelect(select: HTMLSelectElement, s: State): void {
    const sets = s.catalog?.stat_sets ?? [];
    select.innerHTML = "";
    const none = document.createElement("option");
    none.value = "0";
    none.textContent = sets.length === 0 ? "(no stat sets registered)" : "(none)";
    select.appendChild(none);
    for (const set of sets) {
        const o = document.createElement("option");
        o.value = String(set.id);
        o.textContent = set.name;
        if (set.id === s.statSetID) o.selected = true;
        select.appendChild(o);
    }
}

function renderStats(refs: Refs, s: State, dispatch: (a: Action) => void): void {
    refs.statsHost.innerHTML = "";
    refs.statBudget.textContent = "";
    const set = activeStatSet(s);
    if (!set) {
        const p = document.createElement("p");
        p.className = "bx-muted";
        p.textContent = "Pick a stat set above to allocate points.";
        refs.statsHost.appendChild(p);
        return;
    }
    // Sort by display_order, then key.
    const ordered = [...set.stats].sort((a, b) => {
        if (a.display_order !== b.display_order) return a.display_order - b.display_order;
        return a.key.localeCompare(b.key);
    });
    const previewVals = previewStatValues(s);
    for (const def of ordered) {
        const row = document.createElement("div");
        row.className = "bx-character-generator__stat-row";
        if (def.kind === "derived") row.classList.add("bx-character-generator__stat-row--derived");

        const label = document.createElement("div");
        label.className = "bx-character-generator__stat-label";
        label.textContent = def.label;
        row.appendChild(label);

        const value = document.createElement("div");
        value.className = "bx-character-generator__stat-value";
        if (def.kind === "core") {
            value.textContent = String(previewVals[def.key] ?? def.default);
        } else if (def.kind === "resource" || def.kind === "hidden") {
            value.textContent = String(def.default);
        } else {
            value.textContent = "—"; // derived; resolved server-side on bake
        }
        row.appendChild(value);

        if (def.kind === "core") {
            const minus = document.createElement("button");
            minus.type = "button";
            minus.className = "bx-character-generator__stat-step";
            minus.textContent = "−";
            const plus = document.createElement("button");
            plus.type = "button";
            plus.className = "bx-character-generator__stat-step";
            plus.textContent = "+";
            const remaining = pointBuyRemaining(s);
            const alloc = s.statAllocations.get(def.key) ?? 0;
            const final = def.default + alloc;
            const isPointBuy = set.creation_rules.method === "point_buy";
            // Disable plus when at max or no points left in point-buy.
            plus.disabled = (def.max !== 0 && final >= def.max) ||
                (isPointBuy && remaining < def.creation_cost);
            minus.disabled = final <= def.min;
            minus.addEventListener("click", () => dispatch({ kind: "set-stat-alloc", statKey: def.key, delta: -1 }));
            plus.addEventListener("click", () => dispatch({ kind: "set-stat-alloc", statKey: def.key, delta: +1 }));
            row.appendChild(minus);
            row.appendChild(plus);
        } else {
            // Two empty cells so the grid stays aligned.
            row.appendChild(document.createElement("div"));
            row.appendChild(document.createElement("div"));
        }
        refs.statsHost.appendChild(row);
    }
    if (set.creation_rules.method === "point_buy") {
        const remaining = pointBuyRemaining(s);
        const pool = set.creation_rules.pool ?? 0;
        refs.statBudget.textContent = `Pool ${pool}, ${pool - remaining} spent, ${remaining} remaining.`;
    } else if (set.creation_rules.method === "freeform") {
        refs.statBudget.textContent = "Freeform: no budget; respect each stat's min/max.";
    } else {
        refs.statBudget.textContent = "Fixed: stat values come from defaults.";
    }
}

function renderTalents(refs: Refs, s: State, dispatch: (a: Action) => void): void {
    refs.talentsHost.innerHTML = "";
    refs.talentBudget.textContent = "";
    const trees = s.catalog?.talent_trees ?? [];
    if (trees.length === 0) {
        const p = document.createElement("p");
        p.className = "bx-muted";
        p.textContent = "No talent trees registered yet.";
        refs.talentsHost.appendChild(p);
        return;
    }
    const stats = previewStatValues(s);
    const spend = talentSpend(s);
    for (const tree of trees) {
        refs.talentsHost.appendChild(renderTalentTree(tree, s, dispatch));
    }
    // Top-level budget summary (first tree's currency).
    const firstTree = trees[0];
    if (firstTree) {
        const cur = firstTree.currency_key;
        const have = stats[cur] ?? 0;
        const used = spend[cur] ?? 0;
        refs.talentBudget.textContent = `${used} / ${have} ${cur} spent`;
    }
}

function renderTalentTree(tree: CatalogTalentTree, s: State, dispatch: (a: Action) => void): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "bx-character-generator__talent-tree";
    const name = document.createElement("div");
    name.className = "bx-character-generator__talent-tree-name";
    name.textContent = tree.name;
    wrap.appendChild(name);

    // Rank lookup (within this tree only) for prereq checks.
    const ranks: Record<string, number> = {};
    for (const node of tree.nodes) {
        const r = s.talentPicks.get(`${tree.key}.${node.key}`) ?? 0;
        if (r > 0) ranks[node.key] = r;
    }

    for (const node of tree.nodes) {
        const row = document.createElement("div");
        row.className = "bx-character-generator__talent-node";
        const cur = ranks[node.key] ?? 0;
        if (cur > 0) row.classList.add("bx-character-generator__talent-node--ranked");
        // Blocked by missing prereqs.
        const blocked = node.prerequisites.some((p) => (ranks[p.node_key] ?? 0) < p.min_rank);
        if (blocked && cur === 0) row.classList.add("bx-character-generator__talent-node--blocked");

        const lbl = document.createElement("div");
        lbl.className = "bx-character-generator__talent-node-name";
        lbl.textContent = node.name;
        row.appendChild(lbl);

        const value = document.createElement("div");
        value.className = "bx-character-generator__stat-value";
        value.textContent = `${cur} / ${node.max_rank}`;
        row.appendChild(value);

        const minus = document.createElement("button");
        minus.type = "button";
        minus.className = "bx-character-generator__stat-step";
        minus.textContent = "−";
        minus.disabled = cur <= 0;
        minus.addEventListener("click", () => dispatch({
            kind: "set-talent-rank", treeKey: tree.key, nodeKey: node.key, rank: cur - 1,
        }));

        const plus = document.createElement("button");
        plus.type = "button";
        plus.className = "bx-character-generator__stat-step";
        plus.textContent = "+";
        plus.disabled = cur >= node.max_rank || (cur === 0 && blocked);
        plus.addEventListener("click", () => dispatch({
            kind: "set-talent-rank", treeKey: tree.key, nodeKey: node.key, rank: cur + 1,
        }));

        row.appendChild(minus);
        row.appendChild(plus);

        if (node.description) {
            const desc = document.createElement("div");
            desc.className = "bx-character-generator__talent-node-desc";
            desc.textContent = node.description;
            row.appendChild(desc);
        }
        wrap.appendChild(row);
    }
    return wrap;
}

function showPane(refs: Refs, key: string): void {
    for (const [k, el] of refs.panes) el.hidden = k !== key;
    for (const tab of refs.tabs) {
        tab.setAttribute("aria-selected", tab.dataset.bxCharacterTab === key ? "true" : "false");
    }
}

function renderAnimSelect(select: HTMLSelectElement, s: State): void {
    const anims = commonAnimations(s);
    const want = anims.length === 0 ? ["idle"] : anims;
    // Avoid clobbering the selection if nothing changed.
    const existing = Array.from(select.options).map((o) => o.value);
    if (existing.join(",") === want.join(",")) return;
    select.innerHTML = "";
    for (const a of want) {
        const o = document.createElement("option");
        o.value = a;
        o.textContent = a;
        if (a === s.animation) o.selected = true;
        select.appendChild(o);
    }
}

// ---- Boot ----------------------------------------------------------------

function boot(): void {
    const host = document.querySelector<HTMLElement>("[data-bx-character-generator]");
    if (!host) return;
    const cfg = readBootConfig(host);
    const refs = findRefs(host);

    const preview = new LayerPreview(refs.canvas);
    preview.setZoom(parseInt(refs.zoom.value, 10) || 4);

    let state: State = initialState();
    state.recipeName = cfg.templateName;
    let recipeID = cfg.recipeID;

    const dispatch = (a: Action): void => {
        state = reduce(state, a);
        renderSlots(refs.slotsHost, state, dispatch);
        renderStatSetSelect(refs.statSetSelect, state);
        renderStats(refs, state, dispatch);
        renderTalents(refs, state, dispatch);
        renderSummary(refs, state);
        renderAnimSelect(refs.animSelect, state);
        preview.setLayers(layered(state));
        preview.setAnimation(state.animation);
    };

    // Wire static event handlers that don't depend on state.
    refs.zoom.addEventListener("input", () => {
        preview.setZoom(parseInt(refs.zoom.value, 10) || 4);
    });
    refs.animSelect.addEventListener("change", () => {
        dispatch({ kind: "set-animation", animation: refs.animSelect.value });
    });
    refs.statSetSelect.addEventListener("change", () => {
        dispatch({ kind: "select-stat-set", setID: parseInt(refs.statSetSelect.value, 10) || 0 });
    });
    for (const tab of refs.tabs) {
        tab.addEventListener("click", () => {
            const k = tab.dataset.bxCharacterTab;
            if (k) showPane(refs, k);
        });
    }
    host.querySelector("[data-bx-character-randomize]")?.addEventListener("click", () => {
        dispatch({ kind: "randomize", seed: Date.now() & 0xffffffff });
    });
    host.querySelector("[data-bx-character-reset]")?.addEventListener("click", () => {
        dispatch({ kind: "reset" });
    });
    host.querySelector("[data-bx-character-copy-json]")?.addEventListener("click", () => {
        const payload = toRecipePayload(state, recipeID > 0 ? recipeID : null);
        void navigator.clipboard?.writeText(JSON.stringify(payload, null, 2));
    });
    host.querySelector("[data-bx-character-save]")?.addEventListener("click", () => {
        void doSave();
    });

    async function doSave(): Promise<void> {
        if (state.validation.some((v) => v.kind === "error")) {
            // Don't save invalid recipes; the validation panel already
            // tells the designer what's wrong.
            return;
        }
        dispatch({ kind: "set-bake-status", status: "saving…" });
        try {
            const payload = toRecipePayload(state, recipeID > 0 ? recipeID : null);
            const saved = await saveRecipe(cfg.recipeBase, payload, recipeID);
            if (saved.id && recipeID === 0) {
                recipeID = saved.id;
                await attachRecipe(cfg.attachBase, recipeID);
            }
            // Stage a draft on the npc_template so the next publish
            // bakes the new recipe.
            await saveTemplateDraft(cfg.templateDraftBase, state.recipeName, recipeID);
            dispatch({ kind: "set-bake-status", status: "saved (draft) — publish to bake" });
        } catch (err) {
            console.error(err);
            dispatch({ kind: "set-bake-status", status: "save failed: " + (err as Error).message });
        }
    }

    // Kick off async loads.
    void (async () => {
        try {
            const catalog = await fetchCatalog(cfg.catalogURL);
            dispatch({ kind: "set-catalog", catalog });
            if (cfg.recipeID > 0) {
                const recipe = await fetchRecipe(cfg.recipeBase, cfg.recipeID);
                dispatch({ kind: "load-recipe", recipe });
            }
        } catch (err) {
            console.error("character generator boot:", err);
        }
    })();

    preview.start();
}

if (typeof document !== "undefined") {
    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", boot, { once: true });
    } else {
        boot();
    }
}
