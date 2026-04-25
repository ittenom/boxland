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

  // 5. Telemetry breadcrumb.
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
