import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// O painel é servido pela própria API (DEC-34), no mesmo domínio: em
// desenvolvimento o Vite repassa /api e /healthz à API local.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: false },
      "/healthz": { target: "http://localhost:8080", changeOrigin: false },
    },
  },
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: false,
    sourcemap: false,
    assetsDir: "assets",
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
    css: false,
  },
});
