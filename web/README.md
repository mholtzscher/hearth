# Hearth dashboard development

The React dashboard is the household workspace and Core inspection UI. For product behavior, use the [documentation index](../docs/README.md), not a second API or page catalog here. All commands below run from the repository root.

## Run locally

Install [Mise](https://mise.jdx.dev/), then:

```sh
mise install
mise run web-install
mise run simulator-start
```

Use the printed worktree-local web URL. This starts Core, NATS, and simulated Devices; it requires the simulator's selected model-provider credential. The checked-in simulator selects OpenCode Go (`HEARTH_OPENCODE_GO_API_KEY`). Standalone Core defaults to OpenAI (`HEARTH_OPENAI_API_KEY`). Supply the selected key through a supervisor or secret manager, never the browser. See [Core setup](../README.md#try-it-with-simulated-devices) and [provider compatibility](../docs/reference/agent-skills.md). Stop the managed stack with `mise run simulator-stop`.

To use an already running Core instead:

```sh
HEARTHD_URL=http://127.0.0.1:8080 mise run web-dev
```

Open `http://127.0.0.1:5173`. Leave the toolbar's base URL empty to use Vite's same-origin proxy for `/v1`, `/healthz`, `/readyz`, and `/openapi.json`. The Core API has no authentication or CORS headers. Keep it on loopback or a trusted private network. A toolbar override is stored in `localStorage`, but cross-origin browser reads fail without CORS; an override needs a same-origin deployment, not merely a reachable Core.

## Remote access through Tailscale

The managed dev server binds loopback. Instead of widening its bind:

```sh
mise run web-dev
tailscale serve --bg --http=8088 http://127.0.0.1:5173
```

Use `http://<machine>.<tailnet>.ts.net:8088/`, or `--https=443` for TLS. Vite allowlists this machine's tailnet names when `tailscale` is on `PATH`. For another hostname, set `HEARTH_ALLOWED_HOSTS=host.example,.suffix.example`; a leading dot allows the suffix. Only the dashboard port needs exposure because the proxy reaches Core server-side. Every device permitted by tailnet ACLs can use the unauthenticated dashboard and API.

## Develop and verify UI

Follow [Storybook development](../docs/guides/storybook.md) for synthetic, credential-free component work, MCP discovery, and browser verification. Follow [coding standards](../CODING_STANDARDS.md#dashboard-design-system) and [design intent](../DESIGN.md) when changing UI.

```sh
mise run web-fix
mise run web-check
mise run --skip-deps web-test
mise run --skip-deps web-build
```

Review safe fixes before rerunning checks. Ultracite's default rules apply; build output, dependencies, and the generated MSW worker are excluded. Use [the testing guide](../docs/guides/testing.md) for final validation.

### Automation editor safety

Agent proposals and current-definition references open in the canvas, separate from dashboard views. Only explicit browser **Save** persists a definition; **Save and enable** also enables it. External HTTP/MCP CRUD is unchanged. The exact-JSON inspector preserves untouched literal/expression tokens, including large integers, but cannot guarantee that a model reproduces original literals. Core rechecks current support on save.

Failed saves keep the draft. Conflicts offer reload or explicit save-as-new; ambiguous creation is never automatically retried. Check the library before another creation. Referenced children are read-only previews of their current definitions. Editing a draft does not execute or simulate it; use retained Run/Skip detail for execution evidence.

## Container deployment

Releases publish `ghcr.io/mholtzscher/hearth/hearth-web:<tag>`, matching the Go release tag. The static nginx image proxies Core and NATS monitoring server-side:

```sh
mise run web-docker-build
docker run --rm -p 8081:80 -e HEARTHD_URL=http://hearthd:8080 hearth-web:dev
```

Set `HEARTHD_URL` and `NATS_MONITOR_URL` for the deployment. Their hostnames must resolve when nginx starts. The NATS WebSocket URL remains browser-side and editable in the NATS page; it is not proxied.

## NATS debugging

The simulator supplies `VITE_NATS_WS_URL` and `NATS_MONITOR_URL` from worktree-local listeners in [its NATS configuration](../configs/nats.simulator.conf). For separate NATS, supply both URLs when starting Vite. The WebSocket override is stored in `localStorage`; diagnostics pages share a refcounted connection per URL. Restart the simulator stack after changing its NATS configuration.

Wire and Device Fact viewers are diagnostic, not durable recovery consumers. Retained snapshots and live tails use page-owned ephemeral consumers, deleted on completion or disable with an inactivity timeout for orphan cleanup. Use [Device Fact delivery](../docs/architecture/architecture.md#device-fact-delivery-and-recovery) and durable Core APIs for evidence rather than inferring outcomes from missing browser traffic.
