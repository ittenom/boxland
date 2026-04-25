// Boxland — Mapmaker procedural-mode controls.
//
// Lives as a small static script (loaded by views/mapmaker.templ when the
// map's mode == "procedural") so we don't need a Vite build step for the
// design tool. The render-side ghost-overlay rendering is intentionally
// deferred to the Pixi mapmaker entry (PLAN.md task 116); this module
// just owns the panel UX:
//   * Generate / Reroll / Clear / Lock seed
//   * POSTs to /design/maps/{id}/preview
//   * Surfaces error messages from the server
//   * Dispatches a `bx:procedural-preview` CustomEvent on the canvas
//     element with the parsed Region; the Pixi entry listens for it.
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
    const canvas = document.querySelector("[data-bx-mapmaker-canvas]");

    if (!seedInput || !generateBtn) return;

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
      // Number-typed input is fine for v1 since seeds up to 2^53 fit in a JS
      // double. The server accepts uint64 but we never need that many bits
      // in the UI iteration loop.
      return Math.floor(v);
    }

    function pickRandomSeed() {
      // Use crypto-strong randomness so two designers iterating in parallel
      // don't keep landing on the same seed.
      const buf = new Uint32Array(2);
      (window.crypto || window.msCrypto).getRandomValues(buf);
      // Combine to a 53-bit-safe integer.
      return buf[0] * 0x10000 + (buf[1] & 0xfffff);
    }

    async function runPreview() {
      showError("");
      showStatus("Generating…");
      generateBtn.disabled = true;
      try {
        const csrf = document
          .querySelector('meta[name="csrf-token"]')
          ?.getAttribute("content");
        const resp = await fetch(`/design/maps/${mapId}/preview`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-CSRF-Token": csrf || "",
          },
          body: JSON.stringify({ seed: currentSeed() }),
        });
        if (!resp.ok) {
          const text = await resp.text();
          showError(text.trim() || `HTTP ${resp.status}`);
          showStatus("");
          return;
        }
        const region = await resp.json();
        showStatus(`Generated ${region.width}×${region.height} (${region.tileset_size} tiles)`);
        // Hand off to the renderer (when the Pixi mapmaker entry lands in
        // task 116 it'll listen for this event).
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
        canvas.dispatchEvent(
          new CustomEvent("bx:procedural-preview-clear", { bubbles: true })
        );
      }
    }

    generateBtn.addEventListener("click", () => {
      runPreview();
    });

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
    if (clearBtn) {
      clearBtn.addEventListener("click", clearPreview);
    }

    const commitBtn = panel.querySelector("[data-bx-proc-commit]");
    if (commitBtn) {
      commitBtn.addEventListener("click", async () => {
        if (!confirm("Commit this seed to the map? Existing tiles in the base layer will be replaced.")) return;
        showError("");
        showStatus("Committing…");
        commitBtn.disabled = true;
        try {
          const csrf = document
            .querySelector('meta[name="csrf-token"]')
            ?.getAttribute("content");
          const resp = await fetch(`/design/maps/${mapId}/materialize`, {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              "X-CSRF-Token": csrf || "",
            },
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
