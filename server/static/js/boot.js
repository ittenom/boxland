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

  // 5. Tile-group editor: click to assign the active entity-type id to a
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

  // 6. Telemetry breadcrumb.
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
