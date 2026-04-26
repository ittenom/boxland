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
import { CommandBus } from "@command-bus";
import { attachKeyboard } from "@input";

// ---- Boot config ---------------------------------------------------------

type Mode = "designer" | "player";

interface BootConfig {
    mode: Mode;
    templateID: number; // designer: NPC template id; player: player_character id (0 for new)
    templateName: string;
    recipeID: number; // 0 = no recipe yet
    catalogURL: string;
    recipeBase: string;        // designer: /design/characters/recipes; player: /play/characters
    attachBase: string;        // designer only — empty for player mode
    templateDraftBase: string; // designer only — empty for player mode
}

function readBootConfig(host: HTMLElement): BootConfig {
    const ds = host.dataset;
    const need = (k: string): string => {
        const v = ds[k];
        if (v === undefined) throw new Error(`character generator: missing data-bx-${camelToKebab(k)}`);
        return v;
    };
    const optional = (k: string): string => ds[k] ?? "";
    const mode = (ds.bxMode === "player" ? "player" : "designer") as Mode;
    return {
        mode,
        templateID: parseInt(need("templateId"), 10),
        templateName: need("templateName"),
        recipeID: parseInt(need("recipeId"), 10) || 0,
        catalogURL: need("catalogUrl"),
        recipeBase: need("recipeBase"),
        attachBase: optional("attachBase"),
        templateDraftBase: optional("templateDraftBase"),
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

// ---- Player-mode save (single POST) -------------------------------------

interface PlayerCharacterResponse {
    id: number;
    recipe_id: number;
    bake_id: number;
    bake_asset_id: number;
}

/** Player-mode save: one POST that does recipe + bake + link in one tx. */
async function savePlayerCharacter(
    base: string,
    state: State,
    charID: number,
    name: string,
): Promise<PlayerCharacterResponse> {
    // Build a payload shaped like the server's playerCharacterPayload.
    const slots: { slot_key: string; part_id: number }[] = [];
    for (const slot of state.catalog?.slots ?? []) {
        const pid = state.selections.get(slot.key);
        if (pid) slots.push({ slot_key: slot.key, part_id: pid });
    }
    const allocations: Record<string, number> = {};
    for (const [k, v] of state.statAllocations) {
        if (v !== 0) allocations[k] = v;
    }
    const picks: Record<string, number> = {};
    for (const [k, v] of state.talentPicks) {
        if (v > 0) picks[k] = v;
    }
    const payload = {
        name,
        appearance: { slots },
        stats: state.statSetID > 0 ? { set_id: state.statSetID, allocations } : { set_id: 0, allocations: {} },
        talents: { picks },
    };
    const url = charID > 0 ? `${base}/${charID}` : base;
    const r = await fetch(url, {
        method: "POST",
        credentials: "same-origin",
        headers: {
            "Content-Type": "application/json",
            "X-CSRF-Token": csrfToken(),
        },
        body: JSON.stringify(payload),
    });
    if (!r.ok) throw new Error(`save ${r.status}: ${await r.text()}`);
    return r.json();
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
    let templateID = cfg.templateID; // mutates on player-mode "create"

    // Player-mode name input (when present) lets the player rename
    // the character without leaving the editor. The designer mode
    // takes the name from the NPC template row instead, so this is a
    // no-op when the input is absent.
    const nameInput = host.querySelector<HTMLInputElement>("[data-bx-character-name]");
    if (nameInput) {
        nameInput.addEventListener("input", () => {
            state.recipeName = nameInput.value;
        });
    }

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
            // tells the user what's wrong.
            return;
        }
        dispatch({ kind: "set-bake-status", status: "saving…" });
        try {
            if (cfg.mode === "player") {
                if (!state.recipeName.trim()) {
                    dispatch({ kind: "set-bake-status", status: "name is required" });
                    return;
                }
                const saved = await savePlayerCharacter(cfg.recipeBase, state, templateID, state.recipeName);
                if (templateID === 0) {
                    templateID = saved.id;
                    // Future loads / re-saves should hit the edit URL.
                    history.replaceState(null, "", `/play/characters/${templateID}/edit`);
                }
                dispatch({ kind: "set-bake-status", status: "saved & baked" });
            } else {
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
            }
        } catch (err) {
            console.error(err);
            dispatch({ kind: "set-bake-status", status: "save failed: " + (err as Error).message });
        }
    }

    // ---- Hotkeys (per docs/hotkeys.md "Character Generator") ------------
    //
    // The CommandBus owns dispatch + suppresses commands while typing
    // (unless the command is whileTyping=true, which none of these are).
    // Bindings match the doc one-to-one.
    const bus = new CommandBus();
    const tabKey = (k: string): void => showPane(refs, k);

    bus.register({ id: "char.tab.look", description: "Switch to Look tab",
        category: "Character Generator", do: () => tabKey("look") });
    bus.register({ id: "char.tab.sheet", description: "Switch to Sheet tab",
        category: "Character Generator", do: () => tabKey("sheet") });
    bus.register({ id: "char.tab.talents", description: "Switch to Talents tab",
        category: "Character Generator", do: () => tabKey("talents") });
    bus.register({ id: "char.anim.prev", description: "Previous animation",
        category: "Character Generator", do: () => cycleAnim(-1) });
    bus.register({ id: "char.anim.next", description: "Next animation",
        category: "Character Generator", do: () => cycleAnim(+1) });
    bus.register({ id: "char.zoom.out", description: "Zoom out preview",
        category: "Character Generator", do: () => bumpZoom(-1) });
    bus.register({ id: "char.zoom.in", description: "Zoom in preview",
        category: "Character Generator", do: () => bumpZoom(+1) });
    bus.register({ id: "char.randomize", description: "Randomize selections",
        category: "Character Generator", do: () => dispatch({ kind: "randomize", seed: Date.now() & 0xffffffff }) });
    bus.register({ id: "char.save", description: "Save (designer: draft; player: finalize)",
        category: "Character Generator", do: () => { void doSave(); } });
    if (cfg.mode === "designer") {
        bus.register({ id: "char.reset", description: "Reset all selections",
            category: "Character Generator", do: () => dispatch({ kind: "reset" }) });
        bus.register({ id: "char.copy.json", description: "Copy recipe JSON",
            category: "Character Generator", do: () => {
                const payload = toRecipePayload(state, recipeID > 0 ? recipeID : null);
                void navigator.clipboard?.writeText(JSON.stringify(payload, null, 2));
            } });
    }

    bus.bindHotkey("1", "char.tab.look");
    bus.bindHotkey("2", "char.tab.sheet");
    bus.bindHotkey("3", "char.tab.talents");
    bus.bindHotkey("[", "char.anim.prev");
    bus.bindHotkey("]", "char.anim.next");
    bus.bindHotkey("=", "char.zoom.in");  // un-shifted "+" key on US layouts
    bus.bindHotkey("-", "char.zoom.out");
    bus.bindHotkey("R", "char.randomize");
    bus.bindHotkey("Mod+S", "char.save");
    if (cfg.mode === "designer") {
        bus.bindHotkey("Shift+R", "char.reset");
        bus.bindHotkey("Shift+C", "char.copy.json");
    }

    // attachKeyboard handles the "suppress while typing" rule for us.
    const detachKB = attachKeyboard(bus, window);
    // Surface teardown isn't critical for a single-page nav, but expose
    // the cleanup on the host element so future test code / HMR can use it.
    (host as unknown as { __charGenDetach?: () => void }).__charGenDetach = detachKB;

    function cycleAnim(direction: number): void {
        const opts = Array.from(refs.animSelect.options).map((o) => o.value);
        if (opts.length === 0) return;
        const i = Math.max(0, opts.indexOf(state.animation));
        const next = (i + direction + opts.length) % opts.length;
        const nextAnim = opts[next];
        if (nextAnim === undefined) return;
        refs.animSelect.value = nextAnim;
        dispatch({ kind: "set-animation", animation: nextAnim });
    }
    function bumpZoom(direction: number): void {
        const cur = parseInt(refs.zoom.value, 10) || 4;
        const min = parseInt(refs.zoom.min, 10) || 1;
        const max = parseInt(refs.zoom.max, 10) || 6;
        const next = Math.max(min, Math.min(max, cur + direction));
        refs.zoom.value = String(next);
        preview.setZoom(next);
    }

    // Kick off async loads.
    void (async () => {
        try {
            const catalog = await fetchCatalog(cfg.catalogURL);
            dispatch({ kind: "set-catalog", catalog });
            // Designer-mode: explicit recipe id from the data attribute.
            // Player-mode: editing an existing character means
            // templateID > 0 and the recipe lives behind
            // /play/characters/{id} (which returns the same shape).
            if (cfg.mode === "designer" && cfg.recipeID > 0) {
                const recipe = await fetchRecipe(cfg.recipeBase, cfg.recipeID);
                dispatch({ kind: "load-recipe", recipe });
            } else if (cfg.mode === "player" && templateID > 0) {
                const r = await fetch(`${cfg.recipeBase}/${templateID}`, { credentials: "same-origin" });
                if (r.ok) {
                    const recipe = await r.json() as RecipePayload;
                    dispatch({ kind: "load-recipe", recipe });
                    // Sync the name input if present.
                    if (nameInput && recipe.name) nameInput.value = recipe.name;
                }
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
