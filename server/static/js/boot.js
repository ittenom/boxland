// Boxland — minimal global boot shim.
//
// The real per-surface dispatch lives in /web/src/boot.ts (compiled into
// /static/web/...) and the shared command bus comes in PLAN.md tasks
// #37–38. This file ships now (task #36) so global focus + Esc behavior
// is consistent from the very first design-tool page.
(() => {
  "use strict";

  // 1. Esc closes the topmost dismissible overlay.
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    if (isTextEditingTarget(e.target)) return;

    const dismissible = document.querySelector(
      ".bx-modal-backdrop, .bx-cmdk, [data-bx-dismissible]"
    );
    if (!dismissible) return;
    e.preventDefault();
    dismissible.dispatchEvent(new CustomEvent("bx:dismiss", { bubbles: true }));
    if (dismissible.parentElement) dismissible.remove();
  });

  // 2. Mark the active nav link with aria-current="page". Re-runs on HTMX
  //    swaps so client-side nav stays in sync.
  const markActive = () => {
    const path = window.location.pathname.replace(/\/+$/, "/");
    document.querySelectorAll(".bx-topnav a[href]").forEach((a) => {
      const href = a.getAttribute("href");
      if (!href) return;
      if (path === href || path.startsWith(href + "/")) {
        a.setAttribute("aria-current", "page");
      } else {
        a.removeAttribute("aria-current");
      }
    });
  };
  markActive();
  document.body.addEventListener("htmx:afterSwap", markActive);

  // 3. Wire HTMX CSRF: copy the meta token onto every request as the
  //    X-CSRF-Token header so server-side csrf middleware passes.
  //    Plain (non-HTMX) <form method="post"> submissions can't set
  //    headers, so they ship the token as a hidden `csrf_token` field
  //    instead — see views.CSRFInput().
  document.body.addEventListener("htmx:configRequest", (e) => {
    const token = document
      .querySelector('meta[name="csrf-token"]')
      ?.getAttribute("content");
    if (!token) return;
    e.detail.headers["X-CSRF-Token"] = token;
  });

  // 4. data-bx-action shortcuts: declarative buttons that don't need a
  //    bespoke handler, just a target URL+selector. Ergonomic enough for
  //    one-off "open this modal" buttons in Templ.
  document.body.addEventListener("click", (e) => {
    const t = e.target instanceof HTMLElement ? e.target.closest("[data-bx-action]") : null;
    if (!t) return;
    const action = t.getAttribute("data-bx-action");
    switch (action) {
      case "open-upload":
        // HTMX-friendly: fetch the upload modal HTML and swap it into #modal-host.
        if (window.htmx) {
          window.htmx.ajax("GET", "/design/assets/upload", { target: "#modal-host", swap: "innerHTML" });
        }
        break;
    }
  });

  // 5. Ref picker: when a card inside the picker modal is clicked,
  //    write its id into the calling form's hidden input + label, then
  //    close the modal. The picker modal carries the selectors
  //    (data-bx-target-id, data-bx-target-label) on every card so this
  //    handler stays a one-liner regardless of which form opened it.
  document.body.addEventListener("click", (e) => {
    const card = e.target instanceof HTMLElement
      ? e.target.closest(".bx-picker-card")
      : null;
    if (!card) return;
    e.preventDefault();
    const id    = card.getAttribute("data-bx-pick-id") || "";
    const label = card.getAttribute("data-bx-pick-label") || "";
    const tid   = card.getAttribute("data-bx-target-id");
    const tlbl  = card.getAttribute("data-bx-target-label");
    if (tid) {
      const input = document.getElementById(tid);
      if (input instanceof HTMLInputElement) {
        input.value = id;
        input.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
    if (tlbl) {
      const lbl = document.getElementById(tlbl);
      if (lbl) {
        lbl.textContent = label ? `${label} · #${id}` : "none chosen";
        if (id) lbl.dataset.bxRefResolved = id;
      }
    }
    card.closest(".bx-modal-backdrop")?.remove();
  });

  // 5b. Ref picker "Clear" buttons reset the hidden input + label.
  document.body.addEventListener("click", (e) => {
    const btn = e.target instanceof HTMLElement
      ? e.target.closest("[data-bx-ref-clear]")
      : null;
    if (!btn) return;
    e.preventDefault();
    const inputId = btn.getAttribute("data-bx-ref-clear");
    if (!inputId) return;
    const input = document.getElementById(inputId);
    if (input instanceof HTMLInputElement) {
      input.value = "";
      input.dispatchEvent(new Event("change", { bubbles: true }));
    }
    const lbl = document.getElementById(inputId + "-label");
    if (lbl) lbl.textContent = "none chosen";
  });

  // 5c. Ref label resolver. On initial load + after every HTMX swap,
  //     find every refField with a non-zero id, batch-fetch the names
  //     from /design/picker/lookup, and replace "currently #N" with
  //     "name (#N)". Dramatically nicer for users opening forms with
  //     refs already set (e.g. an existing entity's sprite assignment).
  const resolveRefLabels = () => {
    const inputs = document.querySelectorAll("input[data-bx-ref]");
    const assetIDs = new Set();
    const entityIDs = new Set();
    /** @type {{el: HTMLElement, kind: string, id: string}[]} */
    const targets = [];
    for (const el of inputs) {
      if (!(el instanceof HTMLInputElement)) continue;
      const id = el.value.trim();
      if (!id || id === "0") continue;
      const kind = el.getAttribute("data-bx-ref") || "";
      const lbl = document.getElementById(el.id + "-label");
      if (!lbl) continue;
      // Skip if we already resolved (label has a non-default text).
      if (lbl.dataset.bxRefResolved === id) continue;
      targets.push({ el: lbl, kind, id });
      if (kind === "asset") assetIDs.add(id);
      else if (kind === "entity-type") entityIDs.add(id);
    }
    if (assetIDs.size === 0 && entityIDs.size === 0) return;

    const params = new URLSearchParams();
    if (assetIDs.size > 0)  params.set("asset",  [...assetIDs].join(","));
    if (entityIDs.size > 0) params.set("entity", [...entityIDs].join(","));

    fetch(`/design/picker/lookup?${params}`, { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (!data) return;
        for (const t of targets) {
          const bag = t.kind === "asset" ? data.asset : data.entity;
          const name = bag && bag[t.id];
          if (name) {
            t.el.textContent = `${name} · #${t.id}`;
            t.el.dataset.bxRefResolved = t.id;
          }
        }
      })
      .catch(() => { /* silent — fallback hint already rendered */ });
  };
  resolveRefLabels();
  document.body.addEventListener("htmx:afterSwap", resolveRefLabels);

  // 6. Tile-group editor: click to assign the active entity-type id to a
  //    cell. Each .bx-tilegroup-grid is paired with a hidden input
  //    [data-bx-layout-input] that the form serializes.
  document.body.addEventListener("click", (e) => {
    const cell = e.target instanceof HTMLElement
      ? e.target.closest(".bx-tilegroup-cell")
      : null;
    if (!cell) return;
    e.preventDefault();
    const grid = cell.closest("[data-bx-tilegroup-grid]");
    if (!grid) return;
    const activeInput = grid.parentElement?.querySelector("[data-bx-active-entity]");
    const layoutInput = grid.parentElement?.querySelector("[data-bx-layout-input]");
    if (!(activeInput instanceof HTMLInputElement) ||
        !(layoutInput instanceof HTMLInputElement)) return;

    const id = activeInput.value || "0";
    cell.setAttribute("data-id", id);
    cell.textContent = id === "0" ? "" : id;

    // Re-serialize the entire grid into the hidden form input.
    const cells = grid.querySelectorAll(".bx-tilegroup-cell");
    const rows = {};
    for (const c of cells) {
      const r = Number(c.getAttribute("data-r") || "0");
      const col = Number(c.getAttribute("data-c") || "0");
      const v = Number(c.getAttribute("data-id") || "0");
      if (!rows[r]) rows[r] = [];
      rows[r][col] = v;
    }
    const out = [];
    for (const k of Object.keys(rows).sort((a, b) => Number(a) - Number(b))) {
      out.push(rows[k]);
    }
    layoutInput.value = JSON.stringify(out);
  });

  // 7. Telemetry breadcrumb.
  console.info(
    "[boxland] boot.js loaded; surface=%s",
    document.body.dataset.surface || "unknown"
  );

  function isTextEditingTarget(t) {
    if (!t || !(t instanceof HTMLElement)) return false;
    const tag = t.tagName;
    return (
      tag === "INPUT" ||
      tag === "TEXTAREA" ||
      tag === "SELECT" ||
      t.isContentEditable
    );
  }
})();
