// Boxland — Mapmaker authored-mode painting client.
//
// Owns the editor canvas: loads tiles via /design/maps/{id}/tiles, paints
// in response to mouse + tool selection, and POSTs batched placements
// back. Stays a static script (no build step) so the design tool boots
// from a single Go binary.
//
// Wire shape (matches the server handlers in internal/designer/handlers.go):
//
//   GET    /design/maps/{id}/tiles            -> { tiles: [{layer_id,x,y,entity_type_id}] }
//   POST   /design/maps/{id}/tiles            <- { tiles: [...] }   -> { placed: N }
//   DELETE /design/maps/{id}/tiles            <- { layer_id, points: [[x,y]] } -> { erased: N }
//
// Tools (left pane, data-bx-tool=...):
//   brush   one cell at a time (click + drag = stroke)
//   rect    click-drag rectangle filled with active entity
//   fill    flood fill of contiguous matching cells on the active layer
//   eyedrop click cell -> palette switches to that entity type
//   eraser  clears cells (DELETE)
//
// Active state lives entirely on this module; no global. Layers list
// click switches the active layer. Palette item click switches the
// active entity type. The status bar mirrors all three.
(() => {
	"use strict";

	const TILE_PX = 32; // canonical tile size; matches the README's "32x32 tiles"

	function $(sel, root) {
		return (root || document).querySelector(sel);
	}
	function $$(sel, root) {
		return Array.from((root || document).querySelectorAll(sel));
	}

	function init() {
		const canvas = $("[data-bx-mapmaker-canvas]");
		if (!canvas) return;

		const mapId  = Number(canvas.getAttribute("data-map-id")) || 0;
		const mapW   = Number(canvas.getAttribute("data-map-w")) || 0;
		const mapH   = Number(canvas.getAttribute("data-map-h")) || 0;
		if (mapId <= 0 || mapW <= 0 || mapH <= 0) return;

		// Size canvas to the map at 1:1 tile scale. CSS shrinks if the
		// viewport is smaller; logical coordinates stay tile-true.
		canvas.width  = mapW * TILE_PX;
		canvas.height = mapH * TILE_PX;
		canvas.style.maxWidth  = "100%";
		canvas.style.maxHeight = "100%";
		const ctx = canvas.getContext("2d");
		if (!ctx) return;
		ctx.imageSmoothingEnabled = false;

		const state = {
			tool:        "brush", // brush | rect | fill | eyedrop | eraser
			activeLayer: Number(canvas.getAttribute("data-default-layer-id")) || 0,
			activeEntity: 0,
			tiles: new Map(),    // key="L:x:y" -> { layerId, x, y, entityTypeId }
			// images keyed by SOURCE URL (not entity id) so a tile sheet
			// referenced by 24 tile entities loads exactly once and every
			// cell shares the bitmap. Saves memory and makes the canvas
			// snap to fully-painted on the first onload tick.
			images: new Map(),   // url -> HTMLImageElement | null (null while loading)
			// paletteByEntity carries the atlas info so drawTile can
			// slice the right 32x32 sub-rect of the source sheet.
			// Shape: {name, spriteUrl, atlasIndex, atlasCols, tileSize}
			paletteByEntity: new Map(),
			procPreview: null,   // last bx:procedural-preview, drawn as ghost overlay
			pending: 0,          // outstanding network requests
		};

		readPalette(state);
		bindTools(state);
		bindLayers(state, canvas);
		bindPalette(state);
		bindCanvas(state, canvas, ctx);
		bindProceduralOverlay(state, canvas);
		updateStatus(state);

		loadTiles(mapId)
			.then((tiles) => {
				const normalized = tiles.map(normalizeTile).filter(Boolean);
				for (const t of normalized) state.tiles.set(tileKey(t), t);
				prefetchImages(state, normalized);
				draw(state, ctx, canvas);
			})
			.catch((err) => {
				flash(`Failed to load tiles: ${err && err.message ? err.message : err}`);
			});

		// Public surface for other modules (procedural overlay, future
		// tile-group stamps) via a CustomEvent layer. Keeps coupling
		// observable in the dev console.
		canvas.addEventListener("bx:mapmaker-redraw", () => draw(state, ctx, canvas));

		// Expose for the procedural module's commit success path: it can
		// fire bx:mapmaker-reload to re-pull the canonical tiles after
		// a WFC commit lands.
		canvas.addEventListener("bx:mapmaker-reload", () => {
			loadTiles(mapId).then((tiles) => {
				state.tiles.clear();
				const normalized = tiles.map(normalizeTile).filter(Boolean);
				for (const t of normalized) state.tiles.set(tileKey(t), t);
				prefetchImages(state, normalized);
				draw(state, ctx, canvas);
			});
		});

		// Keep the canvas resolution stable on devicePixelRatio changes.
		// (Most relevant when designers move the window to a different
		// monitor; we keep nominal pixels and let CSS scale.)
		window.addEventListener("resize", () => draw(state, ctx, canvas));

		function loadTiles(id) {
			return fetch(`/design/maps/${id}/tiles`, {
				headers: { Accept: "application/json" },
				credentials: "same-origin",
			})
				.then((r) => {
					if (!r.ok) throw new Error(`HTTP ${r.status}`);
					return r.json();
				})
				.then((j) => Array.isArray(j.tiles) ? j.tiles : []);
		}

		function postTiles(tiles) {
			if (tiles.length === 0) return Promise.resolve();
			state.pending++;
			updateStatus(state);
			return fetchJSON(`/design/maps/${mapId}/tiles`, "POST", { tiles }).finally(() => {
				state.pending--;
				updateStatus(state);
			});
		}
		function deleteTiles(layerId, points) {
			if (points.length === 0) return Promise.resolve();
			state.pending++;
			updateStatus(state);
			return fetchJSON(`/design/maps/${mapId}/tiles`, "DELETE", {
				layer_id: layerId, points,
			}).finally(() => {
				state.pending--;
				updateStatus(state);
			});
		}

		// Stamp = the meaning of "click here" for the current tool.
		// Returns { placed: [...], erased: [...] }, applied optimistically.
		function stamp(cellX, cellY) {
			const layerId = state.activeLayer;
			if (!layerId) {
				flash("Pick a layer first.");
				return { placed: [], erased: [] };
			}
			if (cellX < 0 || cellY < 0 || cellX >= mapW || cellY >= mapH) return { placed: [], erased: [] };

			if (state.tool === "eraser") {
				const k = tileKey({ layerId, x: cellX, y: cellY });
				if (!state.tiles.has(k)) return { placed: [], erased: [] };
				state.tiles.delete(k);
				return { placed: [], erased: [{ layerId, x: cellX, y: cellY }] };
			}
			if (state.tool === "eyedrop") {
				const k = tileKey({ layerId, x: cellX, y: cellY });
				const t = state.tiles.get(k);
				if (t && t.entityTypeId) setActiveEntity(state, t.entityTypeId);
				return { placed: [], erased: [] };
			}
			if (!state.activeEntity) {
				flash("Pick an entity from the palette.");
				return { placed: [], erased: [] };
			}
			const t = { layerId, x: cellX, y: cellY, entityTypeId: state.activeEntity };
			state.tiles.set(tileKey(t), t);
			return { placed: [t], erased: [] };
		}

		// ---------- Tool dispatch ----------------------------------------

		function bindCanvas(state, canvas, ctx) {
			let dragging = false;
			let dragStart = null;     // {x, y}  for rect tool
			let strokePlaced = [];    // accumulator for brush + rect
			let strokeErased = [];

			canvas.addEventListener("pointermove", (e) => {
				const cell = pointerToCell(e, canvas);
				const status = $('[data-bx-mapmaker-status="cursor"]');
				if (status) status.textContent = `(${cell.x}, ${cell.y})`;
			});

			canvas.addEventListener("pointerdown", (e) => {
				if (e.button !== 0) return;
				canvas.setPointerCapture(e.pointerId);
				dragging = true;
				const cell = pointerToCell(e, canvas);

				if (state.tool === "fill") {
					floodFill(state, cell.x, cell.y, mapW, mapH).then(() => draw(state, ctx, canvas));
					dragging = false;
					return;
				}
				if (state.tool === "rect") {
					dragStart = cell;
					draw(state, ctx, canvas, { rectFrom: dragStart, rectTo: cell });
					return;
				}
				const out = stamp(cell.x, cell.y);
				strokePlaced.push(...out.placed);
				strokeErased.push(...out.erased);
				draw(state, ctx, canvas);
			});

			canvas.addEventListener("pointermove", (e) => {
				if (!dragging) return;
				const cell = pointerToCell(e, canvas);
				if (state.tool === "rect") {
					draw(state, ctx, canvas, { rectFrom: dragStart, rectTo: cell });
					return;
				}
				if (state.tool === "brush" || state.tool === "eraser") {
					const out = stamp(cell.x, cell.y);
					strokePlaced.push(...out.placed);
					strokeErased.push(...out.erased);
					draw(state, ctx, canvas);
				}
			});

			const finishStroke = (e) => {
				if (!dragging) return;
				dragging = false;
				try { canvas.releasePointerCapture(e.pointerId); } catch (_) {}

				if (state.tool === "rect" && dragStart) {
					const cell = pointerToCell(e, canvas);
					const r = normalizeRect(dragStart, cell);
					for (let y = r.y0; y <= r.y1; y++) {
						for (let x = r.x0; x <= r.x1; x++) {
							const out = stamp(x, y);
							strokePlaced.push(...out.placed);
							strokeErased.push(...out.erased);
						}
					}
					dragStart = null;
				}

				const placedWire = strokePlaced.map(toWire);
				const erasedPoints = strokeErased.map((t) => [t.x, t.y]);
				const layerId = state.activeLayer;
				strokePlaced = [];
				strokeErased = [];
				draw(state, ctx, canvas);

				const tasks = [];
				if (placedWire.length > 0) tasks.push(postTiles(placedWire));
				if (erasedPoints.length > 0) tasks.push(deleteTiles(layerId, erasedPoints));
				if (tasks.length > 0) {
					Promise.all(tasks).catch((err) => {
						flash(`Save failed: ${err.message || err}`);
					});
				}
			};
			canvas.addEventListener("pointerup",     finishStroke);
			canvas.addEventListener("pointercancel", finishStroke);
			canvas.addEventListener("pointerleave",  (e) => { if (dragging) finishStroke(e); });
		}

		// Hotkeys: B / R / F / I / E mirror the toolbar tooltips.
		// H opens the per-realm HUD editor (matches docs/hotkeys.md
		// + the chip-link in mapmaker.templ).
		document.addEventListener("keydown", (e) => {
			if (isTextEditingTarget(e.target)) return;
			const map = { b: "brush", r: "rect", f: "fill", i: "eyedrop", e: "eraser" };
			const k = e.key.toLowerCase();
			if (map[k]) { setTool(state, map[k]); e.preventDefault(); return; }
			if (k === "h" && !e.ctrlKey && !e.metaKey && !e.altKey) {
				const host = document.querySelector("[data-bx-mapmaker-canvas]");
				const mapId = host && host.getAttribute("data-map-id");
				if (mapId) { window.location.href = "/design/maps/" + mapId + "/hud"; e.preventDefault(); }
			}
		});

		function floodFill(state, sx, sy, w, h) {
			const layerId = state.activeLayer;
			if (!layerId) { flash("Pick a layer first."); return Promise.resolve(); }
			const startKey = tileKey({ layerId, x: sx, y: sy });
			const start = state.tiles.get(startKey);
			const startEntity = start ? start.entityTypeId : 0;
			const target = state.tool === "eraser" ? 0 : state.activeEntity;
			if (target === 0 && state.tool !== "eraser") {
				flash("Pick an entity to fill with.");
				return Promise.resolve();
			}
			if (startEntity === target) return Promise.resolve();

			const placed = [];
			const erased = [];
			const visited = new Set();
			const queue = [[sx, sy]];
			let safety = 0;
			while (queue.length > 0 && safety < 4096) {
				safety++;
				const [x, y] = queue.shift();
				const k = tileKey({ layerId, x, y });
				if (visited.has(k)) continue;
				visited.add(k);
				if (x < 0 || y < 0 || x >= w || y >= h) continue;
				const cur = state.tiles.get(k);
				const curEntity = cur ? cur.entityTypeId : 0;
				if (curEntity !== startEntity) continue;
				if (target === 0) {
					if (cur) { state.tiles.delete(k); erased.push({ layerId, x, y }); }
				} else {
					const t = { layerId, x, y, entityTypeId: target };
					state.tiles.set(k, t);
					placed.push(t);
				}
				queue.push([x+1, y], [x-1, y], [x, y+1], [x, y-1]);
			}
			if (safety >= 4096) flash("Fill stopped at 4096 cells (safety cap).");

			const tasks = [];
			if (placed.length > 0) tasks.push(postTiles(placed.map(toWire)));
			if (erased.length > 0) tasks.push(deleteTiles(layerId, erased.map((t) => [t.x, t.y])));
			return Promise.all(tasks).catch((err) => flash(`Save failed: ${err.message || err}`));
		}

		// ---------- Inputs (toolbar / layers / palette) ------------------

		function bindTools(state) {
			$$(".bx-mapmaker__tools [data-bx-tool]").forEach((btn) => {
				btn.addEventListener("click", () => setTool(state, btn.getAttribute("data-bx-tool")));
			});
			highlightTool(state.tool);
		}
		function setTool(state, tool) {
			state.tool = tool;
			highlightTool(tool);
			updateStatus(state);
		}
		function highlightTool(tool) {
			// Match the existing CSS contract: .bx-mapmaker__tools .bx-btn[aria-pressed="true"]
			// already styles itself with the brand accent.
			$$(".bx-mapmaker__tools [data-bx-tool]").forEach((b) => {
				b.setAttribute("aria-pressed", b.getAttribute("data-bx-tool") === tool ? "true" : "false");
			});
		}

		function bindLayers(state, canvas) {
			$$(".bx-mapmaker__layers li[data-bx-layer-id]").forEach((li) => {
				li.addEventListener("click", () => {
					const id = Number(li.getAttribute("data-bx-layer-id"));
					if (!id) return;
					state.activeLayer = id;
					$$(".bx-mapmaker__layers li").forEach((x) => x.setAttribute("aria-selected", "false"));
					li.setAttribute("aria-selected", "true");
					const layerStatus = $('[data-bx-mapmaker-status="layer"]');
					if (layerStatus) layerStatus.textContent = li.getAttribute("data-bx-layer-name") || "layer";
				});
			});
		}

		function bindPalette(state) {
			$$(".bx-mapmaker__palette li[data-bx-entity-type-id]").forEach((li) => {
				li.addEventListener("click", () => {
					const id = Number(li.getAttribute("data-bx-entity-type-id"));
					if (!id) return;
					setActiveEntity(state, id);
				});
			});
			// First palette item is auto-selected for convenience so a
			// brand-new designer can click and paint immediately.
			const first = $(".bx-mapmaker__palette li[data-bx-entity-type-id]");
			if (first) {
				const id = Number(first.getAttribute("data-bx-entity-type-id"));
				if (id) setActiveEntity(state, id);
			}

			// Client-side filter input — matches against data-bx-palette-name
			// (the entity name) so designers can find tiles fast in a
			// project with dozens. Empty input shows everything; non-
			// empty hides non-matching <li>s without touching state.
			const filter = $("[data-bx-palette-filter]");
			if (filter) {
				filter.addEventListener("input", () => {
					const q = filter.value.trim().toLowerCase();
					$$(".bx-mapmaker__palette li[data-bx-entity-type-id]").forEach((li) => {
						const name = (li.getAttribute("data-bx-palette-name") || "").toLowerCase();
						li.style.display = !q || name.includes(q) ? "" : "none";
					});
				});
			}

			// Add-tile bridge: the asset picker writes the picked id
			// into #mapmaker-add-tile. We listen for change, POST to
			// promote-to-entity, then reload the page to pick up the
			// new palette entry. Reload is cheaper than re-fetching
			// + diff-rendering the palette + canvas state.
			const addInput = $("[data-bx-mapmaker-add-tile]");
			if (addInput) {
				addInput.addEventListener("change", () => {
					const id = Number(addInput.value);
					if (!id) return;
					const token = document
						.querySelector('meta[name="csrf-token"]')
						?.getAttribute("content") || "";
					fetch(`/design/assets/${id}/promote-to-entity`, {
						method: "POST",
						credentials: "same-origin",
						headers: { "X-CSRF-Token": token, "Accept": "text/html" },
					}).then((r) => {
						if (!r.ok) {
							flash(`Could not add tile: HTTP ${r.status}`);
							addInput.value = "";
							return;
						}
						// HX-Redirect comes back as a header; we want
						// to land back on THIS map's editor, not the
						// new entity's page, so just reload in place.
						window.location.reload();
					}).catch((err) => {
						flash(`Could not add tile: ${err.message || err}`);
						addInput.value = "";
					});
				});
			}
		}

		function setActiveEntity(state, id) {
			state.activeEntity = id;
			$$(".bx-mapmaker__palette li").forEach((li) => {
				const isMe = Number(li.getAttribute("data-bx-entity-type-id")) === id;
				li.setAttribute("aria-selected", isMe ? "true" : "false");
			});
			updateStatus(state);
		}

		function readPalette(state) {
			$$(".bx-mapmaker__palette li[data-bx-entity-type-id]").forEach((li) => {
				const id = Number(li.getAttribute("data-bx-entity-type-id"));
				const name = li.getAttribute("title") || `entity #${id}`;
				// data-bx-sprite-url is the FULL source sheet, not a
				// per-cell crop — the renderer slices it via atlasIndex.
				const spriteUrl = li.getAttribute("data-bx-sprite-url") || "";
				const atlasIndex = Number(li.getAttribute("data-bx-atlas-index")) || 0;
				const atlasCols = Math.max(1, Number(li.getAttribute("data-bx-atlas-cols")) || 1);
				const tileSize = Number(li.getAttribute("data-bx-tile-size")) || 32;
				if (id) {
					state.paletteByEntity.set(id, {
						name, spriteUrl, atlasIndex, atlasCols, tileSize,
					});
				}
			});
		}

		function prefetchImages(state, tiles) {
			// Build the union of every URL referenced by tiles + palette
			// and load each exactly once. The image cache is keyed by URL
			// so dozens of palette entries that share a sheet share the
			// bitmap.
			const urls = new Set();
			for (const t of tiles) {
				const meta = state.paletteByEntity.get(t.entityTypeId);
				if (meta && meta.spriteUrl) urls.add(meta.spriteUrl);
			}
			for (const meta of state.paletteByEntity.values()) {
				if (meta.spriteUrl) urls.add(meta.spriteUrl);
			}
			for (const url of urls) ensureImage(state, url);
		}

		function ensureImage(state, url) {
			if (!url || state.images.has(url)) return;
			const img = new Image();
			img.crossOrigin = "anonymous";
			img.onload = () => {
				const canvas = $("[data-bx-mapmaker-canvas]");
				if (canvas) canvas.dispatchEvent(new CustomEvent("bx:mapmaker-redraw"));
			};
			img.onerror = () => state.images.set(url, null);
			img.src = url;
			state.images.set(url, img);
		}

		function bindProceduralOverlay(state, canvas) {
			canvas.addEventListener("bx:procedural-preview", (e) => {
				state.procPreview = e.detail.region || null;
				const ctx = canvas.getContext("2d");
				if (ctx) draw(state, ctx, canvas);
			});
			canvas.addEventListener("bx:procedural-preview-clear", () => {
				state.procPreview = null;
				const ctx = canvas.getContext("2d");
				if (ctx) draw(state, ctx, canvas);
			});
		}

		// ---------- Render ------------------------------------------------

		function draw(state, ctx, canvas, ghost) {
			ctx.fillStyle = "#11141b";
			ctx.fillRect(0, 0, canvas.width, canvas.height);

			drawGrid(ctx, canvas);

			// Sort by layer so higher-ord layers render on top. Layer ord
			// isn't on the wire format; use the DOM order as the source
			// of truth (already ord-ascending from Templ).
			const layerOrder = $$(".bx-mapmaker__layers li[data-bx-layer-id]")
				.map((li) => Number(li.getAttribute("data-bx-layer-id")));
			for (const layerId of layerOrder) {
				for (const t of state.tiles.values()) {
					if (t.layerId !== layerId) continue;
					drawTile(ctx, state, t);
				}
			}

			// Ghost preview from procedural module: yellow tint, 50% alpha.
			if (state.procPreview && state.procPreview.cells) {
				ctx.save();
				ctx.globalAlpha = 0.5;
				for (const c of state.procPreview.cells) {
					ctx.fillStyle = "#FFDD4A";
					ctx.fillRect(c.x * TILE_PX, c.y * TILE_PX, TILE_PX, TILE_PX);
				}
				ctx.restore();
			}

			// Active rect-tool marquee (live preview while dragging).
			if (ghost && ghost.rectFrom && ghost.rectTo) {
				const r = normalizeRect(ghost.rectFrom, ghost.rectTo);
				ctx.save();
				ctx.globalAlpha = 0.35;
				ctx.fillStyle = "#5ADBFF";
				ctx.fillRect(r.x0 * TILE_PX, r.y0 * TILE_PX, (r.x1 - r.x0 + 1) * TILE_PX, (r.y1 - r.y0 + 1) * TILE_PX);
				ctx.restore();
			}

			// Map bounds outline.
			ctx.strokeStyle = "rgba(90,219,255,0.4)";
			ctx.lineWidth = 1;
			ctx.strokeRect(0.5, 0.5, canvas.width - 1, canvas.height - 1);
		}

		function drawGrid(ctx, canvas) {
			ctx.strokeStyle = "rgba(255,255,255,0.05)";
			ctx.lineWidth = 1;
			ctx.beginPath();
			for (let x = 0; x <= canvas.width; x += TILE_PX) {
				ctx.moveTo(x + 0.5, 0);
				ctx.lineTo(x + 0.5, canvas.height);
			}
			for (let y = 0; y <= canvas.height; y += TILE_PX) {
				ctx.moveTo(0, y + 0.5);
				ctx.lineTo(canvas.width, y + 0.5);
			}
			ctx.stroke();
		}

		function drawTile(ctx, state, t) {
			const px = t.x * TILE_PX;
			const py = t.y * TILE_PX;
			const meta = state.paletteByEntity.get(t.entityTypeId);
			if (!meta || !meta.spriteUrl) {
				// Entity has no sprite assigned yet — draw a subtle
				// dashed outline so the cell is visibly placeholder
				// without the noisy yellow chips that used to mask
				// the actual rendering bug.
				drawPendingCell(ctx, px, py);
				return;
			}
			const img = state.images.get(meta.spriteUrl);
			if (!img) {
				ensureImage(state, meta.spriteUrl);
				drawPendingCell(ctx, px, py);
				return;
			}
			if (!img.complete || img.naturalWidth === 0) {
				drawPendingCell(ctx, px, py);
				return;
			}
			// Slice the source sheet to the entity's atlas cell.
			// (cellPx, cols) come from the asset's tile-sheet metadata
			// at upload time; single-frame sprites collapse to (32, 1).
			const cellPx = meta.tileSize || 32;
			const cols = Math.max(1, meta.atlasCols || 1);
			const sx = (meta.atlasIndex % cols) * cellPx;
			const sy = Math.floor(meta.atlasIndex / cols) * cellPx;
			ctx.drawImage(img, sx, sy, cellPx, cellPx, px, py, TILE_PX, TILE_PX);
		}

		// drawPendingCell renders a 1px dashed outline so an unloaded /
		// unsprited cell still telegraphs "tile lives here" without
		// dominating the canvas.
		function drawPendingCell(ctx, px, py) {
			ctx.save();
			ctx.strokeStyle = "rgba(255,255,255,0.18)";
			ctx.setLineDash([2, 2]);
			ctx.lineWidth = 1;
			ctx.strokeRect(px + 0.5, py + 0.5, TILE_PX - 1, TILE_PX - 1);
			ctx.restore();
		}

		function updateStatus(state) {
			const tool = $('[data-bx-mapmaker-status="tool"]');
			if (tool) tool.textContent = state.tool;
			const dirty = $('[data-bx-mapmaker-status="dirty"]');
			if (dirty) {
				if (state.pending > 0) dirty.textContent = `saving ${state.pending}…`;
				else if (state.activeEntity) {
					const meta = state.paletteByEntity.get(state.activeEntity);
					dirty.textContent = meta ? `painting: ${meta.name}` : `painting: #${state.activeEntity}`;
				} else dirty.textContent = "pick an entity";
			}
		}
	}

	// ---- helpers -------------------------------------------------------

	function tileKey(t) { return `${t.layerId}:${t.x}:${t.y}`; }
	function normalizeTile(t) {
		if (!t) return null;
		const layerId = Number(t.layerId ?? t.layer_id);
		const entityTypeId = Number(t.entityTypeId ?? t.entity_type_id);
		const x = Number(t.x);
		const y = Number(t.y);
		if (!layerId || !entityTypeId || !Number.isFinite(x) || !Number.isFinite(y)) return null;
		return { layerId, x, y, entityTypeId };
	}
	function toWire(t) {
		return { layer_id: t.layerId, x: t.x, y: t.y, entity_type_id: t.entityTypeId };
	}

	function pointerToCell(e, canvas) {
		const rect = canvas.getBoundingClientRect();
		const sx = (e.clientX - rect.left) / rect.width;
		const sy = (e.clientY - rect.top)  / rect.height;
		const tilesW = canvas.width  / 32;
		const tilesH = canvas.height / 32;
		return {
			x: Math.max(0, Math.min(tilesW - 1, Math.floor(sx * tilesW))),
			y: Math.max(0, Math.min(tilesH - 1, Math.floor(sy * tilesH))),
		};
	}

	function normalizeRect(a, b) {
		return {
			x0: Math.min(a.x, b.x),
			y0: Math.min(a.y, b.y),
			x1: Math.max(a.x, b.x),
			y1: Math.max(a.y, b.y),
		};
	}

	function fetchJSON(url, method, body) {
		const csrf = document.querySelector('meta[name="csrf-token"]')?.getAttribute("content") || "";
		return fetch(url, {
			method,
			headers: {
				"Content-Type": "application/json",
				"X-CSRF-Token": csrf,
			},
			credentials: "same-origin",
			body: body == null ? null : JSON.stringify(body),
		}).then(async (r) => {
			if (!r.ok) {
				const text = await r.text();
				throw new Error(text || `HTTP ${r.status}`);
			}
			return r.json();
		});
	}

	function flash(msg) {
		const status = document.querySelector("[data-bx-status-msg]");
		if (status) {
			status.textContent = msg;
			setTimeout(() => { if (status.textContent === msg) status.textContent = ""; }, 4000);
		} else {
			console.warn("[mapmaker]", msg);
		}
	}

	function isTextEditingTarget(t) {
		if (!t || !(t instanceof HTMLElement)) return false;
		const tag = t.tagName;
		return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || t.isContentEditable;
	}

	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", init);
	} else {
		init();
	}
})();
