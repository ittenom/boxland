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

    function algoLabel(value) {
      switch (value) {
        case "overlapping": return "Overlapping";
        case "socket":      return "Socket";
        default:            return value || "Socket";
      }
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
        showStatus(`Algorithm set to ${algoLabel(value)}.`);
        updateSampleUI();
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
        let msg = `Generated ${region.width}×${region.height} via ${algoLabel(region.algorithm)} (${region.tileset_size} tiles`;
        if (region.pattern_count > 0) {
          msg += `, ${region.pattern_count} patterns`;
        }
        msg += `)`;
        if (currentAlgorithm() === "overlapping" && region.algorithm === "socket") {
          msg += " — overlapping fell back to socket (paint a sample patch first)";
        }
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

    // ---- Sample-patch: source rectangle for the overlapping engine ----
    //
    // The overlapping engine reads its NxN learning sample from a small
    // rectangle of the map's tile layer. The designer picks that rect
    // here (numeric inputs; a future enhancement adds drag-to-select on
    // the canvas via a `bx:request-sample-patch` custom event).

    const sampleSection   = panel.querySelector("[data-bx-sample-patch]");
    const sampleStatus    = panel.querySelector("[data-bx-sample-status]");
    const sampleXInput    = panel.querySelector("[data-bx-sample-x]");
    const sampleYInput    = panel.querySelector("[data-bx-sample-y]");
    const sampleWInput    = panel.querySelector("[data-bx-sample-w]");
    const sampleHInput    = panel.querySelector("[data-bx-sample-h]");
    const sampleNInput    = panel.querySelector("[data-bx-sample-n]");
    const sampleSaveBtn   = panel.querySelector("[data-bx-sample-save]");
    const sampleClearBtn  = panel.querySelector("[data-bx-sample-clear]");

    function updateSampleUI() {
      if (!sampleSection) return;
      const wantsSample = currentAlgorithm() === "overlapping";
      sampleSection.style.display = wantsSample ? "" : "none";
    }

    function showSampleStatus(text) {
      if (sampleStatus) sampleStatus.textContent = text || "";
    }

    async function loadSamplePatch() {
      if (!sampleSection) return;
      try {
        const resp = await fetch(`/design/maps/${mapId}/sample-patch`);
        if (resp.status === 204) {
          showSampleStatus("No sample patch defined — overlapping will fall back to socket.");
          return;
        }
        if (!resp.ok) return;
        const p = await resp.json();
        if (sampleXInput) sampleXInput.value = String(p.x);
        if (sampleYInput) sampleYInput.value = String(p.y);
        if (sampleWInput) sampleWInput.value = String(p.width);
        if (sampleHInput) sampleHInput.value = String(p.height);
        if (sampleNInput) sampleNInput.value = String(p.pattern_n || 2);
        showSampleStatus(`Sample: ${p.width}×${p.height} at (${p.x}, ${p.y}), N=${p.pattern_n}.`);
      } catch (err) {
        // Non-fatal — designer can still set a fresh patch.
      }
    }

    if (sampleSaveBtn) {
      sampleSaveBtn.addEventListener("click", async () => {
        const layerSelect = document.querySelector("[data-bx-layer-select-tile]");
        // Fall back to "1" so the panel still works on older mapmaker
        // builds; the server validates that the layer belongs to the map.
        const layerId = Number((layerSelect && layerSelect.value) || panel.getAttribute("data-bx-default-layer-id") || 0);
        const body = {
          layer_id: layerId,
          x: Number(sampleXInput && sampleXInput.value) || 0,
          y: Number(sampleYInput && sampleYInput.value) || 0,
          width:  Number(sampleWInput && sampleWInput.value)  || 8,
          height: Number(sampleHInput && sampleHInput.value)  || 8,
          pattern_n: Number(sampleNInput && sampleNInput.value) || 2,
        };
        showSampleStatus("Saving…");
        try {
          const resp = await fetch(`/design/maps/${mapId}/sample-patch`, {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
            body: JSON.stringify(body),
          });
          if (!resp.ok) {
            const text = await resp.text();
            showSampleStatus(text.trim() || `HTTP ${resp.status}`);
            return;
          }
          showSampleStatus(`Sample: ${body.width}×${body.height} at (${body.x}, ${body.y}), N=${body.pattern_n}.`);
        } catch (err) {
          showSampleStatus(String(err && err.message ? err.message : err));
        }
      });
    }
    if (sampleClearBtn) {
      sampleClearBtn.addEventListener("click", async () => {
        if (!confirm("Remove the sample patch? Overlapping will fall back to socket until a new one is set.")) return;
        try {
          const resp = await fetch(`/design/maps/${mapId}/sample-patch`, {
            method: "DELETE",
            headers: { "X-CSRF-Token": csrfToken() },
          });
          if (!resp.ok) return;
          showSampleStatus("No sample patch defined — overlapping will fall back to socket.");
        } catch (err) {
          showSampleStatus(String(err && err.message ? err.message : err));
        }
      });
    }

    updateSampleUI();
    loadSamplePatch();

    // ---- Constraints (border / path) ----
    //
    // Per-map non-local constraints persisted via /constraints. List
    // current rules; let the designer add/remove. Every change goes
    // straight to the server — there's no "draft" buffer because the
    // procedural panel as a whole is preview-driven (changes take
    // effect on the next Generate).

    const constraintList   = panel.querySelector("[data-bx-constraints-list]");
    const constraintStatus = panel.querySelector("[data-bx-constraint-status]");
    const constraintKind   = panel.querySelector("[data-bx-constraint-kind]");
    const constraintEt     = panel.querySelector("[data-bx-constraint-et]");
    const constraintEdges  = panel.querySelector("[data-bx-constraint-edges]");
    const constraintEdgesWrap = panel.querySelector("[data-bx-constraint-edges-wrap]");
    const constraintAddBtn = panel.querySelector("[data-bx-constraint-add]");

    function showConstraintStatus(text) {
      if (constraintStatus) constraintStatus.textContent = text || "";
    }

    function constraintLabel(c) {
      if (c.kind === "border") {
        let edges = "all";
        try {
          const p = c.params ? JSON.parse(c.params) : {};
          if (Array.isArray(p.edges) && p.edges.length) edges = p.edges.join(",");
          return `Border tile ${p.entity_type_id || "?"} on ${edges}` + (p.restrict ? " (restrict)" : "");
        } catch (_) { return "Border (parse error)"; }
      }
      if (c.kind === "path") {
        try {
          const p = c.params ? JSON.parse(c.params) : {};
          const ids = (p.entity_type_ids || []).join(",") || "any non-zero";
          return `Path connectivity for tile(s): ${ids}`;
        } catch (_) { return "Path (parse error)"; }
      }
      return c.kind;
    }

    async function loadConstraints() {
      if (!constraintList) return;
      try {
        const resp = await fetch(`/design/maps/${mapId}/constraints`);
        if (!resp.ok) return;
        const data = await resp.json();
        constraintList.innerHTML = "";
        const items = data.items || [];
        if (items.length === 0) {
          showConstraintStatus("No constraints. The map is generated from sockets / sample only.");
          return;
        }
        showConstraintStatus("");
        items.forEach((c) => {
          const li = document.createElement("li");
          li.className = "bx-row";
          li.style.gap = "8px";
          li.style.alignItems = "center";
          const span = document.createElement("span");
          span.className = "bx-small";
          span.textContent = constraintLabel(c);
          li.appendChild(span);
          const del = document.createElement("button");
          del.type = "button";
          del.className = "bx-btn bx-btn--ghost bx-btn--small";
          del.textContent = "Remove";
          del.addEventListener("click", async () => {
            try {
              const r = await fetch(`/design/maps/${mapId}/constraints/${c.id}`, {
                method: "DELETE",
                headers: { "X-CSRF-Token": csrfToken() },
              });
              if (!r.ok) {
                showConstraintStatus(`HTTP ${r.status}`);
                return;
              }
              loadConstraints();
            } catch (err) {
              showConstraintStatus(String(err && err.message ? err.message : err));
            }
          });
          li.appendChild(del);
          constraintList.appendChild(li);
        });
      } catch (err) {
        showConstraintStatus(String(err && err.message ? err.message : err));
      }
    }

    function updateConstraintFormVisibility() {
      if (!constraintKind || !constraintEdgesWrap) return;
      constraintEdgesWrap.style.display = constraintKind.value === "border" ? "" : "none";
    }
    if (constraintKind) {
      constraintKind.addEventListener("change", updateConstraintFormVisibility);
    }

    if (constraintAddBtn) {
      constraintAddBtn.addEventListener("click", async () => {
        const kind = constraintKind ? constraintKind.value : "border";
        const et = Number(constraintEt && constraintEt.value);
        if (!et || et <= 0) {
          showConstraintStatus("Enter the entity-type ID this constraint targets.");
          return;
        }
        let params;
        if (kind === "border") {
          params = { entity_type_id: et, edges: [constraintEdges ? constraintEdges.value : "all"] };
        } else {
          params = { entity_type_ids: [et] };
        }
        try {
          const resp = await fetch(`/design/maps/${mapId}/constraints`, {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
            body: JSON.stringify({ kind, params }),
          });
          if (!resp.ok) {
            const text = await resp.text();
            showConstraintStatus(text.trim() || `HTTP ${resp.status}`);
            return;
          }
          if (constraintEt) constraintEt.value = "";
          loadConstraints();
        } catch (err) {
          showConstraintStatus(String(err && err.message ? err.message : err));
        }
      });
    }

    updateConstraintFormVisibility();
    loadConstraints();

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
