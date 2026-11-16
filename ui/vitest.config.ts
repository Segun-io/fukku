import path from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "src"),
      // Tests run in jsdom: use the browser build of tus-js-client, as the real bundle does.
      "tus-js-client": path.resolve(
        import.meta.dirname,
        "node_modules/tus-js-client/lib.esm/browser/index.js",
      ),
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    testTimeout: 20_000,
    css: false,
  },
});
