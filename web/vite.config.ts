import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import { defineConfig } from "vite";

// Proxies hearthd's HTTP API during development so the dashboard can use
// relative URLs (same-origin, no CORS issues).
// Run hearthd first, e.g. `go run ./cmd/hearthd -config configs/hearthd.yaml`
// (default bind 127.0.0.1:8080), then `npm run dev`.
const HEARTHD = process.env.HEARTHD_URL ?? "http://127.0.0.1:8080";
const NATS_MONITOR = process.env.NATS_MONITOR_URL ?? "http://127.0.0.1:8222";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/v1": HEARTHD,
      "/healthz": HEARTHD,
      "/readyz": HEARTHD,
      "/openapi.json": HEARTHD,
      "/nats-monitor": {
        target: NATS_MONITOR,
        rewrite: (path) => path.replace(/^\/nats-monitor/, ""),
      },
    },
  },
});
