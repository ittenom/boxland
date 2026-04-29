// Boxland — Mapmaker renderable layer.
//
// Adapter from the shared renderer's Renderable[] contract to a Pixi
// Container that lives inside the Mapmaker's editor viewport. This is
// intentionally editor-specific: it paints top-left grid cells directly
// while the workbench owns pan/zoom around the map surface.

import { Container, Graphics, Sprite } from "pixi.js";

import { SUB_PER_PX, StaticAssetCatalog, TextureCache, type Renderable } from "@render";

const TILE_SIZE = 32;

export class MapmakerRenderableLayer extends Container {
	private readonly fallbackRoot = new Container();
	private readonly spriteRoot = new Container();
	private readonly fallbacks = new Map<number, Graphics>();
	private readonly sprites = new Map<number, Sprite>();
	private readonly textures: TextureCache;
	private renderGeneration = 0;

	constructor(private readonly catalog: StaticAssetCatalog, mapWidth: number, mapHeight: number) {
		super();
		void mapWidth;
		void mapHeight;
		this.textures = new TextureCache(catalog);
		this.addChild(this.fallbackRoot);
		this.addChild(this.spriteRoot);
	}

	async setRenderables(renderables: readonly Renderable[]): Promise<void> {
		const generation = ++this.renderGeneration;
		const seen = new Set<number>();
		for (const r of renderables) {
			seen.add(r.id);
			this.upsertFallback(r);
		}
		for (const [id, g] of this.fallbacks) {
			if (seen.has(id)) continue;
			this.fallbackRoot.removeChild(g);
			g.destroy();
			this.fallbacks.delete(id);
		}
		for (const [id, s] of this.sprites) {
			if (seen.has(id)) continue;
			this.spriteRoot.removeChild(s);
			s.destroy();
			this.sprites.delete(id);
		}
		sortChildren(this.fallbackRoot);
		await Promise.all(renderables.map((r) => this.upsertSprite(r, generation)));
		if (generation === this.renderGeneration) sortChildren(this.spriteRoot);
	}

	private upsertFallback(r: Renderable): void {
		let g = this.fallbacks.get(r.id);
		if (!g) {
			g = new Graphics();
			this.fallbacks.set(r.id, g);
			this.fallbackRoot.addChild(g);
		}
		drawFallbackTile(g, r);
	}

	private async upsertSprite(r: Renderable, generation: number): Promise<void> {
		if (!this.catalog.urlFor(r.asset_id, r.variant_id ?? 0)) return;
		let sprite = this.sprites.get(r.id);
		if (!sprite) {
			sprite = new Sprite();
			sprite.roundPixels = true;
			this.sprites.set(r.id, sprite);
			this.spriteRoot.addChild(sprite);
		}
		const tex = await this.textures.frame(r.asset_id, r.anim_id, r.anim_frame, r.variant_id ?? 0);
		if (generation !== this.renderGeneration || !this.fallbacks.has(r.id)) return;
		if (!tex) {
			this.spriteRoot.removeChild(sprite);
			sprite.destroy();
			this.sprites.delete(r.id);
			return;
		}
		sprite.texture = tex;
		sprite.width = TILE_SIZE;
		sprite.height = TILE_SIZE;
		sprite.tint = r.tint && (r.tint >>> 8) !== 0 ? (r.tint >>> 8) & 0xffffff : 0xffffff;
		positionSprite(sprite, r);
		sprite.zIndex = r.layer;
	}

	spriteCount(): number {
		return this.sprites.size;
	}

	fallbackCount(): number {
		return this.fallbacks.size;
	}
}

function drawFallbackTile(g: Graphics, r: Renderable): void {
	const x = Math.floor(r.x / SUB_PER_PX);
	const y = Math.floor(r.y / SUB_PER_PX);
	const base = fallbackColor(r.asset_id);
	const tint = r.tint && (r.tint >>> 8) !== 0 ? (r.tint >>> 8) & 0xffffff : base;
	const alpha = r.tint ? Math.max(0.25, (r.tint & 0xff) / 255) : 1;
	g.clear();
	g.rect(0, 0, TILE_SIZE, TILE_SIZE)
		.fill({ color: tint, alpha: 0.85 * alpha })
		.rect(0, 0, TILE_SIZE, TILE_SIZE)
		.stroke({ color: 0x10131c, width: 1, alignment: 1 });
	g.rect(4, 4, TILE_SIZE - 8, TILE_SIZE - 8)
		.fill({ color: lighten(base), alpha: 0.45 * alpha });
	g.position.set(x, y);
	g.zIndex = r.layer;
	const rot = r.rotation ?? 0;
	if (rot !== 0) {
		g.pivot.set(TILE_SIZE / 2, TILE_SIZE / 2);
		g.position.set(x + TILE_SIZE / 2, y + TILE_SIZE / 2);
		g.angle = rot;
	} else {
		g.pivot.set(0, 0);
		g.angle = 0;
	}
}

function fallbackColor(id: number): number {
	const hue = Math.abs((id * 47) % 360);
	const c = 0.62;
	const x = c * (1 - Math.abs(((hue / 60) % 2) - 1));
	const m = 0.20;
	let r = 0, g = 0, b = 0;
	if (hue < 60) { r = c; g = x; }
	else if (hue < 120) { r = x; g = c; }
	else if (hue < 180) { g = c; b = x; }
	else if (hue < 240) { g = x; b = c; }
	else if (hue < 300) { r = x; b = c; }
	else { r = c; b = x; }
	return (Math.round((r + m) * 255) << 16) | (Math.round((g + m) * 255) << 8) | Math.round((b + m) * 255);
}

function lighten(color: number): number {
	const r = Math.min(255, ((color >> 16) & 0xff) + 38);
	const g = Math.min(255, ((color >> 8) & 0xff) + 38);
	const b = Math.min(255, (color & 0xff) + 38);
	return (r << 16) | (g << 8) | b;
}

function positionSprite(sprite: Sprite, r: Renderable): void {
	const x = Math.floor(r.x / SUB_PER_PX);
	const y = Math.floor(r.y / SUB_PER_PX);
	const rot = r.rotation ?? 0;
	if (rot !== 0 && sprite.texture && sprite.texture.width > 0) {
		const halfW = sprite.texture.width / 2;
		const halfH = sprite.texture.height / 2;
		sprite.anchor.set(0.5);
		sprite.angle = rot;
		sprite.position.set(x + halfW, y + halfH);
	} else {
		sprite.anchor.set(0);
		sprite.angle = 0;
		sprite.position.set(x, y);
	}
}

function sortChildren(root: Container): void {
	root.children.sort((a, b) => (a.zIndex ?? 0) - (b.zIndex ?? 0));
}
