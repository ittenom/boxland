// Boxland — Character Generator live layered preview.
//
// Pure Canvas2D, nearest-neighbor, single 32x32 frame at a time. Mirrors
// the asset-manager preview pattern but composites multiple sprite
// sheets (one per layered part) into one cell. The bake pipeline
// produces the same composition server-side; this preview is the
// designer's instant feedback before save.

import type { LayeredPart } from "./state";

export const FRAME_SIZE = 32;

interface SourceImage {
    img: HTMLImageElement;
    cols: number;
    loaded: boolean;
}

export class LayerPreview {
    private canvas: HTMLCanvasElement;
    private ctx: CanvasRenderingContext2D;
    private cache = new Map<string, SourceImage>();
    private rafHandle = 0;
    private layers: LayeredPart[] = [];
    private animation = "idle";
    private frame = 0;
    private frameMS = 1000 / 8;
    private lastTime = 0;

    constructor(canvas: HTMLCanvasElement) {
        this.canvas = canvas;
        canvas.width = FRAME_SIZE;
        canvas.height = FRAME_SIZE;
        canvas.style.imageRendering = "pixelated";
        const ctx = canvas.getContext("2d");
        if (!ctx) throw new Error("character preview: 2D context unavailable");
        this.ctx = ctx;
        this.ctx.imageSmoothingEnabled = false;
    }

    /** Update zoom by setting CSS width/height to (FRAME_SIZE * zoom)px. */
    setZoom(zoom: number): void {
        const px = Math.max(1, Math.round(zoom)) * FRAME_SIZE;
        this.canvas.style.width = `${px}px`;
        this.canvas.style.height = `${px}px`;
    }

    /** Replace the layer set. Triggers any new image loads. */
    setLayers(layers: LayeredPart[]): void {
        this.layers = layers;
        for (const l of layers) {
            if (!l.part.asset_url) continue;
            if (this.cache.has(l.part.asset_url)) continue;
            const entry: SourceImage = { img: new Image(), cols: 1, loaded: false };
            entry.img.crossOrigin = "anonymous";
            entry.img.onload = () => {
                entry.cols = Math.max(1, Math.floor(entry.img.naturalWidth / FRAME_SIZE));
                entry.loaded = true;
                this.scheduleDraw();
            };
            entry.img.src = l.part.asset_url;
            this.cache.set(l.part.asset_url, entry);
        }
        this.frame = 0;
        this.scheduleDraw();
    }

    /** Set the active animation key. Resets the frame counter. */
    setAnimation(key: string): void {
        this.animation = key;
        this.frame = 0;
    }

    /** Start the rAF loop. Idempotent. */
    start(): void {
        if (this.rafHandle !== 0) return;
        this.lastTime = performance.now();
        const tick = (now: number) => {
            this.rafHandle = requestAnimationFrame(tick);
            const dt = now - this.lastTime;
            if (dt < this.frameMS) return;
            this.lastTime = now;
            this.frame = (this.frame + 1) % Math.max(1, this.frameCount());
            this.draw();
        };
        this.rafHandle = requestAnimationFrame(tick);
    }

    /** Stop the rAF loop. */
    stop(): void {
        if (this.rafHandle !== 0) {
            cancelAnimationFrame(this.rafHandle);
            this.rafHandle = 0;
        }
    }

    /** Force a single draw without advancing the frame. */
    scheduleDraw(): void {
        // Draw once on the next microtask so back-to-back state changes
        // coalesce.
        queueMicrotask(() => this.draw());
    }

    private frameCount(): number {
        // Common-animation frame count = min over selected layers'
        // frame_map[animation] ranges. Returns 1 when nothing's
        // selected (preview shows a static scene).
        if (this.layers.length === 0) return 1;
        let min = Number.POSITIVE_INFINITY;
        for (const l of this.layers) {
            const fm = l.part.frame_map ?? {};
            const v = fm[this.animation];
            if (v === undefined) return 1; // anim missing on a layer; preview as static frame 0
            const range = Array.isArray(v) ? v : ([v, v] as [number, number]);
            const cnt = range[1] - range[0] + 1;
            if (cnt < min) min = cnt;
        }
        return Number.isFinite(min) && min >= 1 ? min : 1;
    }

    private draw(): void {
        const ctx = this.ctx;
        ctx.clearRect(0, 0, FRAME_SIZE, FRAME_SIZE);
        for (const l of this.layers) {
            const src = this.cache.get(l.part.asset_url);
            if (!src || !src.loaded) continue;
            const fm = l.part.frame_map ?? {};
            const v = fm[this.animation];
            const range: [number, number] = Array.isArray(v) ? v : v !== undefined ? [v, v] : [0, 0];
            const srcFrame = range[0] + this.frame;
            const sx = (srcFrame % src.cols) * FRAME_SIZE;
            const sy = Math.floor(srcFrame / src.cols) * FRAME_SIZE;
            // Defensive: if the source isn't tall/wide enough, skip.
            if (sx + FRAME_SIZE > src.img.naturalWidth || sy + FRAME_SIZE > src.img.naturalHeight) continue;
            ctx.drawImage(src.img, sx, sy, FRAME_SIZE, FRAME_SIZE, 0, 0, FRAME_SIZE, FRAME_SIZE);
        }
    }
}
