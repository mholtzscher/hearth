# Hearth Debug Dashboard

Basic React admin/development/debug dashboard for `hearthd`. It exposes the
v1 HTTP API and stored data: adapters + health history, devices, entities +
state, command sender, command history, availability history, and raw JSON
everywhere.

## Prerequisites

- `hearthd` running, default `http://127.0.0.1:8080`
  (see root `README.md`; start the local brokers with `mise run brokers` first).
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

## Remote access (Tailscale)

The dev server binds loopback only (`mise run web-dev` passes `--host 127.0.0.1`),
so expose it with `tailscale serve` instead of widening the bind:

```sh
mise run web-dev                                          # loopback
tailscale serve --bg --http=8088 http://127.0.0.1:5173    # tailnet only
```

→ `http://<machine>.<tailnet>.ts.net:8088/` (use `--https=443` for a TLS
certificate). Tailscale forwards the original Host header, so `vite.config.ts`
allowlists this machine's tailnet names automatically when `tailscale` is on
`PATH`. Set `HEARTH_ALLOWED_HOSTS=host.example,.suffix.example` for any other
name (a leading dot allows the whole suffix).

Only the dashboard port needs to be reachable: `hearthd` stays on
`127.0.0.1:8080` because the proxy runs server-side. Neither the dashboard nor
the HTTP API authenticates, so every device your tailnet ACLs allow can use it.

## Build

```sh
pnpm run build
pnpm run preview
```

## Container image

Releases publish `ghcr.io/mholtzscher/hearth/hearth-web:<tag>` (same tag as the
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
- **Commands**: household history newest-first (`GET /v1/commands`) with entity/status filters, lookup by `cmd_…` id (`GET /v1/commands/{id}`) with shareable `?command_id=` links and recent IDs, outcome timeline with latency, and links to entity pages and automation step attempts.
- **NATS**: live wire traffic (`hearth.v1.adapter.>` over websocket) with
  subject presets, pause/clear, and subject filter; plus server info from the
  NATS monitoring endpoint (connections with subscriptions, JetStream stream
  `HEARTH_OBSERVATIONS_V1` state and consumer lag).
- **Device facts**: reads Core's `HEARTH_DEVICE_FACTS_V1` JetStream stream over
  the same websocket. A bounded retained snapshot (newest 200, newest-first,
  `hearth.v1.core.fact.>`) is read through a short-lived ephemeral consumer that
  is deleted as soon as the read completes; live streaming is off by default and
  adds a separate ephemeral `DeliverNew` tail that sees only facts published
  after it is created. Both consumers are page-owned and deleted on completion or
  disable, with `inactive_threshold` as orphan safety for a closed tab. Entity and
  device names are enrichment from the HTTP read API; the payload and subject
  carry only the canonical entity id. Neither path offers durable recovery.

Health (`/healthz`) and readiness (`/readyz`) poll every 10s in the header.

## NATS debugging prerequisites

The NATS and Device facts pages need the dev-only loopback listeners in
`configs/nats-server.conf` (already present: `websocket` on
`127.0.0.1:4223`, `http` monitoring on `127.0.0.1:8222`). Restart the local
brokers after changing that file (`mise run brokers-down && mise run brokers`).
Vite proxies `/nats-monitor` to the monitoring port (override with
`NATS_MONITOR_URL`); the websocket URL is editable in the NATS page (stored in
`localStorage`) and both pages share one refcounted connection per URL, so one
page's teardown never closes the other's socket.
