// Boxland — collider overlay for the Entity Manager detail editor.
//
// Renders a 1-px outline of the entity-type AABB on top of its sprite so
// designers can see where the collider lives relative to the art. Updates
// in real time when the form's collider_w/h/anchor_x/anchor_y inputs
// change (so the visual catches up before the draft is saved).
//
// Lives in @entity-manager. Auto-attaches to any
// <canvas data-bx-collider-overlay> on the page.

const COLOR_OUTLINE = "#ffd34a"; // brand accent

interface OverlayInputs {
	spriteURL: string;
	colliderW: number;
	colliderH: number;
	anchorX: number;
	anchorY: number;
}

function readDataset(canvas: HTMLCanvasElement): OverlayInputs {
	return {
		spriteURL: canvas.dataset.spriteUrl ?? "",
		colliderW: Number(canvas.dataset.colliderW || "0"),
		colliderH: Number(canvas.dataset.colliderH || "0"),
		anchorX: Number(canvas.dataset.anchorX || "0"),
		anchorY: Number(canvas.dataset.anchorY || "0"),
	};
}

/**
 * Draw the overlay onto canvas using the supplied inputs. Idempotent;
 * fully replaces the canvas's pixels each call.
 */
export function drawColliderOverlay(canvas: HTMLCanvasElement, inputs: OverlayInputs): void {
	const ctx = canvas.getContext("2d");
	if (!ctx) return;

	const W = canvas.width;
	const H = canvas.height;
	ctx.imageSmoothingEnabled = false;
	ctx.fillStyle = "#1a1733";
	ctx.fillRect(0, 0, W, H);

	const drawOutline = (): void => {
		// Fit the entity's bounds inside the canvas. Center the sprite.
		// Treat the AABB anchor as offset from the sprite top-left in *pixels*.
		const spriteAssumedSize = Math.max(inputs.colliderW + 8, inputs.colliderH + 8, 32);
		const scale = Math.max(1, Math.floor(Math.min(W, H) / spriteAssumedSize));
		const offsetX = Math.floor((W - spriteAssumedSize * scale) / 2);
		const offsetY = Math.floor((H - spriteAssumedSize * scale) / 2);

		ctx.strokeStyle = COLOR_OUTLINE;
		ctx.lineWidth = 1;
		ctx.strokeRect(
			offsetX + (spriteAssumedSize / 2 - inputs.anchorX) * scale + 0.5,
			offsetY + (spriteAssumedSize / 2 - inputs.anchorY) * scale + 0.5,
			Math.max(1, inputs.colliderW * scale),
			Math.max(1, inputs.colliderH * scale),
		);
	};

	if (!inputs.spriteURL) {
		drawOutline();
		return;
	}

	const img = new Image();
	img.crossOrigin = "anonymous";
	img.onload = () => {
		const spriteW = img.naturalWidth;
		const spriteH = img.naturalHeight;
		const scale = Math.max(1, Math.floor(Math.min(W, H) / Math.max(spriteW, spriteH)));
		const offsetX = Math.floor((W - spriteW * scale) / 2);
		const offsetY = Math.floor((H - spriteH * scale) / 2);
		ctx.drawImage(img, 0, 0, spriteW, spriteH, offsetX, offsetY, spriteW * scale, spriteH * scale);

		ctx.strokeStyle = COLOR_OUTLINE;
		ctx.lineWidth = 1;
		ctx.strokeRect(
			offsetX + (spriteW / 2 - inputs.anchorX) * scale + 0.5,
			offsetY + (spriteH / 2 - inputs.anchorY) * scale + 0.5,
			Math.max(1, inputs.colliderW * scale),
			Math.max(1, inputs.colliderH * scale),
		);
	};
	img.onerror = () => drawOutline();
	img.src = inputs.spriteURL;
}

/**
 * Auto-attach to every overlay canvas on the page and wire it to live-update
 * when the surrounding form's collider_w/h/anchor inputs change.
 */
export function autoMountColliderOverlays(root: Document | HTMLElement = document): void {
	const list = root.querySelectorAll<HTMLCanvasElement>("canvas[data-bx-collider-overlay]");
	for (const canvas of list) {
		const update = (): void => drawColliderOverlay(canvas, readDataset(canvas));
		update();

		// Find the nearest enclosing form and listen for input changes.
		const form = canvas.closest(".bx-modal__body")?.querySelector("form");
		if (!form) continue;
		form.addEventListener("input", (e) => {
			const t = e.target as HTMLInputElement;
			if (!t || !t.name) return;
			switch (t.name) {
				case "collider_w":
					canvas.dataset.colliderW = t.value;
					break;
				case "collider_h":
					canvas.dataset.colliderH = t.value;
					break;
				case "collider_anchor_x":
					canvas.dataset.anchorX = t.value;
					break;
				case "collider_anchor_y":
					canvas.dataset.anchorY = t.value;
					break;
				default:
					return;
			}
			update();
		});
	}
}
