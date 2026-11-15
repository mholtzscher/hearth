import { execFileSync } from "node:child_process";
import path from "node:path";

/// <reference types="vitest/config" />
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, searchForWorkspaceRoot } from "vite";

// Proxies hearthd's HTTP API during development so the dashboard can use
// relative URLs (same-origin, no CORS issues).
// Run hearthd first, e.g. `go run ./cmd/hearthd --config configs/hearthd.yaml`
// (default bind 127.0.0.1:8080), then `npm run dev`.
const HEARTHD = process.env.HEARTHD_URL ?? "http://127.0.0.1:8080";
const NATS_MONITOR = process.env.NATS_MONITOR_URL ?? "http://127.0.0.1:8222";

/**
 * Hosts allowed to reach the dev server. Vite only answers `localhost` and
 * IP-literal Host headers by default, but `tailscale serve` forwards the
 * original Host header and the dev server is otherwise addressed by its
 * MagicDNS name, so add this machine's tailnet names when Tailscale is
 * installed. HEARTH_ALLOWED_HOSTS adds more (comma-separated; a leading dot
 * allows a whole suffix).
 */
const tailnetHosts = (): string[] => {
  let status: { Self?: { DNSName?: string }; MagicDNSSuffix?: string };
  try {
    status = JSON.parse(
      execFileSync("tailscale", ["status", "--json"], {
        encoding: "utf-8",
        timeout: 2000,
      })
    );
  } catch {
    // Not installed, not running, or too slow: keep Vite's default allowlist.
    return [];
  }
  // Tailscale reports DNS names with a trailing dot; Vite compares hostnames.
  const suffix = status.MagicDNSSuffix?.replace(/\.+$/u, "");
  const self = status.Self?.DNSName?.replace(/\.+$/u, "");
  return [suffix ? `.${suffix}` : null, self ?? null].filter(
    (host): host is string => host !== null
  );
};

const resolveAllowedHosts = (): string[] => {
  const configured = (process.env.HEARTH_ALLOWED_HOSTS ?? "")
    .split(",")
    .map((host) => host.trim())
    .filter(Boolean);
  return [...tailnetHosts(), ...configured];
};

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  test: {
    // Page tests render React into jsdom; fetch and localStorage come from the
    // Node runtime the tests stub and read. See `pnpm run test`.
    environment: "jsdom",
  },
  server: {
    port: Number(process.env.HEARTH_WEB_PORT ?? 5173),
    allowedHosts: resolveAllowedHosts(),
    // Generated contracts (readings catalog) live beside web/ at the repository root.
    fs: {
      allow: [
        searchForWorkspaceRoot(process.cwd()),
        path.resolve(import.meta.dirname, "../contracts"),
      ],
    },
    proxy: {
      "/v1": HEARTHD,
      "/healthz": HEARTHD,
      "/readyz": HEARTHD,
      "/openapi.json": HEARTHD,
      "/nats-monitor": {
        target: NATS_MONITOR,
        rewrite: (requestPath) => requestPath.replace(/^\/nats-monitor/u, ""),
      },
    },
  },
});
