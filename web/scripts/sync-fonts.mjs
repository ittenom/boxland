// Boxland — sync canonical fonts from /shared/fonts/ into the server's
// static dir so they ship with the binary.
//
// Cross-platform alternative to the PowerShell version. /shared/fonts/
// is the source of truth (used by the future iOS bundle too);
// server/static/fonts/ is a build artifact (gitignored).

import { existsSync, mkdirSync, readdirSync, copyFileSync } from "node:fs";
import { resolve, extname } from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const repoRoot = resolve(here, "..", "..");
const src = resolve(repoRoot, "shared", "fonts");
const dst = resolve(repoRoot, "server", "static", "fonts");

if (!existsSync(src)) {
	process.stderr.write(`sync-fonts: source missing at ${src}\n`);
	process.exit(1);
}

mkdirSync(dst, { recursive: true });

let n = 0;
for (const entry of readdirSync(src)) {
	if (extname(entry).toLowerCase() !== ".ttf") continue;
	copyFileSync(resolve(src, entry), resolve(dst, entry));
	n++;
}
process.stdout.write(`sync-fonts: copied ${n} .ttf file(s) from ${src} to ${dst}\n`);
