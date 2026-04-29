// Boxland — Pixi texture loading helpers.
//
// Designer asset routes are authenticated same-origin blob URLs such as
// /design/assets/blob/42. They return image/png, but Pixi's resolver picks
// a parser from the URL extension before it fetches headers. Force the
// texture parser for extensionless URLs so editor sprites load normally.

import { Assets, type Texture } from "pixi.js";

const TEXTURE_EXT = /\.(avif|gif|jpeg|jpg|png|svg|webp)(?:$|\?)/i;

export function loadTextureAsset(url: string): Promise<Texture> {
	if (!url) return Promise.reject(new Error("loadTextureAsset: missing URL"));
	const asset = shouldForceTextureParser(url)
		? { alias: url, src: url, parser: "texture" as const }
		: url;
	return Assets.load<Texture>(asset);
}

function shouldForceTextureParser(url: string): boolean {
	if (url.startsWith("data:") || url.startsWith("blob:")) return false;
	return !TEXTURE_EXT.test(url);
}
