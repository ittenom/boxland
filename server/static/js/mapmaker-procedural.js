// Boxland — Mapmaker procedural-mode controls.
//
// Loaded by views/mapmaker.templ when the map's mode == "procedural".
// One algorithm, one button. The engine derives its sample from
// painted tiles automatically; the Sample tool (toolbar S) lets the
// designer override that with a specific rectangle drawn on the
// canvas. No algorithm picker, no numeric coords, no constraint UI —
// configuration is the painted map itself.
//
// Events:
//   * Outbound  bx:procedural-preview     { region, seed }
//                bx:procedural-preview-clear
//                bx:procedural-sample-set   { x, y, width, height }
//                bx:procedural-sample-clear
//   * Inbound   bx:procedural-sample-drawn { x, y, width, height }   (from canvas Sample tool)
//                bx:locks-changed           { count }                 (from mapmaker.js Lock tool)
(() => {
  "use strict";

  function init() {
    const panel = document.querySelector("[data-bx-procedural]");
    if (!panel) return;
    const mapId = panel.getAttribute("data-map-id");
    const seedInput      = panel.querySelector("[data-bx-proc-seed]");
    const lockedInput    = panel.querySelector("[data-bx-proc-locked]");
    const generateBtn    = panel.querySelector("[data-bx-proc-generate]");
    const rerollBtn      = panel.querySelector("[data-bx-proc-reroll]");
    const clearBtn       = panel.querySelector("[data-bx-proc-clear]");
    const commitBtn      = panel.querySelector("[data-bx-proc-commit]");
    const status         = panel.querySelector("[data-bx-proc-status]");
    const errorEl        = panel.querySelector("[data-bx-proc-error]");
    const sampleLabel    = panel.querySelector("[data-bx-sample-label]");
    const sampleClearBtn = panel.querySelector("[data-bx-sample-clear]");
    const lockedCountEl  = panel.querySelector("[data-bx-locked-count]");
    const locksClearBtn  = panel.querySelector("[data-bx-locks-clear]");
    const canvas         = document.querySelector("[data-bx-mapmaker-canvas]");

    if (!seedInput || !generateBtn) return;

    function csrfToken() {
      const m = document.querySelector('meta[name="csrf-token"]');
      return (m && m.getAttribute("content")) || "";
    }
    function showError(msg) {
      if (!errorEl) return;
      errorEl.textContent = msg || "";
      errorEl.style.display = msg ? "block" : "none";
    }
    function showStatus(msg) {
      if (status) status.textContent = msg || "";
    }
    function currentSeed() {
      const v = Number(seedInput.value);
      if (!Number.isFinite(v) || v < 0) return 0;
      return Math.floor(v);
    }
    function pickRandomSeed() {
      const buf = new Uint32Array(2);
      (window.crypto || window.msCrypto).getRandomValues(buf);
      return buf[0] * 0x10000 + (buf[1] & 0xfffff);
    }

    // ---- Sample patch state ----
    //
    // The panel only displays; the canvas Sample tool drives changes.
    // We load the persisted patch on init so a reload still shows the
    // yellow frame around the saved area.

    function setSampleLabel(patch) {
      if (!sampleLabel) return;
      if (!patch) {
        sampleLabel.textContent = "No sample — using painted tiles, or sockets if blank.";
        if (sampleClearBtn) sampleClearBtn.style.display = "none";
      } else {
        sampleLabel.textContent = `Sample: ${patch.width}×${patch.height} at (${patch.x}, ${patch.y}).`;
        if (sampleClearBtn) sampleClearBtn.style.display = "";
      }
    }

    function dispatchSampleSet(patch) {
      if (!canvas) return;
      canvas.dispatchEvent(new CustomEvent("bx:procedural-sample-set", {
        bubbles: true,
        detail: { x: patch.x, y: patch.y, width: patch.width, height: patch.height },
      }));
    }
    function dispatchSampleClear() {
      if (!canvas) return;
      canvas.dispatchEvent(new CustomEvent("bx:procedural-sample-clear", { bubbles: true }));
    }

    async function loadSamplePatch() {
      try {
        const resp = await fetch(`/design/maps/${mapId}/sample-patch`);
        if (resp.status === 204) {
          setSampleLabel(null);
          dispatchSampleClear();
          return;
        }
        if (!resp.ok) return;
        const p = await resp.json();
        setSampleLabel(p);
        dispatchSampleSet(p);
      } catch (_) {
        /* non-fatal */
      }
    }

    async function postSamplePatch(rect) {
      const layerSelect = document.querySelector("[data-bx-layer-select-tile]");
      const layerId = Number(
        (layerSelect && layerSelect.value) ||
        panel.getAttribute("data-bx-default-layer-id") || 0
      );
      const body = {
        layer_id: layerId,
        x: rect.x,
        y: rect.y,
        width:  rect.width,
        height: rect.height,
        pattern_n: 2,
      };
      try {
        const resp = await fetch(`/design/maps/${mapId}/sample-patch`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
          body: JSON.stringify(body),
        });
        if (!resp.ok) {
          const text = await resp.text();
          showError(text.trim() || `HTTP ${resp.status}`);
          return;
        }
        showError("");
        setSampleLabel(rect);
        dispatchSampleSet(rect);
      } catch (err) {
        showError(String((err && err.message) || err));
      }
    }

    async function deleteSamplePatch() {
      try {
        const resp = await fetch(`/design/maps/${mapId}/sample-patch`, {
          method: "DELETE",
          headers: { "X-CSRF-Token": csrfToken() },
        });
        if (!resp.ok) return;
        setSampleLabel(null);
        dispatchSampleClear();
      } catch (_) {
        /* non-fatal */
      }
    }

    if (sampleClearBtn) {
      sampleClearBtn.addEventListener("click", deleteSamplePatch);
    }

    // The canvas Sample tool fires this on mouse-up after a drag-rect.
    // We clamp the rectangle to the engine's [2, 32] cell limits and
    // POST it. Out-of-range drags surface a friendly message instead
    // of a Postgres CHECK violation.
    document.addEventListener("bx:procedural-sample-drawn", (e) => {
      const r = (e && e.detail) || {};
      if (!r || r.width < 2 || r.height < 2) {
        showError("Sample must be at least 2×2 cells. Drag a larger rectangle.");
        return;
      }
      if (r.width > 32 || r.height > 32) {
        showError("Sample is capped at 32×32 cells. Drag a smaller rectangle.");
        return;
      }
      postSamplePatch(r);
    });

    // ---- Per-tile include toggle (eye icon on each palette entry) ----

    document.querySelectorAll("[data-bx-palette-include]").forEach((btn) => {
      btn.addEventListener("click", async (e) => {
        e.stopPropagation(); // don't change the active tile selection
        const li = btn.closest("li[data-bx-entity-type-id]");
        if (!li) return;
        const id = li.getAttribute("data-bx-entity-type-id");
        const wasOn = li.getAttribute("data-bx-proc-include") === "1";
        const next = !wasOn;
        try {
          const resp = await fetch(`/design/entity-types/${id}/procedural-include`, {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
            body: JSON.stringify({ include: next }),
          });
          if (!resp.ok) return;
          li.setAttribute("data-bx-proc-include", next ? "1" : "0");
          li.classList.toggle("bx-mapmaker__palette__entry--excluded", !next);
          btn.setAttribute("aria-pressed", next ? "1" : "0");
          btn.textContent = next ? "👁" : "🚫";
          btn.setAttribute(
            "title",
            next
              ? "Tile is included in procedural fill — click to exclude"
              : "Tile is excluded from procedural fill — click to include"
          );
        } catch (_) {
          /* non-fatal; the next reload reflects truth */
        }
      });
    });

    // ---- Generate / Reroll / Clear / Commit ----

    async function runPreview() {
      showError("");
      showStatus("Generating…");
      generateBtn.disabled = true;
      try {
        const resp = await fetch(`/design/maps/${mapId}/preview`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
          body: JSON.stringify({ seed: currentSeed() }),
        });
        if (!resp.ok) {
          const text = await resp.text();
          showError(text.trim() || `HTTP ${resp.status}`);
          showStatus("");
          return;
        }
        const region = await resp.json();
        let msg = `Generated ${region.width}×${region.height}`;
        if (region.algorithm === "chunked-overlapping") {
          msg += ` from your painted tiles`;
          if (region.pattern_count > 0) msg += ` (${region.pattern_count} patterns)`;
        } else {
          msg += ` from socket adjacency`;
        }
        showStatus(msg);
        if (canvas) {
          canvas.dispatchEvent(new CustomEvent("bx:procedural-preview", {
            bubbles: true,
            detail: { region, seed: currentSeed() },
          }));
        }
      } catch (err) {
        showError(String((err && err.message) || err));
        showStatus("");
      } finally {
        generateBtn.disabled = false;
      }
    }

    function clearPreview() {
      showStatus("Cleared.");
      showError("");
      if (canvas) {
        canvas.dispatchEvent(new CustomEvent("bx:procedural-preview-clear", { bubbles: true }));
      }
    }

    generateBtn.addEventListener("click", runPreview);
    if (rerollBtn) {
      rerollBtn.addEventListener("click", () => {
        if (lockedInput && lockedInput.checked) {
          showStatus("Seed is locked. Uncheck to reroll.");
          return;
        }
        seedInput.value = String(pickRandomSeed());
        runPreview();
      });
    }
    if (clearBtn) clearBtn.addEventListener("click", clearPreview);

    if (commitBtn) {
      commitBtn.addEventListener("click", async () => {
        if (!confirm("Commit this seed to the map? Existing tiles in the base layer will be replaced; locked cells are preserved.")) return;
        showError("");
        showStatus("Committing…");
        commitBtn.disabled = true;
        try {
          const resp = await fetch(`/design/maps/${mapId}/materialize`, {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
            body: JSON.stringify({ seed: currentSeed() }),
          });
          if (!resp.ok) {
            const text = await resp.text();
            showError(text.trim() || `HTTP ${resp.status}`);
            showStatus("");
            return;
          }
          const r = await resp.json();
          showStatus(`Committed ${r.tiles_written} tiles to layer ${r.layer_id}.`);
          if (canvas) {
            canvas.dispatchEvent(new CustomEvent("bx:mapmaker-reload", { bubbles: true }));
            canvas.dispatchEvent(new CustomEvent("bx:procedural-preview-clear", { bubbles: true }));
          }
        } catch (err) {
          showError(String((err && err.message) || err));
          showStatus("");
        } finally {
          commitBtn.disabled = false;
        }
      });
    }

    // ---- Lock count chip refresh ----

    function updateLockCountText(n) {
      if (!lockedCountEl) return;
      lockedCountEl.textContent = n === 1 ? "1 cell locked" : `${n} cells locked`;
      if (locksClearBtn) {
        locksClearBtn.style.display = n > 0 ? "" : "none";
      }
    }
    document.addEventListener("bx:locks-changed", (e) => {
      const n = e && e.detail && typeof e.detail.count === "number" ? e.detail.count : null;
      if (n !== null) updateLockCountText(n);
    });

    if (locksClearBtn) {
      locksClearBtn.addEventListener("click", async () => {
        if (!confirm("Remove every lock on this map? This can't be undone.")) return;
        try {
          const resp = await fetch(`/design/maps/${mapId}/locks`, {
            method: "DELETE",
            headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
            body: JSON.stringify({ all: true }),
          });
          if (!resp.ok) {
            const text = await resp.text();
            showError(text.trim() || `HTTP ${resp.status}`);
            return;
          }
          updateLockCountText(0);
          if (canvas) canvas.dispatchEvent(new CustomEvent("bx:locks-cleared", { bubbles: true }));
          showStatus("All locks cleared.");
        } catch (err) {
          showError(String((err && err.message) || err));
        }
      });
    }

    // ---- Init ----
    setSampleLabel(null); // hide the Clear button until we know there is one
    loadSamplePatch();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
