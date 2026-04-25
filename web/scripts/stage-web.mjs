// Boxland — stage the freshly-built Vite bundle into the Go server's
// embed tree.
//
// Cross-platform alternative to PowerShell-only Copy-Item: pure Node
// fs APIs work the same on Windows/macOS/Linux. Called by
// `just _stage-web` after `npm run build` so /static/web/*.js resolves
// at runtime; the production Docker image does the same copy in its
// multi-stage `COPY --from=web-stage` step.

import { existsSync, mkdirSync, readdirSync, rmSync, cpSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const repoRoot = resolve(here, "..", "..");
const src = resolve(repoRoot, "web", "dist");
const dst = resolve(repoRoot, "server", "static", "web");

if (!existsSync(src)) {
	process.stderr.write(`stage-web: source missing at ${src}\nDid you run \`npm run build\` first?\n`);
	process.exit(1);
}

// Wipe everything except .gitkeep so the embed directive's target dir
// stays present across runs (an empty directory makes //go:embed
// all:web fail).
if (existsSync(dst)) {
	for (const entry of readdirSync(dst)) {
		if (entry === ".gitkeep") continue;
		rmSync(resolve(dst, entry), { recursive: true, force: true });
	}
} else {
	mkdirSync(dst, { recursive: true });
}

// Recursive copy preserves directory structure (chunks/, *.js.map, etc.).
// Node 16+ has cpSync built in; we already require Node 20+ for Vite.
for (const entry of readdirSync(src)) {
	cpSync(resolve(src, entry), resolve(dst, entry), { recursive: true });
}

const _ = dirname; // imported for future use; silences unused-import lint
process.stdout.write(`stage-web: ${src} -> ${dst}\n`);
