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
      coverage: {
        provider: "v8",
        reporter: ["text", "html"],
      },
    },
  }),
);
