// Boxland — integer-scale viewport math.
//
// Per PLAN.md §1: "Always integer-scale (1x/2x/3x...), nearest-neighbor only."
// The renderer keeps a logical "world view" of fixed tile dimensions and
// scales it to fit the available pixel canvas at the largest integer
// multiple. Excess space is letterboxed.

export interface ViewportPx {
	canvasW: number;       // device pixels of the host canvas
	canvasH: number;
	worldViewW: number;    // virtual world view in *world pixels* (1 unit = 1 px)
	worldViewH: number;
}

export interface ViewportLayout {
	scale: number;         // integer >= 1
	offsetX: number;       // device-pixel letterbox offset
	offsetY: number;
	scaledW: number;       // worldViewW * scale (always <= canvasW)
	scaledH: number;
}

/**
 * Compute the integer-scale layout for a given canvas + world view. The
 * scale is the largest k such that worldViewW*k <= canvasW AND
 * worldViewH*k <= canvasH. Always returns at least 1, even if the world
 * view is bigger than the canvas (in which case the player sees a
 * cropped view; preferable to blurry sub-integer scaling).
 */
export function computeLayout(p: ViewportPx): ViewportLayout {
	const sx = Math.max(1, Math.floor(p.canvasW / p.worldViewW));
	const sy = Math.max(1, Math.floor(p.canvasH / p.worldViewH));
	const scale = Math.min(sx, sy);
	const scaledW = p.worldViewW * scale;
	const scaledH = p.worldViewH * scale;
	const offsetX = Math.floor((p.canvasW - scaledW) / 2);
	const offsetY = Math.floor((p.canvasH - scaledH) / 2);
	return { scale, offsetX, offsetY, scaledW, scaledH };
}

/**
 * Convert a sub-pixel world coordinate into integer device pixels for the
 * given layout. Sub-pixel rounds via floor (not nearest) so motion never
 * jitters between two destination pixels at fractional sub-pixel speeds.
 */
export function worldToScreen(
	subX: number, subY: number,
	cameraSubCx: number, cameraSubCy: number,
	layout: ViewportLayout,
	worldViewW: number, worldViewH: number,
	subPerPx: number,
): { x: number; y: number } {
	const worldPxX = Math.floor(subX / subPerPx);
	const worldPxY = Math.floor(subY / subPerPx);
	const cameraPxX = Math.floor(cameraSubCx / subPerPx);
	const cameraPxY = Math.floor(cameraSubCy / subPerPx);
	const halfViewW = Math.floor(worldViewW / 2);
	const halfViewH = Math.floor(worldViewH / 2);
	const screenWorldX = worldPxX - (cameraPxX - halfViewW);
	const screenWorldY = worldPxY - (cameraPxY - halfViewH);
	return {
		x: layout.offsetX + screenWorldX * layout.scale,
		y: layout.offsetY + screenWorldY * layout.scale,
	};
}
