# Hearth Debug Dashboard

Basic React admin/development/debug dashboard for `hearthd`. It exposes the
v1 HTTP API and stored data: adapters + health history, devices, entities +
state, command sender, command history, availability history, and raw JSON
everywhere.

## Prerequisites

- `hearthd` running, default `http://127.0.0.1:8080`
  (see root `README.md`; start NATS with `mise run nats` first).
- Node + pnpm, provided by mise: run `mise install` from the repo root.

## Run

```sh
cd web
pnpm install
pnpm run dev
```

Open http://127.0.0.1:5173. `/v1`, `/healthz`, `/readyz`, and `/openapi.json`
are proxied to `HEARTHD_URL` (default `http://127.0.0.1:8080`):

```sh
HEARTHD_URL=http://127.0.0.1:8080 pnpm run dev
```

To point at a non-loopback `hearthd` on a trusted network, set the base URL in
the toolbar (stored in `localStorage`) instead of using the proxy. The HTTP API
has no authentication, so keep `hearthd` on loopback or a trusted network.
Browsers block cross-origin reads unless the server sends CORS headers, and
`hearthd` serves none: in a browser, leave the base URL empty to use the
same-origin vite proxy. A non-empty base URL only works from a same-origin
deployment or once `hearthd` gains CORS support.

## Build

```sh
pnpm run build
pnpm run preview
```

## Container image

Releases publish `ghcr.io/mholtzscher/hearth-web:<tag>` (same tag as the
`ko` Go images): a static build served by nginx, proxying `/v1`, `/healthz`,
`/readyz`, `/openapi.json` to `HEARTHD_URL` and `/nats-monitor` to
`NATS_MONITOR_URL` (same-origin, so no CORS setup is needed). Point it at
`hearthd` at container start:

```sh
mise run web-docker-build
docker run --rm -p 8081:80 -e HEARTHD_URL=http://hearthd:8080 hearth-web:dev
```

The `HEARTHD_URL` hostname must resolve when the container starts (nginx
resolves proxy targets at startup). The NATS websocket URL stays editable in
the NATS page (stored in `localStorage`), so it needs no proxy.

## Pages

- **Entities**: list (optional `device_id` filter), link to detail.
- **Entity detail**: metadata, state, support, enable/disable toggle
  (`PATCH /v1/entities/{id}`), command sender (`POST …/commands`) with
  power/brightness presets, command history, availability history, raw JSON.
- **Devices**: list + detail with embedded entities.
- **Adapters**: list + detail with runtime evidence, health history, raw JSON.
- **Commands**: lookup by `cmd_…` id (`GET /v1/commands/{id}`).
- **NATS**: live wire traffic (`hearth.v1.adapter.>` over websocket) with
  subject presets, pause/clear, and subject filter; plus server info from the
  NATS monitoring endpoint (connections with subscriptions, JetStream stream
  `HEARTH_OBSERVATIONS_V1` state and consumer lag).

Health (`/healthz`) and readiness (`/readyz`) poll every 10s in the header.

## NATS debugging prerequisites

The NATS page needs the dev-only loopback listeners in
`configs/nats-server.conf` (already present: `websocket` on
`127.0.0.1:4223`, `http` monitoring on `127.0.0.1:8222`). Restart NATS after
changing that file (`mise run nats`). Vite proxies `/nats-monitor` to the
monitoring port (override with `NATS_MONITOR_URL`); the websocket URL is
editable in the page (stored in `localStorage`).
