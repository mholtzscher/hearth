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
are proxied to `HEARTHD_URL` (default `http://127.0.0.1:8080`), and `/flue` is
proxied to `FLUE_URL` (default `http://127.0.0.1:5174`, the `agent-flue` server):

```sh
HEARTHD_URL=http://127.0.0.1:8080 FLUE_URL=http://127.0.0.1:5174 pnpm run dev
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

## Household agent

`/agent` drives hearthd's required household agent (`POST /v1/agent/conversations`,
`POST .../{id}/messages`, `GET .../{id}/messages`): Eino ReAct in-process with
SQLite conversation history. Turns stream over SSE (`POST
.../{id}/messages/stream`: `turn.started`, `tool.started`, `tool.finished`,
`turn.finished`/`turn.failed`) so tool activity renders incrementally; the page
reconciles with history at turn end. The stream route is a plain Echo handler
and stays out of `openapi.json`. The browser holds no model key; the
conversation ID persists in `localStorage` while history is durable
server-side. Core always registers these routes: the agent is a required module,
configured by the `agent` block in `configs/hearthd.yaml` (an
`agent.api_key_file` secret plus an optional `agent.model`, default
`gpt-5.6-luna`), and Core fails startup when the secret file is missing. A 404
from the page means the dashboard is connected to an older Core build. See
`web/src/pages/AgentPage.tsx` and `internal/modules/agent`.

## Flue agent (spike)

`/agent-flue` renders the isolated Flue household agent (`agent-flue/`) in the
same shell as `/agent`. It is a separate runtime: Flue owns its conversation and
execution records, while Hearth stays the source of truth for devices, Commands,
and their outcomes. The page addresses one caller-chosen instance ID
(`hearth.flueAgentInstanceId` in `localStorage`, distinct from the built-in
agent's key), mints one on first use, and starts a new conversation by persisting
a fresh ID; Flue creates the instance on the first admitted message.

`useFlueAgent()` from `@flue/react` loads the durable history and follows the
live updates stream, so the page renders visible user/assistant text, reasoning,
and each tool call's lifecycle (running → done/failed) without hand-rolling
transports. Flue is reached through the same-origin `/flue` proxy, which strips
the prefix: `/flue/agents/household/<id>` becomes
`${FLUE_URL}/agents/household/<id>`. There are no cross-origin calls by default.

The Flue routes have no authentication and the agent can execute real Entity
Commands. Anyone who can reach the dashboard can use the model credentials and
act on the household. Keep both services on loopback or a trusted network; do
not publish `/flue` to an untrusted origin without adding authentication and an
authorization policy.

Start Flue (it needs `OPENAI_API_KEY`; see `agent-flue/README.md`) and set
`FLUE_URL` if it is not on the default loopback port:

```sh
mise run flue-agent-dev                                  # 127.0.0.1:5174
FLUE_URL=http://127.0.0.1:5174 pnpm --dir web run dev
```

The page reports a connection error when Flue is unreachable; the required
built-in `/agent` page and hearthd are unaffected.

## Build

```sh
pnpm run build
pnpm run preview
```

## Container image

Releases publish `ghcr.io/mholtzscher/hearth/hearth-web:<tag>` (same tag as the
`ko` Go images): a static build served by nginx, proxying `/v1`, `/healthz`,
`/readyz`, `/openapi.json` to `HEARTHD_URL`, `/nats-monitor` to
`NATS_MONITOR_URL`, and `/flue` to `FLUE_URL` (same-origin, so no CORS setup is
needed). Point it at `hearthd` at container start:

```sh
mise run web-docker-build
docker run --rm -p 8081:80 \
  -e HEARTHD_URL=http://hearthd:8080 \
  -e FLUE_URL=http://flue-agent:5174 \
  hearth-web:dev
```

The `HEARTHD_URL`/`FLUE_URL` hostnames must resolve when the container starts
(nginx resolves proxy targets at startup). `/flue` disables proxy buffering so
the SSE updates stream is not held back. The NATS websocket URL stays editable
in the NATS page (stored in `localStorage`), so it needs no proxy.

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
- **Flue agent**: the isolated Flue household agent (see above) with a
  persisted instance ID, New conversation, live user/assistant transcript, and
  tool call lifecycle. Same-origin `/flue` proxy; no cross-origin calls.

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
