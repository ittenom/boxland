import { defineConfig, mergeConfig } from "vitest/config";
import viteConfig from "./vite.config";

// Vitest config inherits the Vite config (path aliases, root, etc.)
// so unit tests resolve @net/, @collision/, etc. exactly as the bundle does.
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      globals: true,
      environment: "node",
      include: ["**/*.{test,spec}.ts"],
      // _author_vectors.ts is a one-shot CLI used to regenerate the
      // collision corpus; not a test, must not be auto-discovered.
      exclude: ["**/_*.ts", "**/node_modules/**", "**/dist/**"],
      // Polyfill CanvasRenderingContext2D + HTMLCanvasElement.getContext
      // so jsdom-environment tests that touch Pixi 8.13+'s Text
      // measurement path don't blow up. Real-browser builds are
      // unaffected (the polyfill only runs under vitest).
      setupFiles: ["./test-setup.ts"],
      coverage: {
        provider: "v8",
        reporter: ["text", "html"],
      },
    },
  }),
);
