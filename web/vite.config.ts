import { defineConfig } from "vite";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Boxland web bundler.
// Multi-entry: one entry per page surface (game, mapmaker, sandbox, pixel
// editor, designer shell). Shared modules (net/, render/, collision/,
// command-bus/, input/) tree-shake into each entry as needed.
//
// Path aliases mirror tsconfig.json. Keep them in sync.

const here = fileURLToPath(new URL(".", import.meta.url));

export default defineConfig({
  root: "src",
  resolve: {
    alias: {
      "@net": resolve(here, "src/net"),
      "@render": resolve(here, "src/render"),
      "@collision": resolve(here, "src/collision"),
      "@command-bus": resolve(here, "src/command-bus"),
      "@input": resolve(here, "src/input"),
      "@pixel-editor": resolve(here, "src/pixel-editor"),
      "@mapmaker": resolve(here, "src/mapmaker"),
      "@sandbox": resolve(here, "src/sandbox"),
      "@game": resolve(here, "src/game"),
      "@settings": resolve(here, "src/settings"),
      "@proto": resolve(here, "src/net/proto/boxland/proto"),
      "@shared": resolve(here, "../shared"),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      // Design-tool HTTP + WS share the Go server on :8080 (set in task #16+).
      "/api": "http://localhost:8080",
      "/design": "http://localhost:8080",
      "/auth": "http://localhost:8080",
      "/ws": { target: "ws://localhost:8080", ws: true },
    },
  },
  build: {
    outDir: resolve(here, "dist"),
    emptyOutDir: true,
    sourcemap: true,
    rollupOptions: {
      // Per-page entries get added here as their modules land (game in task
      // #116, mapmaker in #104, sandbox in #131, pixel editor in #61).
      input: {
        boot:     resolve(here, "src/boot.ts"),
        game:     resolve(here, "src/game/entry-game.ts"),
        settings: resolve(here, "src/settings/entry-settings.ts"),
      },
      // Stable filenames so the Templ pages can <script src="/static/web/<entry>.js"/>
      // without templating in build hashes. Long-cache headers come from
      // the Go static handler; cache busting works via deploy ids on the
      // outer URL.
      output: {
        entryFileNames: "[name].js",
        chunkFileNames: "chunks/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
      },
    },
  },
});
