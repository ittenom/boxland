// Boxland — Asset Manager animation preview.
//
// Per PLAN.md §5c: "animation preview (Canvas2D loop)". This is a tiny
// pure-Canvas2D renderer driven by sheet metadata stored on the asset
// row. It runs only inside the Asset Manager detail panel; the actual
// game uses the Pixi renderer.
//
// Activation: any <canvas data-bx-asset-preview="<asset-id>"
//                       data-asset-url="..."
//                       data-grid-w="32" data-grid-h="32"
//                       data-fps="8"> on the page is auto-attached.

export interface PreviewOptions {
	url: string;
	gridW: number;
	gridH: number;
	fps: number;
	loop?: boolean;
}

/**
 * Mount a Canvas2D animation preview onto the given canvas element. Returns
 * a stop() callback the caller can invoke to release the requestAnimationFrame.
 */
export function mountPreview(canvas: HTMLCanvasElement, opts: PreviewOptions): () => void {
	const ctx = canvas.getContext("2d");
	if (!ctx) return () => undefined;
	ctx.imageSmoothingEnabled = false;

	canvas.width = opts.gridW;
	canvas.height = opts.gridH;
	canvas.style.imageRendering = "pixelated";

	let stopped = false;
	let frame = 0;
	let frameCount = 1;
	let lastTime = performance.now();
	const frameMS = 1000 / Math.max(1, opts.fps);

	const img = new Image();
	img.crossOrigin = "anonymous";
	img.onload = () => {
		// Frame count = floor(width / gridW) * floor(height / gridH).
		const cols = Math.max(1, Math.floor(img.naturalWidth / opts.gridW));
		const rows = Math.max(1, Math.floor(img.naturalHeight / opts.gridH));
		frameCount = cols * rows;
		requestAnimationFrame(tick);
	};
	img.src = opts.url;

	function tick(now: number): void {
		if (stopped) return;
		if (now - lastTime >= frameMS) {
			lastTime = now;
			frame = (frame + 1) % frameCount;
			draw();
		}
		requestAnimationFrame(tick);
	}

	function draw(): void {
		if (!ctx) return;
		const cols = Math.max(1, Math.floor(img.naturalWidth / opts.gridW));
		const sx = (frame % cols) * opts.gridW;
		const sy = Math.floor(frame / cols) * opts.gridH;
		ctx.clearRect(0, 0, opts.gridW, opts.gridH);
		ctx.drawImage(img, sx, sy, opts.gridW, opts.gridH, 0, 0, opts.gridW, opts.gridH);
	}

	return () => {
		stopped = true;
	};
}

/**
 * Auto-attach to all matching canvases on the current document.
 * Idempotent: re-running re-binds without leaking RAF callbacks if the
 * caller stored the previous teardown via dataset.bxStop.
 */
export function autoMountPreviews(root: Document | HTMLElement = document): void {
	const list = root.querySelectorAll<HTMLCanvasElement>("canvas[data-bx-asset-preview]");
	for (const canvas of list) {
		const prevStop = (canvas as HTMLCanvasElement & { __bxStop?: () => void }).__bxStop;
		prevStop?.();
		const url = canvas.dataset.assetUrl ?? "";
		const gridW = Number(canvas.dataset.gridW || "32");
		const gridH = Number(canvas.dataset.gridH || "32");
		const fps = Number(canvas.dataset.fps || "8");
		if (!url) continue;
		(canvas as HTMLCanvasElement & { __bxStop?: () => void }).__bxStop =
			mountPreview(canvas, { url, gridW, gridH, fps });
	}
}
