// Boxland — Mapmaker procedural-mode controls.
//
// Lives as a small static script (loaded by views/mapmaker.templ when the
// map's mode == "procedural") so we don't need a Vite build step for the
// design tool. The render-side ghost-overlay rendering is intentionally
// deferred to the Pixi mapmaker entry (PLAN.md task 116); this module
// owns the panel UX:
//   * Generation-algorithm picker (Socket / Pixel WFC)
//   * Generate / Reroll / Clear / Lock seed
//   * POSTs to /design/maps/{id}/preview, /materialize, /gen-algorithm,
//     /locks
//   * Surfaces error messages from the server
//   * Dispatches `bx:procedural-preview` and `bx:locks-changed` CustomEvents
//     on the canvas element so mapmaker.js can re-render.
(() => {
  "use strict";

  function init() {
    const panel = document.querySelector("[data-bx-procedural]");
    if (!panel) return;
    const mapId = panel.getAttribute("data-map-id");
    const seedInput = panel.querySelector("[data-bx-proc-seed]");
    const lockedInput = panel.querySelector("[data-bx-proc-locked]");
    const generateBtn = panel.querySelector("[data-bx-proc-generate]");
    const rerollBtn = panel.querySelector("[data-bx-proc-reroll]");
    const clearBtn = panel.querySelector("[data-bx-proc-clear]");
    const status = panel.querySelector("[data-bx-proc-status]");
    const errorEl = panel.querySelector("[data-bx-proc-error]");
    const algoGroup = panel.querySelector("[data-bx-proc-algorithm]");
    const lockedCountEl = panel.querySelector("[data-bx-locked-count]");
    const locksClearBtn = panel.querySelector("[data-bx-locks-clear]");
    const canvas = document.querySelector("[data-bx-mapmaker-canvas]");

    if (!seedInput || !generateBtn) return;

    function csrfToken() {
      return document.querySelector('meta[name="csrf-token"]')?.getAttribute("content") || "";
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

    function currentAlgorithm() {
      return panel.getAttribute("data-bx-gen-algorithm") || "socket";
    }

    async function setAlgorithm(value) {
      if (value === currentAlgorithm()) return;
      try {
        const resp = await fetch(`/design/maps/${mapId}/gen-algorithm`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
          body: JSON.stringify({ algorithm: value }),
        });
        if (!resp.ok) {
          const text = await resp.text();
          showError(text.trim() || `HTTP ${resp.status}`);
          return;
        }
        panel.setAttribute("data-bx-gen-algorithm", value);
        // Repaint segmented control state.
        if (algoGroup) {
          algoGroup.querySelectorAll(".bx-segmented__option").forEach((opt) => {
            const input = opt.querySelector('input[type="radio"]');
            const on = input && input.value === value;
            opt.classList.toggle("bx-segmented__option--on", !!on);
            if (input) input.checked = !!on;
          });
        }
        showStatus(`Algorithm set to ${value === "pixel_wfc" ? "Pixel WFC" : "Socket"}.`);
      } catch (err) {
        showError(String(err && err.message ? err.message : err));
      }
    }

    if (algoGroup) {
      algoGroup.addEventListener("change", (e) => {
        const t = e.target;
        if (t && t.matches('input[type="radio"]')) {
          setAlgorithm(t.value);
        }
      });
      // Click on the label too — radio change fires anyway, but this gives
      // an instant visual response on browsers that delay it.
      algoGroup.addEventListener("click", (e) => {
        const opt = e.target.closest && e.target.closest(".bx-segmented__option");
        if (!opt) return;
        const input = opt.querySelector('input[type="radio"]');
        if (input && !input.checked) {
          input.checked = true;
          setAlgorithm(input.value);
        }
      });
    }

    async function runPreview() {
      showError("");
      showStatus("Generating…");
      generateBtn.disabled = true;
      try {
        const resp = await fetch(`/design/maps/${mapId}/preview`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
          body: JSON.stringify({ seed: currentSeed(), algorithm: currentAlgorithm() }),
        });
        if (!resp.ok) {
          const text = await resp.text();
          showError(text.trim() || `HTTP ${resp.status}`);
          showStatus("");
          return;
        }
        const region = await resp.json();
        const algoLabel = region.algorithm === "pixel_wfc" ? "Pixel WFC" : "Socket";
        let msg = `Generated ${region.width}×${region.height} via ${algoLabel} (${region.tileset_size} tiles)`;
        if (region.fallbacks > 0) {
          msg += ` — ${region.fallbacks} fallback cells (add tile variety for tighter matches)`;
        }
        showStatus(msg);
        if (canvas) {
          canvas.dispatchEvent(
            new CustomEvent("bx:procedural-preview", {
              bubbles: true,
              detail: { region, seed: currentSeed() },
            })
          );
        }
      } catch (err) {
        showError(String(err && err.message ? err.message : err));
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

    // Lock-count refresh: mapmaker.js dispatches bx:locks-changed after a
    // brushstroke saves; we update the chip text without a full reload.
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
          showError(String(err && err.message ? err.message : err));
        }
      });
    }

    const commitBtn = panel.querySelector("[data-bx-proc-commit]");
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
          showError(String(err && err.message ? err.message : err));
          showStatus("");
        } finally {
          commitBtn.disabled = false;
        }
      });
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
