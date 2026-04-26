// Boxland — entity manager surface JS.
//
// Today: mounts the per-entity preview canvas in the detail modal so
// designers see the same sprite the map renders. The TS module that
// originally owned this lived in /web/src but never got bundled into
// any Vite entry; this static drop-in matches the boot.js / mapmaker.js
// pattern (no build step) so the canvas paints on first render and
// after every HTMX swap.
//
// The canvas reads everything it needs from data-* attributes set by
// views.ColliderOverlay:
//
//   data-sprite-url    full source PNG (whole sheet for tile uploads)
//   data-atlas-index   row-major cell index inside the sheet
//   data-atlas-cols    sheet width in cells; 1 for single-frame sprites
//   data-tile-size     cell size in source pixels; 32 by default
//   data-collider-w/h  AABB size in source pixels
//   data-anchor-x/y    AABB anchor offset from top-left of the cell
//
// Live form-input changes for collider_w/h/anchor_x/anchor_y redraw
// without waiting for a save round-trip — the same UX the tile palette
// gives you.
(() => {
	"use strict";

	const COLOR_OUTLINE = "#ffd34a";   // brand accent
	const BG            = "#1a1733";   // matches .bx-collider-overlay bg

	// Per-URL Image cache so re-mounts (htmx swaps) don't re-fetch the
	// PNG. Keyed by URL because every overlay on a project shares the
	// same handful of source sheets.
	const imageCache = new Map();

	function loadImage(url, onReady) {
		if (!url) return null;
		const hit = imageCache.get(url);
		if (hit) {
			if (hit.complete && hit.naturalWidth > 0) onReady(hit);
			else hit.addEventListener("load", () => onReady(hit), { once: true });
			return hit;
		}
		const img = new Image();
		img.addEventListener("load", () => onReady(img), { once: true });
		img.addEventListener("error", () => imageCache.delete(url), { once: true });
		img.src = url;
		imageCache.set(url, img);
		return img;
	}

	function readDataset(canvas) {
		return {
			spriteURL : canvas.dataset.spriteUrl   || "",
			atlasIndex: Number(canvas.dataset.atlasIndex || "0") || 0,
			atlasCols : Math.max(1, Number(canvas.dataset.atlasCols  || "1") || 1),
			tileSize  : Math.max(1, Number(canvas.dataset.tileSize   || "32") || 32),
			colliderW : Number(canvas.dataset.colliderW || "0") || 0,
			colliderH : Number(canvas.dataset.colliderH || "0") || 0,
			anchorX   : Number(canvas.dataset.anchorX   || "0") || 0,
			anchorY   : Number(canvas.dataset.anchorY   || "0") || 0,
		};
	}

	// Compute integer scale + offset that fits a `srcSize x srcSize`
	// source square into the canvas centered, with crisp pixel-art
	// scaling (no fractional zoom). `pad` reserves room for the AABB
	// outline at large source sizes.
	function fitScale(canvasW, canvasH, srcSize) {
		const max = Math.min(canvasW, canvasH) - 4;
		return Math.max(1, Math.floor(max / Math.max(1, srcSize)));
	}

	function paintChecker(ctx, w, h) {
		// Subtle two-tone checker so transparent sprite cells read
		// against the dark modal background. Matches the asset detail
		// preview aesthetic; tuned dim so the sprite stays the focus.
		ctx.fillStyle = BG;
		ctx.fillRect(0, 0, w, h);
		ctx.fillStyle = "rgba(255,255,255,0.04)";
		const s = 8;
		for (let y = 0; y < h; y += s) {
			for (let x = 0; x < w; x += s) {
				if (((x / s) + (y / s)) % 2 === 0) ctx.fillRect(x, y, s, s);
			}
		}
	}

	function drawOutline(ctx, d, scale, offsetX, offsetY) {
		// Outline is positioned in source-pixel coordinates: the AABB
		// top-left within the cell is (cellW/2 - anchorX, cellH/2 -
		// anchorY) per the engine's anchor convention. We stroke after
		// the sprite so it always wins z-order.
		const cell = d.tileSize;
		const x = offsetX + (cell / 2 - d.anchorX) * scale;
		const y = offsetY + (cell / 2 - d.anchorY) * scale;
		ctx.strokeStyle = COLOR_OUTLINE;
		ctx.lineWidth = 1;
		ctx.strokeRect(
			Math.floor(x) + 0.5,
			Math.floor(y) + 0.5,
			Math.max(1, d.colliderW * scale),
			Math.max(1, d.colliderH * scale),
		);
	}

	function drawOverlay(canvas) {
		const ctx = canvas.getContext("2d");
		if (!ctx) return;
		const W = canvas.width, H = canvas.height;
		ctx.imageSmoothingEnabled = false;
		paintChecker(ctx, W, H);

		const d = readDataset(canvas);
		const scale = fitScale(W, H, d.tileSize);
		const drawnW = d.tileSize * scale;
		const offsetX = Math.floor((W - drawnW) / 2);
		const offsetY = Math.floor((H - drawnW) / 2);

		const renderCell = (img) => {
			// We may have moved on (htmx swap) — bail if the canvas
			// was replaced before the image landed.
			if (!canvas.isConnected) return;
			const sx = (d.atlasIndex % d.atlasCols) * d.tileSize;
			const sy = Math.floor(d.atlasIndex / d.atlasCols) * d.tileSize;
			ctx.drawImage(
				img,
				sx, sy, d.tileSize, d.tileSize,
				offsetX, offsetY, drawnW, drawnW,
			);
			drawOutline(ctx, d, scale, offsetX, offsetY);
		};

		if (!d.spriteURL) {
			drawOutline(ctx, d, scale, offsetX, offsetY);
			return;
		}
		const img = loadImage(d.spriteURL, renderCell);
		if (img && img.complete && img.naturalWidth > 0) {
			renderCell(img);
		} else {
			// Show the outline immediately so the user sees *something*
			// while the sheet downloads.
			drawOutline(ctx, d, scale, offsetX, offsetY);
		}
	}

	// Wire each canvas exactly once. We tag a private flag on the
	// element so repeat calls (initial mount + htmx:afterSwap) don't
	// stack listeners on the surrounding form.
	function mount(root) {
		const canvases = (root || document).querySelectorAll(
			"canvas[data-bx-collider-overlay]"
		);
		for (const canvas of canvases) {
			drawOverlay(canvas);
			if (canvas.__bxOverlayBound) continue;
			canvas.__bxOverlayBound = true;
			const form = canvas
				.closest(".bx-modal__body")
				?.querySelector("form");
			if (!form) continue;
			form.addEventListener("input", (e) => {
				const t = e.target;
				if (!(t instanceof HTMLInputElement) || !t.name) return;
				switch (t.name) {
					case "collider_w":        canvas.dataset.colliderW = t.value; break;
					case "collider_h":        canvas.dataset.colliderH = t.value; break;
					case "collider_anchor_x": canvas.dataset.anchorX   = t.value; break;
					case "collider_anchor_y": canvas.dataset.anchorY   = t.value; break;
					default: return;
				}
				drawOverlay(canvas);
			});
		}
	}

	// First paint: in case the script is parsed after the DOM lands
	// (defer) we run immediately; in case we beat the DOM, we re-run on
	// DOMContentLoaded for safety.
	mount(document);
	document.addEventListener("DOMContentLoaded", () => mount(document));
	// HTMX swaps the modal in for both initial open and component-add
	// re-renders. Re-mount on every swap so the new canvas paints.
	document.body.addEventListener("htmx:afterSwap", (e) => {
		mount(e.target instanceof HTMLElement ? e.target : document);
	});
})();
