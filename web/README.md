# Hearth Debug Dashboard

Basic React admin/development/debug dashboard for `hearthd`. It exposes the
v1 HTTP API and stored data: adapters + health history, devices, entities +
state, command sender, command history, availability history, and raw JSON
everywhere.

## Prerequisites

- `hearthd` running, default `http://127.0.0.1:8080`
  (see root `README.md`; start NATS with `mise run nats` first).
- Node (see `mise.lock` / root `mise.toml` for other tools; npm comes with Node).

## Run

```sh
cd web
npm install
npm run dev
```

Open http://127.0.0.1:5173. `/v1`, `/healthz`, `/readyz`, and `/openapi.json`
are proxied to `HEARTHD_URL` (default `http://127.0.0.1:8080`):

```sh
HEARTHD_URL=http://127.0.0.1:8080 npm run dev
```

To point at a non-loopback `hearthd` on a trusted network, set the base URL in
the toolbar (stored in `localStorage`) instead of using the proxy. The HTTP API
has no authentication, so keep `hearthd` on loopback or a trusted network.

## Build

```sh
npm run build
npm run preview
```

## Pages

- **Entities**: list (optional `device_id` filter), link to detail.
- **Entity detail**: metadata, state, support, enable/disable toggle
  (`PATCH /v1/entities/{id}`), command sender (`POST …/commands`) with
  power/brightness presets, command history, availability history, raw JSON.
- **Devices**: list + detail with embedded entities.
- **Adapters**: list + detail with runtime evidence, health history, raw JSON.
- **Commands**: lookup by `cmd_…` id (`GET /v1/commands/{id}`).

Health (`/healthz`) and readiness (`/readyz`) poll every 10s in the header.
