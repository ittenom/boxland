// Boxland — minimal global boot shim.
//
// The real per-surface dispatch lives in /web/src/boot.ts (compiled into
// /static/web/...) and the shared command bus comes in PLAN.md tasks
// #37–38. This file ships now (task #36) so global focus + Esc behavior
// is consistent from the very first design-tool page.
(() => {
  "use strict";

  // 1. Esc closes the topmost dismissible overlay. We pick the LAST
  //    matching node in the DOM (which is also the visually-topmost
  //    one given the z-index stack) so a confirm dialog spawned over
  //    an existing detail modal dismisses the confirm first.
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    if (isTextEditingTarget(e.target)) return;

    const all = document.querySelectorAll(
      ".bx-modal-backdrop, .bx-cmdk, [data-bx-dismissible]"
    );
    if (all.length === 0) return;
    const dismissible = all[all.length - 1];
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

  // 4b. Picker Enter-to-pick: pressing Enter inside the picker search
  //     when exactly one result is visible synthesizes a click on that
  //     card. Saves a round-trip click on a workflow people run dozens
  //     of times an hour. Multi-result Enter is a no-op (no surprise
  //     auto-pick); zero-result Enter is also a no-op. Wired via the
  //     form's onsubmit so it works alongside the input's hx-trigger.
  window.bxPickerSubmit = function (form) {
    const modal = form.closest("[data-bx-picker-modal]");
    if (!modal) return;
    const cards = modal.querySelectorAll(".bx-picker-card");
    // Only count cards still in the grid (not e.g. hidden by CSS).
    /** @type {HTMLElement[]} */
    const visible = Array.from(cards).filter((c) => c instanceof HTMLElement);
    if (visible.length !== 1) return;
    visible[0].click();
  };

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

  // 7b. Multi-select for grids tagged with [data-bx-multi-select-grid].
  //     Driven by a [data-bx-multi-select-toggle] button in the page
  //     toolbar. When toggled on:
  //       - Card / row clicks toggle a selection class instead of
  //         opening their detail (we capture the click and stop it
  //         from reaching HTMX).
  //       - A floating action bar appears with kind-aware buttons
  //         (Delete / Promote-to-tile-entity for assets; Delete / +tile
  //         tag for entities).
  //     Toggling off (Cancel or the same toggle button) clears the
  //     selection and restores normal click-to-open behavior.
  (function () {
    /** @type {{grid: HTMLElement|null, kind: string, ids: Set<string>, toggle: HTMLElement|null, bar: HTMLElement|null}} */
    const ms = { grid: null, kind: "", ids: new Set(), toggle: null, bar: null };

    document.body.addEventListener("click", (e) => {
      const t = e.target instanceof HTMLElement
        ? e.target.closest("[data-bx-multi-select-toggle]")
        : null;
      if (!t) return;
      e.preventDefault();
      const targetSel = t.getAttribute("data-bx-multi-select-target") || "";
      const grid = targetSel ? document.querySelector(targetSel) : null;
      if (!grid) return;
      if (ms.grid === grid) {
        msExit();
      } else {
        msExit();
        msEnter(grid, t);
      }
    });

    // Capture-phase click handler so we run BEFORE HTMX's bubble-phase
    // listener and can stop card clicks from opening the detail modal
    // when select mode is on.
    document.addEventListener("click", (e) => {
      if (!ms.grid) return;
      const target = e.target instanceof HTMLElement ? e.target : null;
      if (!target || !ms.grid.contains(target)) return;
      // Ignore clicks on action-bar controls themselves.
      if (target.closest("[data-bx-multi-select-bar]")) return;
      const card = target.closest("[data-bx-multi-select-id]");
      if (!card) return;
      e.preventDefault();
      e.stopPropagation();
      const id = card.getAttribute("data-bx-multi-select-id") || "";
      if (!id) return;
      if (ms.ids.has(id)) {
        ms.ids.delete(id);
        card.classList.remove("is-selected");
        card.setAttribute("aria-selected", "false");
      } else {
        ms.ids.add(id);
        card.classList.add("is-selected");
        card.setAttribute("aria-selected", "true");
      }
      msRefreshBar();
    }, true);

    // After every HTMX swap, if we were in select mode and the grid
    // just rerendered, drop the selection (ids may no longer exist).
    document.body.addEventListener("htmx:afterSwap", () => {
      if (!ms.grid) return;
      if (!document.body.contains(ms.grid)) {
        msExit();
      } else {
        ms.ids.clear();
        msRefreshBar();
      }
    });

    function msEnter(grid, toggle) {
      ms.grid   = grid;
      ms.kind   = grid.getAttribute("data-bx-multi-select-kind") || "";
      ms.toggle = toggle;
      ms.ids.clear();
      grid.classList.add("is-multi-select");
      if (toggle) {
        toggle.setAttribute("aria-pressed", "true");
        toggle.textContent = "Cancel";
      }
      msRefreshBar();
    }

    function msExit() {
      if (ms.grid) ms.grid.classList.remove("is-multi-select");
      if (ms.toggle) {
        ms.toggle.setAttribute("aria-pressed", "false");
        ms.toggle.textContent = "Select";
      }
      if (ms.bar && ms.bar.parentElement) ms.bar.remove();
      // Clean any lingering is-selected marks.
      document.querySelectorAll("[data-bx-multi-select-id].is-selected").forEach((el) => {
        el.classList.remove("is-selected");
        el.setAttribute("aria-selected", "false");
      });
      ms.grid = null; ms.kind = ""; ms.toggle = null; ms.bar = null;
      ms.ids.clear();
    }

    function msRefreshBar() {
      if (!ms.grid) return;
      const count = ms.ids.size;
      if (count === 0) {
        if (ms.bar && ms.bar.parentElement) ms.bar.remove();
        ms.bar = null;
        return;
      }
      if (!ms.bar) {
        ms.bar = document.createElement("div");
        ms.bar.className = "bx-multi-select-bar";
        ms.bar.setAttribute("data-bx-multi-select-bar", "");
        document.body.appendChild(ms.bar);
      }
      const ids = [...ms.ids].join(",");
      const csrf = document.querySelector('meta[name="csrf-token"]')?.getAttribute("content") || "";
      ms.bar.innerHTML = `
        <span class="bx-mono bx-small">${count} selected</span>
        <span style="flex:1"></span>
        ${msKindActions(ms.kind, ids)}
        <button type="button" class="bx-btn bx-btn--ghost bx-btn--small" data-bx-multi-select-cancel>Cancel</button>`;
      ms.bar.querySelectorAll("[data-bx-multi-select-action]").forEach((btn) => {
        btn.addEventListener("click", () => msAction(btn, ids, csrf));
      });
      ms.bar.querySelector("[data-bx-multi-select-cancel]")?.addEventListener("click", msExit);
    }

    function msKindActions(kind, ids) {
      if (kind === "asset") {
        return `
          <button type="button" class="bx-btn bx-btn--small" data-bx-multi-select-action="promote"
            data-url="/design/assets/promote-bulk?ids=${ids}"
            data-confirm="Promote ${count(ids)} asset(s) into tile entities?"
            title="Create a tile-tagged entity for each selected asset">+ Tile entity</button>
          <button type="button" class="bx-btn bx-btn--danger bx-btn--small" data-bx-multi-select-action="delete"
            data-url="/design/assets/delete-bulk"
            data-target="#assets-grid"
            data-confirm="Delete ${count(ids)} asset(s)? Variants and animation tags go with each. Entities using them will lose their sprite.">Delete</button>`;
      }
      if (kind === "entity") {
        return `
          <button type="button" class="bx-btn bx-btn--small" data-bx-multi-select-action="tag-tile"
            data-url="/design/entities/tag-bulk?ids=${ids}&tag=tile&op=add"
            data-target="#entities-grid">+ tile tag</button>
          <button type="button" class="bx-btn bx-btn--danger bx-btn--small" data-bx-multi-select-action="delete"
            data-url="/design/entities/delete-bulk"
            data-target="#entities-grid"
            data-confirm="Delete ${count(ids)} entity type(s)? Components, automations, and tile-edge assignments go with each.">Delete</button>`;
      }
      return "";
    }

    function count(ids) { return ids ? ids.split(",").length : 0; }

    function msAction(btn, ids, csrf) {
      const url = btn.getAttribute("data-url") || "";
      const target = btn.getAttribute("data-target") || "";
      const confirmMsg = btn.getAttribute("data-confirm") || "";
      const action = btn.getAttribute("data-bx-multi-select-action") || "";
      const proceed = () => {
        // For "promote" the URL already carries ?ids=…; for delete +
        // tag-tile we ship a form body so the server can read it via
        // ParseForm. Both shapes are accepted by parseCommaIDs +
        // firstNonEmpty.
        const fd = new FormData();
        fd.set("ids", ids);
        const xhr = new XMLHttpRequest();
        xhr.open("POST", url, true);
        xhr.setRequestHeader("X-CSRF-Token", csrf);
        xhr.setRequestHeader("Accept", "text/html");
        xhr.onload = () => {
          if (xhr.status >= 200 && xhr.status < 300 && target) {
            const node = document.querySelector(target);
            if (node) node.outerHTML = xhr.responseText;
          }
          msExit();
        };
        xhr.send(fd);
        // For "promote" we want the upload-result fragment to swap into
        // the modal-host, not the assets grid. Override target.
        if (action === "promote") {
          const host = document.getElementById("modal-host");
          if (host) {
            xhr.onload = () => {
              if (xhr.status >= 200 && xhr.status < 300) {
                // Re-render upload-result-style fragment in a quick modal.
                host.innerHTML = `
                  <div class="bx-modal-backdrop" data-bx-dismissible role="dialog" aria-modal="true">
                    <div class="bx-modal" style="width: min(560px, 95vw);">
                      <header class="bx-modal__header">
                        <h2 style="margin:0; font-size: 14px;">Promotion result</h2>
                        <button type="button" class="bx-btn bx-btn--ghost bx-btn--small"
                                hx-on:click="this.closest('.bx-modal-backdrop').remove()" aria-label="Close">Esc</button>
                      </header>
                      <div class="bx-modal__body">${xhr.responseText}</div>
                    </div>
                  </div>`;
              }
              msExit();
            };
          }
        }
      };
      if (confirmMsg) {
        showBxConfirm(confirmMsg, btn).then((ok) => { if (ok) proceed(); });
      } else {
        proceed();
      }
    }
  })();

  // 7c. Quiet repeat draft-saved toasts. The first verbose toast in a
  //     persisted session shows the "Push to Live" affordance; after
  //     that the chrome's draft-count pill is the persistent surface
  //     so the toast shrinks to a no-nag "Draft saved.". A 6s grace
  //     window after the first verbose toast keeps it visible long
  //     enough to read before the localStorage flag flips.
  document.body.addEventListener("htmx:afterSwap", () => {
    const toasts = document.querySelectorAll("[data-bx-draft-toast]:not([data-bx-draft-toast-handled])");
    if (toasts.length === 0) return;
    let alreadySeen = false;
    try { alreadySeen = localStorage.getItem("bx_draft_toast_seen") === "1"; } catch (_) {}
    toasts.forEach((toast) => {
      toast.setAttribute("data-bx-draft-toast-handled", "1");
      const verbose = toast.querySelector("[data-bx-draft-toast-verbose]");
      const short   = toast.querySelector("[data-bx-draft-toast-short]");
      if (alreadySeen && verbose && short) {
        verbose.hidden = true;
        short.hidden = false;
      }
    });
    if (!alreadySeen) {
      // Wait long enough that the user can scan the verbose copy at
      // least once, then mark seen so the next save in any artifact
      // editor renders the short version.
      setTimeout(() => {
        try { localStorage.setItem("bx_draft_toast_seen", "1"); } catch (_) {}
      }, 6000);
    }
  });

  // 8. Replace HTMX's native `confirm()` with our themed modal so the
  //    brand-aligned design language carries into destructive actions
  //    and the long count-aware confirm strings (asset/entity/socket
  //    delete) wrap legibly. Falls back to native confirm() if HTMX
  //    or the modal layer ever misbehaves.
  document.body.addEventListener("htmx:confirm", (e) => {
    const msg = e.detail.question;
    if (!msg) return; // no hx-confirm on this element; let HTMX proceed.
    e.preventDefault(); // we'll re-issue the request after the user decides.
    showBxConfirm(msg, e.detail.elt).then((ok) => {
      if (ok) e.detail.issueRequest(true);
    });
  });

  // 9. Tile atlas previews. CSS background clipping is fragile across
  //    same-origin asset proxies and HTMX swaps; draw the preview cells
  //    with Canvas2D so Asset Manager matches Mapmaker's renderer.
  function drawTilePreviews(root) {
    const host = root && root.querySelectorAll ? root : document;
    host.querySelectorAll("canvas[data-bx-tile-preview]").forEach((canvas) => {
      if (!(canvas instanceof HTMLCanvasElement)) return;
      if (canvas.dataset.bxTilePreviewDrawn === "1") return;
      const url = canvas.getAttribute("data-bx-tile-preview") || "";
      if (!url) return;
      const cols = Math.max(1, Number(canvas.dataset.cols || "1"));
      const rows = Math.max(1, Number(canvas.dataset.rows || "1"));
      const tileSize = Math.max(1, Number(canvas.dataset.tileSize || "32"));
      const maxCols = Math.max(1, Number(canvas.dataset.maxCols || cols));
      const maxRows = Math.max(1, Number(canvas.dataset.maxRows || rows));
      const visibleCols = Math.min(cols, maxCols);
      const visibleRows = Math.min(rows, maxRows);
      const cellPx = 32;
      canvas.width = visibleCols * cellPx;
      canvas.height = visibleRows * cellPx;
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.imageSmoothingEnabled = false;
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      drawChecker(ctx, canvas.width, canvas.height);
      drawTileGrid(ctx, visibleCols, visibleRows, cellPx);

      const nonEmpty = new Set((canvas.dataset.nonEmpty || "")
        .split(",")
        .map((x) => Number(x.trim()))
        .filter((x) => Number.isFinite(x)));
      const img = new Image();
      img.onload = () => {
        drawChecker(ctx, canvas.width, canvas.height);
        for (let r = 0; r < visibleRows; r++) {
          for (let c = 0; c < visibleCols; c++) {
            const idx = r * cols + c;
            if (nonEmpty.size > 0 && !nonEmpty.has(idx)) continue;
            ctx.drawImage(img, c * tileSize, r * tileSize, tileSize, tileSize, c * cellPx, r * cellPx, cellPx, cellPx);
          }
        }
        drawTileGrid(ctx, visibleCols, visibleRows, cellPx);
        canvas.dataset.bxTilePreviewDrawn = "1";
      };
      img.onerror = () => {
        canvas.dataset.bxTilePreviewDrawn = "error";
      };
      img.src = url;
    });
  }
  function drawChecker(ctx, w, h) {
    const s = 8;
    ctx.fillStyle = "#08213a";
    ctx.fillRect(0, 0, w, h);
    ctx.fillStyle = "#0a2a49";
    for (let y = 0; y < h; y += s) {
      for (let x = 0; x < w; x += s) {
        if (((x / s) + (y / s)) % 2 === 0) ctx.fillRect(x, y, s, s);
      }
    }
  }
  function drawTileGrid(ctx, cols, rows, cellPx) {
    ctx.strokeStyle = "rgba(144, 213, 255, 0.16)";
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let x = 0; x <= cols; x++) {
      ctx.moveTo(x * cellPx + 0.5, 0);
      ctx.lineTo(x * cellPx + 0.5, rows * cellPx);
    }
    for (let y = 0; y <= rows; y++) {
      ctx.moveTo(0, y * cellPx + 0.5);
      ctx.lineTo(cols * cellPx, y * cellPx + 0.5);
    }
    ctx.stroke();
  }
  drawTilePreviews(document);
  document.body.addEventListener("htmx:afterSwap", (e) => drawTilePreviews(e.target || document));

  // 10. Telemetry breadcrumb.
  console.info(
    "[boxland] boot.js loaded; surface=%s",
    document.body.dataset.surface || "unknown"
  );

  /**
   * Show a themed confirm dialog. Returns a Promise<boolean>.
   * The "Confirm" button focus is delayed so an in-flight Enter keypress
   * (which is how a lot of users dismiss the previous form) can't
   * accidentally accept the dialog.
   */
  function showBxConfirm(message, sourceEl) {
    return new Promise((resolve) => {
      const dangerous = isDangerousAction(sourceEl);
      const backdrop = document.createElement("div");
      backdrop.className = "bx-modal-backdrop bx-modal-backdrop--confirm";
      backdrop.setAttribute("data-bx-dismissible", "");
      backdrop.setAttribute("role", "dialog");
      backdrop.setAttribute("aria-modal", "true");
      backdrop.innerHTML = `
        <div class="bx-modal bx-modal--confirm" style="width: min(440px, 92vw);">
          <header class="bx-modal__header">
            <h2 style="margin:0; font-size: 14px;">${dangerous ? "Confirm deletion" : "Are you sure?"}</h2>
          </header>
          <div class="bx-modal__body">
            <p style="margin:0; line-height: 1.45;"></p>
          </div>
          <footer class="bx-modal__footer" style="gap: var(--bx-s2); justify-content: flex-end;">
            <button type="button" class="bx-btn bx-btn--ghost" data-bx-confirm-cancel>Cancel</button>
            <button type="button" class="${dangerous ? "bx-btn bx-btn--danger" : "bx-btn bx-btn--primary"}" data-bx-confirm-ok>${dangerous ? "Delete" : "Confirm"}</button>
          </footer>
        </div>`;
      // Use textContent so user-supplied counts can't inject HTML.
      backdrop.querySelector("p").textContent = message;
      document.body.appendChild(backdrop);

      const cancel = () => { cleanup(); resolve(false); };
      const ok     = () => { cleanup(); resolve(true);  };
      const cleanup = () => {
        backdrop.removeEventListener("bx:dismiss", cancel);
        backdrop.remove();
      };

      backdrop.querySelector("[data-bx-confirm-cancel]").addEventListener("click", cancel);
      backdrop.querySelector("[data-bx-confirm-ok]").addEventListener("click", ok);
      backdrop.addEventListener("bx:dismiss", cancel);
      // Click on the dim backdrop (outside the modal) cancels.
      backdrop.addEventListener("click", (ev) => {
        if (ev.target === backdrop) cancel();
      });

      // 250ms delay before focusing Confirm so a stray Enter can't
      // auto-accept. Cancel keeps focus first so Esc/Enter both
      // resolve to "no" until the user has time to read the message.
      backdrop.querySelector("[data-bx-confirm-cancel]").focus();
      setTimeout(() => {
        const okBtn = backdrop.querySelector("[data-bx-confirm-ok]");
        if (okBtn && document.body.contains(backdrop)) okBtn.focus();
      }, 250);
    });
  }

  /**
   * Returns true when the source element looks like a destructive action
   * (DELETE verb or .bx-btn--danger class). Drives the modal's red
   * Confirm button so the affordance matches the action.
   */
  function isDangerousAction(el) {
    if (!(el instanceof HTMLElement)) return false;
    if (el.hasAttribute("hx-delete")) return true;
    if (el.classList.contains("bx-btn--danger")) return true;
    return false;
  }

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
