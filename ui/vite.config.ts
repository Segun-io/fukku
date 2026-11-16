import { readFileSync } from "node:fs";
import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// `npm run dev:mock` (vite --mode mock) answers the API in the browser with MSW. Its service
// worker script is served from the package, so nothing generated is committed.
const workerScript = path.resolve(import.meta.dirname, "node_modules/msw/lib/mockServiceWorker.js");

function mockServiceWorker(): Plugin {
  return {
    name: "fukku-mock-service-worker",
    apply: "serve",
    configureServer(server) {
      server.middlewares.use("/mockServiceWorker.js", (_req, res) => {
        res.setHeader("Content-Type", "text/javascript");
        res.end(readFileSync(workerScript));
      });
    },
  };
}

// The build goes to ui/dist, which the Go binary embeds (ui/embed.go).
export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), ...(mode === "mock" ? [mockServiceWorker()] : [])],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  build: { outDir: "dist", emptyOutDir: false, sourcemap: true },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080",
    },
  },
}));
