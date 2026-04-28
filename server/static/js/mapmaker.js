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
		canvas.style.maxWidth  = "none";
		canvas.style.maxHeight = "none";
		const ctx = canvas.getContext("2d");
		if (!ctx) return;
		ctx.imageSmoothingEnabled = false;

		const state = {
			tool:        "brush", // brush | rect | fill | eyedrop | eraser | lock
			activeLayer: Number(canvas.getAttribute("data-default-layer-id")) || 0,
			activeEntity: 0,
			activeRotation: 0,
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
			sampleRect: null,    // {x,y,width,height} cell coords; the procedural
			                      // engine's current sample area, drawn as a yellow
			                      // dashed frame so the designer always sees what's
			                      // being sampled.
			// lockedCells is keyed "L:x:y" same as tiles. Loaded once on
			// boot via /design/maps/{id}/locks; the Lock tool mutates it
			// + ships diffs back to the server.
			lockedCells: new Map(),
			pending: 0,          // outstanding network requests
			// Undo/redo. Each entry captures the inverse + forward diff
			// for one stroke (brush drag, rect, fill, eraser drag, lock
			// gesture). Applying restores both state.tiles and
			// state.lockedCells, then ships the diff to the existing
			// /tiles + /locks endpoints — the server's POST upserts so
			// no new endpoints are needed.
			// Cap at HISTORY_LIMIT so a marathon session can't blow
			// memory; oldest entries fall off the bottom.
			undoStack: [],
			redoStack: [],
		};
		const HISTORY_LIMIT = 100;

		readPalette(state);
		renderPaletteThumbs(state);
		bindTools(state);
		bindRotation(state);
		bindLayers(state, canvas);
		bindPalette(state);
		bindCanvas(state, canvas, ctx);
		bindHistory(state);
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

		// Locks (procedural maps only). Failure is non-fatal — authored-
		// mode maps simply have an empty lock set and the Lock tool isn't
		// rendered anyway.
		loadLocks(mapId)
			.then((cells) => {
				for (const c of cells) state.lockedCells.set(tileKey(c), c);
				draw(state, ctx, canvas);
			})
			.catch(() => { /* swallow: 404 on authored maps is fine */ });

		// Public surface for other modules (procedural overlay, future
		// tile-group stamps) via a CustomEvent layer. Keeps coupling
		// observable in the dev console.
		canvas.addEventListener("bx:mapmaker-redraw", () => draw(state, ctx, canvas));

		// "Clear all locks" from the procedural panel: drop every lock
		// without removing the underlying tiles.
		canvas.addEventListener("bx:locks-cleared", () => {
			state.lockedCells.clear();
			draw(state, ctx, canvas);
		});

		// Expose for the procedural module's commit success path: it can
		// fire bx:mapmaker-reload to re-pull the canonical tiles after
		// a WFC commit lands.
		canvas.addEventListener("bx:mapmaker-reload", () => {
			loadTiles(mapId).then((tiles) => {
				state.tiles.clear();
				const normalized = tiles.map(normalizeTile).filter(Boolean);
				for (const t of normalized) state.tiles.set(tileKey(t), t);
				prefetchImages(state, normalized);
				// Procedural commits replace the map wholesale — old
				// history entries point at cells that no longer exist
				// in their pre-image form.
				clearHistory(state);
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

		function loadLocks(id) {
			return fetch(`/design/maps/${id}/locks`, {
				headers: { Accept: "application/json" },
				credentials: "same-origin",
			})
				.then((r) => {
					if (!r.ok) throw new Error(`HTTP ${r.status}`);
					return r.json();
				})
				.then((j) => Array.isArray(j.cells) ? j.cells.map((c) => ({
					layerId: c.layer_id, x: c.x, y: c.y,
					entityTypeId: c.entity_type_id, rotationDegrees: c.rotation_degrees || 0,
				})) : []);
		}
		function postLocks(cells) {
			if (cells.length === 0) return Promise.resolve();
			state.pending++;
			updateStatus(state);
			const wire = cells.map((c) => ({
				layer_id: c.layerId, x: c.x, y: c.y,
				entity_type_id: c.entityTypeId, rotation_degrees: c.rotationDegrees || 0,
			}));
			return fetchJSON(`/design/maps/${mapId}/locks`, "POST", { cells: wire })
				.finally(() => { state.pending--; updateStatus(state); broadcastLockCount(); });
		}
		function deleteLocks(layerId, points) {
			if (points.length === 0) return Promise.resolve();
			state.pending++;
			updateStatus(state);
			return fetchJSON(`/design/maps/${mapId}/locks`, "DELETE", { layer_id: layerId, points })
				.finally(() => { state.pending--; updateStatus(state); broadcastLockCount(); });
		}
		function broadcastLockCount() {
			document.dispatchEvent(new CustomEvent("bx:locks-changed", {
				detail: { count: state.lockedCells.size },
			}));
		}

		// Stamp = the meaning of "click here" for the current tool.
		// Returns { placed: [...], erased: [...], locked: [...], unlocked: [...] }
		// Mutations are optimistic; the network roundtrip happens in finishStroke.
		//
		// `strokeCtx` (optional) is a per-stroke object the caller uses to
		// remember the pre-image of every cell this stroke touches, so
		// finishStroke can build a single inverse-op history entry. The
		// "first touch wins" semantics in capturePreImages() ensure that
		// dragging back over a cell within the same stroke doesn't
		// overwrite the genuine pre-stroke snapshot.
		function stamp(cellX, cellY, modifiers, strokeCtx) {
			const layerId = state.activeLayer;
			if (!layerId) {
				flash("Pick a layer first.");
				return emptyStamp();
			}
			if (cellX < 0 || cellY < 0 || cellX >= mapW || cellY >= mapH) return emptyStamp();
			modifiers = modifiers || {};

			if (state.tool === "eraser") {
				const k = tileKey({ layerId, x: cellX, y: cellY });
				if (!state.tiles.has(k)) return emptyStamp();
				capturePreImages(strokeCtx, state, layerId, cellX, cellY);
				state.tiles.delete(k);
				return { placed: [], erased: [{ layerId, x: cellX, y: cellY }], locked: [], unlocked: [] };
			}
			if (state.tool === "eyedrop") {
				const k = tileKey({ layerId, x: cellX, y: cellY });
				const t = state.tiles.get(k);
				if (t && t.entityTypeId) {
					setActiveEntity(state, t.entityTypeId);
					setRotation(state, t.rotationDegrees || 0);
				}
				return emptyStamp();
			}
			if (state.tool === "lock") {
				const key = tileKey({ layerId, x: cellX, y: cellY });
				// Modifiers: shift = unlock only (leave the tile);
				// alt = unlock and erase the tile beneath; default = paint+lock.
				if (modifiers.shift || modifiers.alt) {
					const had = state.lockedCells.has(key);
					if (!had && !modifiers.alt) return emptyStamp();
					capturePreImages(strokeCtx, state, layerId, cellX, cellY);
					state.lockedCells.delete(key);
					const out = { placed: [], erased: [], locked: [], unlocked: [] };
					if (had) out.unlocked.push({ layerId, x: cellX, y: cellY });
					if (modifiers.alt && state.tiles.has(key)) {
						state.tiles.delete(key);
						out.erased.push({ layerId, x: cellX, y: cellY });
					}
					return out;
				}
				if (!state.activeEntity) {
					flash("Pick an entity to lock here.");
					return emptyStamp();
				}
				// Lock = place the tile AND mark it locked. Re-locking the
				// same cell with a new entity replaces both.
				capturePreImages(strokeCtx, state, layerId, cellX, cellY);
				const t = { layerId, x: cellX, y: cellY, entityTypeId: state.activeEntity, rotationDegrees: state.activeRotation };
				state.tiles.set(key, t);
				state.lockedCells.set(key, t);
				return { placed: [t], erased: [], locked: [t], unlocked: [] };
			}
			if (!state.activeEntity) {
				flash("Pick an entity from the palette.");
				return emptyStamp();
			}
			capturePreImages(strokeCtx, state, layerId, cellX, cellY);
			const t = { layerId, x: cellX, y: cellY, entityTypeId: state.activeEntity, rotationDegrees: state.activeRotation };
			state.tiles.set(tileKey(t), t);
			return { placed: [t], erased: [], locked: [], unlocked: [] };
		}

		function emptyStamp() {
			return { placed: [], erased: [], locked: [], unlocked: [] };
		}

		// capturePreImages records the BEFORE state of (layerId, x, y) on
		// first touch within a stroke. ctx may be null (e.g. for redoApply
		// flows that already know the inverse) — in that case we skip the
		// capture entirely.
		function capturePreImages(ctx, state, layerId, x, y) {
			if (!ctx) return;
			const k = `${layerId}:${x}:${y}`;
			if (ctx.seen.has(k)) return;
			ctx.seen.add(k);
			const prevTile = state.tiles.get(k);
			const prevLock = state.lockedCells.get(k);
			ctx.prevTiles.set(k, prevTile ? { ...prevTile } : null);
			ctx.prevLocks.set(k, prevLock ? { ...prevLock } : null);
		}

		function newStrokeCtx() {
			return {
				seen: new Set(),
				prevTiles: new Map(), // key -> tile snapshot or null
				prevLocks: new Map(), // key -> lock snapshot or null
			};
		}

		// ---------- Tool dispatch ----------------------------------------

		function bindCanvas(state, canvas, ctx) {
			let dragging = false;
			let dragStart = null;     // {x, y}  for rect tool
			let strokePlaced = [];    // accumulator for brush + rect
			let strokeErased = [];
			let strokeLocked = [];
			let strokeUnlocked = [];
			let strokeCtx = null;     // pre-image snapshots for undo
			let strokeLabel = null;   // human label for the history entry

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
				const mods = { shift: e.shiftKey, alt: e.altKey };

				if (state.tool === "fill") {
					floodFill(state, cell.x, cell.y, mapW, mapH).then(() => draw(state, ctx, canvas));
					dragging = false;
					return;
				}
				if (state.tool === "rect" || state.tool === "sample") {
					dragStart = cell;
					draw(state, ctx, canvas, { rectFrom: dragStart, rectTo: cell });
					return;
				}
				strokeCtx = newStrokeCtx();
				strokeLabel = state.tool;
				const out = stamp(cell.x, cell.y, mods, strokeCtx);
				strokePlaced.push(...out.placed);
				strokeErased.push(...out.erased);
				strokeLocked.push(...out.locked);
				strokeUnlocked.push(...out.unlocked);
				draw(state, ctx, canvas);
			});

			canvas.addEventListener("pointermove", (e) => {
				if (!dragging) return;
				const cell = pointerToCell(e, canvas);
				if (state.tool === "rect" || state.tool === "sample") {
					draw(state, ctx, canvas, { rectFrom: dragStart, rectTo: cell });
					return;
				}
				if (state.tool === "brush" || state.tool === "eraser" || state.tool === "lock") {
					const mods = { shift: e.shiftKey, alt: e.altKey };
					const out = stamp(cell.x, cell.y, mods, strokeCtx);
					strokePlaced.push(...out.placed);
					strokeErased.push(...out.erased);
					strokeLocked.push(...out.locked);
					strokeUnlocked.push(...out.unlocked);
					draw(state, ctx, canvas);
				}
			});

			const finishStroke = (e) => {
				if (!dragging) return;
				dragging = false;
				try { canvas.releasePointerCapture(e.pointerId); } catch (_) {}

				if (state.tool === "rect" && dragStart) {
					strokeCtx = newStrokeCtx();
					strokeLabel = "rect";
					const cell = pointerToCell(e, canvas);
					const r = normalizeRect(dragStart, cell);
					for (let y = r.y0; y <= r.y1; y++) {
						for (let x = r.x0; x <= r.x1; x++) {
							const out = stamp(x, y, null, strokeCtx);
							strokePlaced.push(...out.placed);
							strokeErased.push(...out.erased);
							strokeLocked.push(...out.locked);
							strokeUnlocked.push(...out.unlocked);
						}
					}
					dragStart = null;
				}
				if (state.tool === "sample" && dragStart) {
					// Sample tool: bubble the dragged rectangle up to the
					// procedural module via a CustomEvent. mapmaker.js
					// stays oblivious to the procedural API surface — the
					// procedural module owns the POST.
					const cell = pointerToCell(e, canvas);
					const r = normalizeRect(dragStart, cell);
					const detail = {
						x: r.x0,
						y: r.y0,
						width: r.x1 - r.x0 + 1,
						height: r.y1 - r.y0 + 1,
					};
					canvas.dispatchEvent(new CustomEvent("bx:procedural-sample-drawn", {
						bubbles: true, detail,
					}));
					dragStart = null;
				}

				const placedWire = strokePlaced.map(toWire);
				const erasedPoints = strokeErased.map((t) => [t.x, t.y]);
				const lockedWire = strokeLocked.slice();
				const unlockedPoints = strokeUnlocked.map((t) => [t.x, t.y]);
				const layerId = state.activeLayer;
				const finishedCtx = strokeCtx;
				const finishedLabel = strokeLabel;
				strokePlaced = [];
				strokeErased = [];
				strokeLocked = [];
				strokeUnlocked = [];
				strokeCtx = null;
				strokeLabel = null;
				draw(state, ctx, canvas);

				// Record the inverse op for undo. Skip empty strokes
				// (e.g. eyedrop, no-op clicks) so an idle click on the
				// canvas doesn't push a "do nothing" history entry.
				if (finishedCtx && finishedCtx.seen.size > 0) {
					pushHistory(state, buildHistoryEntry(finishedLabel || "stroke", finishedCtx, state));
				}

				const tasks = [];
				if (placedWire.length > 0) tasks.push(postTiles(placedWire));
				if (erasedPoints.length > 0) tasks.push(deleteTiles(layerId, erasedPoints));
				if (lockedWire.length > 0) tasks.push(postLocks(lockedWire));
				if (unlockedPoints.length > 0) tasks.push(deleteLocks(layerId, unlockedPoints));
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

		// Hotkeys: B / R / F / I / E / L / S mirror the toolbar tooltips.
		// L (Lock) and S (Sample) are only meaningful on procedural maps
		// (the toolbar omits the buttons on authored maps); the hotkeys
		// still flip state safely because stamp() flashes "pick a layer
		// first" if there's nothing to lock onto, and the sample tool's
		// drag is a no-op until the procedural module responds to the
		// resulting bx:procedural-sample-drawn event.
		// T rotates the active tile stamp.
		// H opens the per-realm HUD editor.
		document.addEventListener("keydown", (e) => {
			if (isTextEditingTarget(e.target)) return;
			const k = e.key.toLowerCase();
			const mod = e.ctrlKey || e.metaKey;

			// Undo / Redo. Take precedence over the tool-letter map so
			// Ctrl+Z never selects a tool and Ctrl+Y never fires past
			// the redo. docs/hotkeys.md is the canonical reference.
			if (mod && k === "z" && !e.shiftKey && !e.altKey) { undo(state); e.preventDefault(); return; }
			if (mod && k === "z" &&  e.shiftKey && !e.altKey) { redo(state); e.preventDefault(); return; }
			if (mod && k === "y" && !e.shiftKey && !e.altKey) { redo(state); e.preventDefault(); return; }

			const map = { b: "brush", r: "rect", f: "fill", i: "eyedrop", e: "eraser", l: "lock", s: "sample" };
			if (map[k] && !mod && !e.altKey) { setTool(state, map[k]); e.preventDefault(); return; }
			if (k === "t" && !mod && !e.altKey) { cycleRotation(state); e.preventDefault(); return; }
			// H used to jump to the per-map HUD editor, but HUD lives
			// on LEVELs in the holistic redesign; a map can back many
			// levels (each with its own HUD), so a map-scoped HUD
			// hotkey isn't unambiguous. Designers reach the HUD from
			// the level editor's HUD tab.
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
			const fillCtx = newStrokeCtx();
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
					if (cur) {
						capturePreImages(fillCtx, state, layerId, x, y);
						state.tiles.delete(k);
						erased.push({ layerId, x, y });
					}
				} else {
					capturePreImages(fillCtx, state, layerId, x, y);
					const t = { layerId, x, y, entityTypeId: target, rotationDegrees: state.activeRotation };
					state.tiles.set(k, t);
					placed.push(t);
				}
				queue.push([x+1, y], [x-1, y], [x, y+1], [x, y-1]);
			}
			if (safety >= 4096) flash("Fill stopped at 4096 cells (safety cap).");

			if (fillCtx.seen.size > 0) {
				pushHistory(state, buildHistoryEntry("fill", fillCtx, state));
			}

			const tasks = [];
			if (placed.length > 0) tasks.push(postTiles(placed.map(toWire)));
			if (erased.length > 0) tasks.push(deleteTiles(layerId, erased.map((t) => [t.x, t.y])));
			return Promise.all(tasks).catch((err) => flash(`Save failed: ${err.message || err}`));
		}

		// ---------- Undo / Redo -----------------------------------------
		//
		// History entries store the BEFORE and AFTER state of every cell
		// the stroke touched, on both the tile map and the lock map.
		// Apply() rewrites state.tiles + state.lockedCells from the
		// snapshot side, then ships the diff to the existing endpoints.
		// The /tiles POST handler upserts, so re-placing a previously
		// modified tile just rewrites the row — no add-only/delete-only
		// special casing.

		function buildHistoryEntry(label, ctx, state) {
			// "after" = whatever's in state right now (post-stroke).
			const beforeTiles = new Map();
			const beforeLocks = new Map();
			const afterTiles  = new Map();
			const afterLocks  = new Map();
			for (const k of ctx.seen) {
				beforeTiles.set(k, ctx.prevTiles.get(k) || null);
				beforeLocks.set(k, ctx.prevLocks.get(k) || null);
				const curTile = state.tiles.get(k);
				const curLock = state.lockedCells.get(k);
				afterTiles.set(k, curTile ? { ...curTile } : null);
				afterLocks.set(k, curLock ? { ...curLock } : null);
			}
			return { label, beforeTiles, beforeLocks, afterTiles, afterLocks };
		}

		function pushHistory(state, entry) {
			state.undoStack.push(entry);
			while (state.undoStack.length > HISTORY_LIMIT) state.undoStack.shift();
			state.redoStack.length = 0;
			updateHistoryButtons(state);
		}

		function clearHistory(state) {
			state.undoStack.length = 0;
			state.redoStack.length = 0;
			updateHistoryButtons(state);
		}

		// applyHistorySide rewrites local state to the "before" or
		// "after" snapshot of an entry, returns the diff to ship to
		// the server. side === "before" is undo, "after" is redo.
		function applyHistorySide(state, entry, side) {
			const tilesSnap = side === "before" ? entry.beforeTiles : entry.afterTiles;
			const locksSnap = side === "before" ? entry.beforeLocks : entry.afterLocks;
			const placed = [];
			const erased = [];   // {layerId, x, y}
			const locked = [];
			const unlocked = []; // {layerId, x, y}
			for (const [k, tile] of tilesSnap) {
				const cur = state.tiles.get(k);
				if (tile) {
					state.tiles.set(k, { ...tile });
					// Always re-POST: upsert handles same-payload safely
					// AND covers the "rotation/entity changed" case.
					if (!cur || cur.entityTypeId !== tile.entityTypeId || (cur.rotationDegrees||0) !== (tile.rotationDegrees||0)) {
						placed.push({ ...tile });
					}
				} else if (cur) {
					state.tiles.delete(k);
					erased.push({ layerId: cur.layerId, x: cur.x, y: cur.y });
				}
			}
			for (const [k, lock] of locksSnap) {
				const cur = state.lockedCells.get(k);
				if (lock) {
					state.lockedCells.set(k, { ...lock });
					if (!cur || cur.entityTypeId !== lock.entityTypeId || (cur.rotationDegrees||0) !== (lock.rotationDegrees||0)) {
						locked.push({ ...lock });
					}
				} else if (cur) {
					state.lockedCells.delete(k);
					unlocked.push({ layerId: cur.layerId, x: cur.x, y: cur.y });
				}
			}
			return { placed, erased, locked, unlocked };
		}

		function shipHistoryDiff(diff) {
			// Group erases/unlocks by layerId for the bulk DELETE shape.
			const tasks = [];
			if (diff.placed.length > 0) tasks.push(postTiles(diff.placed.map(toWire)));
			if (diff.locked.length > 0) tasks.push(postLocks(diff.locked));
			const erasedByLayer = groupByLayer(diff.erased);
			for (const [layerId, points] of erasedByLayer) {
				tasks.push(deleteTiles(layerId, points));
			}
			const unlockedByLayer = groupByLayer(diff.unlocked);
			for (const [layerId, points] of unlockedByLayer) {
				tasks.push(deleteLocks(layerId, points));
			}
			if (tasks.length === 0) return Promise.resolve();
			return Promise.all(tasks).catch((err) => {
				flash(`Save failed: ${err.message || err}`);
			});
		}

		function groupByLayer(items) {
			const out = new Map();
			for (const it of items) {
				if (!out.has(it.layerId)) out.set(it.layerId, []);
				out.get(it.layerId).push([it.x, it.y]);
			}
			return out;
		}

		function undo(state) {
			const entry = state.undoStack.pop();
			if (!entry) return;
			const diff = applyHistorySide(state, entry, "before");
			state.redoStack.push(entry);
			updateHistoryButtons(state);
			draw(state, ctx, canvas);
			flash(`Undo: ${entry.label} (${entry.beforeTiles.size} cells)`);
			shipHistoryDiff(diff);
			broadcastLockCount();
		}

		function redo(state) {
			const entry = state.redoStack.pop();
			if (!entry) return;
			const diff = applyHistorySide(state, entry, "after");
			state.undoStack.push(entry);
			updateHistoryButtons(state);
			draw(state, ctx, canvas);
			flash(`Redo: ${entry.label} (${entry.afterTiles.size} cells)`);
			shipHistoryDiff(diff);
			broadcastLockCount();
		}

		function updateHistoryButtons(state) {
			const ub = $("[data-bx-history-undo]");
			const rb = $("[data-bx-history-redo]");
			if (ub) {
				ub.disabled = state.undoStack.length === 0;
				ub.title = state.undoStack.length > 0
					? `Undo ${state.undoStack[state.undoStack.length-1].label} (Ctrl+Z)`
					: "Nothing to undo (Ctrl+Z)";
			}
			if (rb) {
				rb.disabled = state.redoStack.length === 0;
				rb.title = state.redoStack.length > 0
					? `Redo ${state.redoStack[state.redoStack.length-1].label} (Ctrl+Shift+Z)`
					: "Nothing to redo (Ctrl+Shift+Z)";
			}
		}

		function bindHistory(state) {
			const ub = $("[data-bx-history-undo]");
			const rb = $("[data-bx-history-redo]");
			if (ub) ub.addEventListener("click", () => undo(state));
			if (rb) rb.addEventListener("click", () => redo(state));
			updateHistoryButtons(state);
		}

		// Expose for procedural commit / reload paths: those replace
		// the canonical map wholesale, so any locally-staged history
		// entries no longer make sense as inverses.
		canvas.addEventListener("bx:mapmaker-history-clear", () => clearHistory(state));

		// ---------- Inputs (toolbar / layers / palette) ------------------

		function bindTools(state) {
			$$(".bx-mapmaker__tools [data-bx-tool]").forEach((btn) => {
				btn.addEventListener("click", () => setTool(state, btn.getAttribute("data-bx-tool")));
			});
			highlightTool(state.tool);
		}
		function bindRotation(state) {
			const btn = $("[data-bx-rotate-tile]");
			if (btn) btn.addEventListener("click", () => cycleRotation(state));
			updateRotationButton(state);
		}
		function cycleRotation(state) {
			setRotation(state, (state.activeRotation + 90) % 360);
		}
		function setRotation(state, degrees) {
			state.activeRotation = normalizeRotation(degrees);
			updateRotationButton(state);
			updateStatus(state);
		}
		function updateRotationButton(state) {
			const btn = $("[data-bx-rotate-tile]");
			if (btn) btn.textContent = `⟳ ${state.activeRotation}°`;
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
			// Folder-tree disclosure: clicking the group head toggles
			// the child <ol> in place. The default open/closed state
			// is set by the templ via the `hidden` attribute. We
			// preserve the expanded state across re-paints in memory
			// only — refresh resets it to the templ's default.
			$$("[data-bx-palette-group-toggle]").forEach((btn) => {
				btn.addEventListener("click", (e) => {
					e.stopPropagation();
					const li = btn.closest("[data-bx-palette-group]");
					if (!li) return;
					const sub = li.querySelector(":scope > ol");
					if (!sub) return;
					const open = !sub.hasAttribute("hidden");
					if (open) {
						sub.setAttribute("hidden", "");
						btn.setAttribute("aria-expanded", "false");
						const tri = btn.querySelector(".bx-folder-rail__disclose");
						if (tri) tri.textContent = "▶";
					} else {
						sub.removeAttribute("hidden");
						btn.setAttribute("aria-expanded", "true");
						const tri = btn.querySelector(".bx-folder-rail__disclose");
						if (tri) tri.textContent = "▼";
					}
				});
			});

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
					// Show/hide leaves first.
					$$(".bx-mapmaker__palette li[data-bx-entity-type-id]").forEach((li) => {
						const name = (li.getAttribute("data-bx-palette-name") || "").toLowerCase();
						li.style.display = !q || name.includes(q) ? "" : "none";
					});
					// When searching, auto-expand any folder that
					// contains a visible match so the user sees their
					// hits without manual disclosure clicks. When the
					// search clears, leave folder open/closed state
					// alone — designer's choices stick.
					if (q) {
						$$("[data-bx-palette-group]").forEach((grp) => {
							const sub = grp.querySelector(":scope > ol");
							if (!sub) return;
							const hasVisibleHit = !!sub.querySelector(
								'li[data-bx-entity-type-id]:not([style*="display: none"])'
							);
							if (hasVisibleHit) {
								sub.removeAttribute("hidden");
								const tri = grp.querySelector(".bx-folder-rail__disclose");
								if (tri) tri.textContent = "▼";
								const btn = grp.querySelector("[data-bx-palette-group-toggle]");
								if (btn) btn.setAttribute("aria-expanded", "true");
								grp.style.display = "";
							} else {
								grp.style.display = "none";
							}
						});
					} else {
						// Restore folder visibility when search clears.
						$$("[data-bx-palette-group]").forEach((grp) => {
							grp.style.display = "";
						});
					}
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

		function renderPaletteThumbs(state) {
			$$(".bx-mapmaker__palette li[data-bx-entity-type-id]").forEach((li) => {
				const id = Number(li.getAttribute("data-bx-entity-type-id"));
				const meta = state.paletteByEntity.get(id);
				const thumb = $(".bx-mapmaker__palette__thumb", li);
				if (!(thumb instanceof HTMLCanvasElement) || !meta || !meta.spriteUrl) return;
				const ctx = thumb.getContext("2d");
				if (!ctx) return;
				ctx.imageSmoothingEnabled = false;
				ctx.clearRect(0, 0, thumb.width, thumb.height);
				const img = ensureImage(state, meta.spriteUrl);
				if (img && img.complete && img.naturalWidth > 0) drawAtlasCell(ctx, img, meta, 0, 0, thumb.width, thumb.height);
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
			if (!url) return null;
			if (state.images.has(url)) return state.images.get(url);
			const img = new Image();
			img.onload = () => {
				renderPaletteThumbs(state);
				const canvas = $("[data-bx-mapmaker-canvas]");
				if (canvas) canvas.dispatchEvent(new CustomEvent("bx:mapmaker-redraw"));
			};
			img.onerror = () => state.images.set(url, null);
			img.src = url;
			state.images.set(url, img);
			return img;
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
			// Sample-rect overlay: drawn as a persistent dashed yellow
			// frame so the designer can see what's being sampled.
			// Detail shape: { x, y, width, height } in cell coords, or
			// null to clear.
			canvas.addEventListener("bx:procedural-sample-set", (e) => {
				state.sampleRect = e.detail || null;
				const ctx = canvas.getContext("2d");
				if (ctx) draw(state, ctx, canvas);
			});
			canvas.addEventListener("bx:procedural-sample-clear", () => {
				state.sampleRect = null;
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

			// Locked-cell highlight: subtle yellow corner bracket on each
			// locked cell so designers can see at a glance what survives
			// procedural generation. Brackets sit just inside the tile
			// borders so they don't obscure the sprite.
			if (state.lockedCells.size > 0) {
				ctx.save();
				ctx.strokeStyle = "rgba(255, 221, 74, 0.85)"; // var(--bx-accent) at 85%
				ctx.lineWidth = 2;
				const inset = 2;
				const armLen = Math.max(4, Math.floor(TILE_PX / 4));
				for (const c of state.lockedCells.values()) {
					const px = c.x * TILE_PX;
					const py = c.y * TILE_PX;
					ctx.beginPath();
					// top-left
					ctx.moveTo(px + inset, py + inset + armLen); ctx.lineTo(px + inset, py + inset); ctx.lineTo(px + inset + armLen, py + inset);
					// top-right
					ctx.moveTo(px + TILE_PX - inset - armLen, py + inset); ctx.lineTo(px + TILE_PX - inset, py + inset); ctx.lineTo(px + TILE_PX - inset, py + inset + armLen);
					// bottom-right
					ctx.moveTo(px + TILE_PX - inset, py + TILE_PX - inset - armLen); ctx.lineTo(px + TILE_PX - inset, py + TILE_PX - inset); ctx.lineTo(px + TILE_PX - inset - armLen, py + TILE_PX - inset);
					// bottom-left
					ctx.moveTo(px + inset + armLen, py + TILE_PX - inset); ctx.lineTo(px + inset, py + TILE_PX - inset); ctx.lineTo(px + inset, py + TILE_PX - inset - armLen);
					ctx.stroke();
				}
				ctx.restore();
			}

			// Ghost preview from procedural module: draw the actual tile
			// sprites at 70% alpha. The wire shape (entity_type_id) maps
			// straight onto drawTile's expected shape — we just need to
			// project the snake_case fields onto camelCase. Falling back
			// to a soft yellow tint when the entity has no sprite assigned
			// preserves the "this cell is procedural" hint without
			// hiding the rest of the map.
			if (state.procPreview && state.procPreview.cells) {
				ctx.save();
				ctx.globalAlpha = 0.7;
				for (const c of state.procPreview.cells) {
					drawTile(ctx, state, {
						x: c.x,
						y: c.y,
						entityTypeId: c.entity_type_id,
						rotationDegrees: c.rotation_degrees || 0,
					});
				}
				ctx.restore();
			}

			// Procedural sample-rect frame: a yellow dashed outline around
			// the area the engine is sampling from. Set by the procedural
			// module via bx:procedural-sample-set / -clear so the designer
			// always sees what's being sampled.
			if (state.sampleRect) {
				const r = state.sampleRect;
				ctx.save();
				ctx.strokeStyle = "#FFDD4A";
				ctx.lineWidth = 2;
				ctx.setLineDash([6, 4]);
				ctx.strokeRect(
					r.x * TILE_PX + 1,
					r.y * TILE_PX + 1,
					r.width * TILE_PX - 2,
					r.height * TILE_PX - 2,
				);
				ctx.setLineDash([]);
				// Tiny "sample" label in the corner.
				ctx.fillStyle = "rgba(0,0,0,0.65)";
				ctx.fillRect(r.x * TILE_PX, r.y * TILE_PX - 14, 52, 14);
				ctx.fillStyle = "#FFDD4A";
				ctx.font = "10px monospace";
				ctx.fillText("sample", r.x * TILE_PX + 4, r.y * TILE_PX - 3);
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
			drawAtlasCell(ctx, img, meta, px, py, TILE_PX, TILE_PX, t.rotationDegrees || 0);
		}

		function drawAtlasCell(ctx, img, meta, dx, dy, dw, dh, rotationDegrees) {
			// Slice the source sheet to the entity's atlas cell.
			// (cellPx, cols) come from the asset's tile-sheet metadata
			// at upload time; single-frame sprites collapse to (32, 1).
			const cellPx = meta.tileSize || 32;
			const cols = Math.max(1, meta.atlasCols || 1);
			const sx = (meta.atlasIndex % cols) * cellPx;
			const sy = Math.floor(meta.atlasIndex / cols) * cellPx;
			const rot = normalizeRotation(rotationDegrees || 0);
			if (!rot) {
				ctx.drawImage(img, sx, sy, cellPx, cellPx, dx, dy, dw, dh);
				return;
			}
			ctx.save();
			ctx.translate(dx + dw / 2, dy + dh / 2);
			ctx.rotate(rot * Math.PI / 180);
			ctx.drawImage(img, sx, sy, cellPx, cellPx, -dw / 2, -dh / 2, dw, dh);
			ctx.restore();
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
					const label = meta ? meta.name : `#${state.activeEntity}`;
					dirty.textContent = `painting: ${label} · ${state.activeRotation}°`;
				} else dirty.textContent = `pick an entity · ${state.activeRotation}°`;
			}
		}
	}

	// ---- helpers -------------------------------------------------------

	function tileKey(t) { return `${t.layerId}:${t.x}:${t.y}`; }
	function normalizeTile(t) {
		if (!t) return null;
		const layerId = Number(t.layerId ?? t.layer_id);
		const entityTypeId = Number(t.entityTypeId ?? t.entity_type_id);
		const rotationDegrees = normalizeRotation(t.rotationDegrees ?? t.rotation_degrees ?? 0);
		const x = Number(t.x);
		const y = Number(t.y);
		if (!layerId || !entityTypeId || !Number.isFinite(x) || !Number.isFinite(y)) return null;
		return { layerId, x, y, entityTypeId, rotationDegrees };
	}
	function toWire(t) {
		return { layer_id: t.layerId, x: t.x, y: t.y, entity_type_id: t.entityTypeId, rotation_degrees: normalizeRotation(t.rotationDegrees || 0) };
	}

	function normalizeRotation(degrees) {
		const n = Number(degrees) || 0;
		const r = ((n % 360) + 360) % 360;
		return r === 90 || r === 180 || r === 270 ? r : 0;
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
