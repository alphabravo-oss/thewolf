import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import path from "node:path";

// Wolf UI build config.
//
// - Output goes to ./dist (consumed by the Go server's embedded UI serving
//   layer; the previous Next.js standalone tree at `.next/standalone` is
//   replaced by a static SPA).
// - The Go API runs on :8778 by default; we proxy /api/* to it in dev so
//   `npm run dev` Just Works without CORS dancing.
// - TanStackRouterVite generates the type-safe route tree from src/routes/.
export default defineConfig({
  plugins: [
    TanStackRouterVite({
      routesDirectory: "./src/routes",
      generatedRouteTree: "./src/routeTree.gen.ts",
      autoCodeSplitting: true,
    }),
    react(),
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 3000,
    host: "127.0.0.1",
    proxy: {
      "/api": {
        target: "http://localhost:8778",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    target: "es2022",
    chunkSizeWarningLimit: 1000,
  },
});
