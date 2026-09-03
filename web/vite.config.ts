import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Proxies hearthd's HTTP API during development so the dashboard can use
// relative URLs (same-origin, no CORS issues).
// Run hearthd first, e.g. `go run ./cmd/hearthd -config configs/hearthd.yaml`
// (default bind 127.0.0.1:8080), then `npm run dev`.
const HEARTHD = process.env.HEARTHD_URL ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/v1": HEARTHD,
      "/healthz": HEARTHD,
      "/readyz": HEARTHD,
      "/openapi.json": HEARTHD,
    },
  },
});
