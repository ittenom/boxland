// Cross-platform smoke test for the just-helper Node scripts. Runs
// each script in a child process + asserts it exits 0.
//
// Why a *.test.mjs alongside the scripts (instead of vitest in src/)?
// These scripts run OUTSIDE the Vite root and OUTSIDE the TS suite --
// they're plain Node CLIs that have to work on a fresh clone before
// vitest is even installed. Keeping the test alongside (and runnable
// via `node web/scripts/scripts.test.mjs`) makes them survive
// independently of the test toolchain.

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const repoRoot = resolve(here, "..", "..");

let failed = 0;

function check(name, ok, detail = "") {
	if (ok) {
		process.stdout.write(`  ✓ ${name}\n`);
	} else {
		process.stdout.write(`  ✕ ${name}${detail ? "  " + detail : ""}\n`);
		failed++;
	}
}

function runNode(script, opts = {}) {
	return spawnSync(process.execPath, [resolve(here, script)], {
		cwd: opts.cwd ?? repoRoot,
		encoding: "utf8",
	});
}

// --- banner.mjs ---
{
	const r = runNode("banner.mjs");
	check("banner.mjs exits 0", r.status === 0, `status=${r.status} stderr=${r.stderr}`);
	check("banner prints localhost URLs", /localhost:8080/.test(r.stdout));
	check("banner prints /design/login + /play/login",
		r.stdout.includes("/design/login") && r.stdout.includes("/play/login"));
}

// --- stage-web.mjs (requires web/dist/ to exist) ---
{
	const dist = resolve(repoRoot, "web", "dist");
	if (!existsSync(dist)) {
		// Don't fail the whole suite -- the script self-reports a useful
		// error message; we just skip the success path.
		const r = runNode("stage-web.mjs");
		check("stage-web.mjs reports missing dist", r.status === 1 && /source missing/.test(r.stderr));
	} else {
		const r = runNode("stage-web.mjs");
		check("stage-web.mjs exits 0", r.status === 0, `status=${r.status} stderr=${r.stderr}`);
		const staged = resolve(repoRoot, "server", "static", "web", "boot.js");
		check("stage-web.mjs copies boot.js", existsSync(staged));
	}
}

// --- sync-fonts.mjs ---
{
	const r = runNode("sync-fonts.mjs");
	check("sync-fonts.mjs exits 0", r.status === 0, `status=${r.status} stderr=${r.stderr}`);
	const ttf = resolve(repoRoot, "server", "static", "fonts", "C64esque.ttf");
	check("sync-fonts.mjs copies C64esque.ttf", existsSync(ttf));
}

// Ensure the embed dir exists so the Go //go:embed all:web directive
// resolves regardless of test order. Idempotent.
mkdirSync(resolve(repoRoot, "server", "static", "web"), { recursive: true });

if (failed > 0) {
	process.stderr.write(`\n${failed} check(s) failed\n`);
	process.exit(1);
}
process.stdout.write("\nAll script checks passed.\n");
