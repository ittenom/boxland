// Boxland — web boot entrypoint.
//
// Selects the correct page module based on a body data-attribute set by the
// server-rendered Templ shell. Per-page modules (game, mapmaker, sandbox,
// pixel-editor) are added in later tasks (#116, #104, #131, #61).

// Sentinel so the bundle isn't empty during early development.
export const BOOT_VERSION = "0.0.0-dev";

export function detectSurface(doc: Document = document): string {
  return doc.body?.dataset.surface ?? "unknown";
}

export function boot(doc: Document = document): void {
  const surface = detectSurface(doc);
  console.info(`[boxland] boot, surface=${surface}, version=${BOOT_VERSION}`);
  // Per-surface dispatch lands here in later tasks.
}

// Auto-run when loaded in a browser; tests import the named exports directly.
if (typeof document !== "undefined") {
  boot();
}
