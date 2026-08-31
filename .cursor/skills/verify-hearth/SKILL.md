---
name: verify-hearth
description: Verify Hearth through its unauthenticated HTTP API against a real local hearthd, JetStream server, and first-party simulator. Use when changing or checking resource discovery, Entity State, Commands, Entity enablement, command history, readiness, or HTTP contracts and user-visible proof is needed.
---

# Verify Hearth

Hearth currently exposes an HTTP API. It has no browser UI or management CLI. Drive `hearthd` through HTTP and use the first-party simulator as the adapter. The Home Assistant migration adapter is a secondary surface and needs household credentials and a real light, so it is not the default verification path.

## Launch

Run from the repository root. Use a fresh lower-case run ID for every attempt.

```bash
CONTROL=./.cursor/skills/verify-hearth/scripts/control-hearth
RUN_ID=verify-$(date +%s)
"$CONTROL" launch "$RUN_ID" happy
```

`launch` builds `hearthd` and `hearth-simulator` from the current checkout. It starts an isolated loopback NATS server with JetStream, an isolated SQLite database, `hearthd` on a free loopback port, and the simulator's `happy` scenario. It prints the HTTP URL and canonical Device and Entity IDs.

The run is ready only after `/readyz` returns `{"status":"ready"}`, the simulator registers one `Power` Entity and publishes its initial State, and the simulator subscribes for that Entity's `set` Commands.

Each run uses `.data/verify-hearth/runs/$RUN_ID`. Ports, NATS storage, SQLite, adapter IDs, and binding keys are unique, so separate run IDs can run side by side. Do not point this skill at a developer's existing NATS server or `hearthd` process.

For a simulator failure path, pass its repository scenario as the third argument. Supported scenarios are `duplicate`, `delayed-source-time`, `future-clock-skew`, `malformed`, `unavailable-adapter`, `upstream-rejection`, `no-op-refresh`, `overlapping-opposite-command`, `outcome-timeout`, `interrupted-command`, and `restart-before-ack`. Use `happy` unless the feature under test requires one of these behaviors.

Teardown is:

```bash
"$CONTROL" cleanup "$RUN_ID"
```

## Doctor

Run this before driving the API and whenever the instance looks wrong:

```bash
"$CONTROL" doctor "$RUN_ID"
```

Doctor is read-only. It checks that the recorded NATS, `hearthd`, and simulator processes still have their original process start tokens and executable hashes; confirms that those processes own the recorded ports; requires healthy and ready responses; checks the runtime OpenAPI identity `Hearth` version `1.0.0`; reads the registered Entity; and checks the simulator Command subscription when the scenario should have one. Hearth's HTTP API has no authentication, which doctor reports as `auth=not_configured`.

Do not drive a run when doctor fails. Clean it up, inspect `.data/verify-hearth/evidence/$RUN_ID/runtime/*.log`, and start a fresh run ID.

## Drive

Read [`features/README.md`](./features/README.md), then use the file for the feature being verified. Load the IDs and paths produced by launch:

```bash
source ".data/verify-hearth/runs/$RUN_ID/runtime.env"
```

Drive the real HTTP routes through `control-hearth request`. The helper captures the request, headers, body, and status before checking the expected status.

```bash
"$CONTROL" request "$RUN_ID" entity-before GET "/v1/entities/$ENTITY_ID" 200
"$CONTROL" request "$RUN_ID" set-power POST "/v1/entities/$ENTITY_ID/commands" 200 \
  '{"operation":"set","parameters":{"value":true}}'
"$CONTROL" request "$RUN_ID" entity-after GET "/v1/entities/$ENTITY_ID" 200
```

Use a new request label for every action. Labels are permanent evidence keys and the helper refuses to overwrite one. Prefer canonical IDs from `runtime.env`; do not scrape IDs from logs.

The simulator crosses the same adapter SDK and NATS contracts as a real adapter. Do not bypass the user path by writing SQLite rows, publishing observations directly, or calling package internals. NATS monitoring is reserved for launch and doctor checks, not feature proof.

## Evidence

Proof is stored at `.data/verify-hearth/evidence/$RUN_ID` and survives cleanup. Every captured request has:

- `request.txt` with the method, URL, expected status, and action time
- `request.json` when the request has a body
- `response.headers`, `response.json`, `status.txt`, and `curl.stderr`

Cleanup adds process logs, exact generated configs, launch metadata, and the stopped SQLite database under `runtime/`.

A valid proof does all of the following:

- Exercises a documented HTTP route, not an internal setter or test-only endpoint.
- Captures both the user action and its response.
- Reads the resulting Entity or Command through a second HTTP request.
- For a Command, confirms the durable side effect through `/v1/commands/{command_id}` or `/v1/entities/{entity_id}/commands`, not only the synchronous result.
- For enablement, confirms both the visible `enabled` value and the next Command's accepted or rejected result.

Hearth has no dry-run HTTP mode. Simulator scenarios replace the external device boundary but still use production registration, Observation, Command, persistence, and HTTP paths. A scenario name is not proof by itself. Capture the HTTP outcome, the resulting resource, and the copied logs.

## Cleanup

Always clean up, including after a failed drive:

```bash
"$CONTROL" cleanup "$RUN_ID"
```

Cleanup sends signals only to the PIDs recorded for this run and verifies each process start token before doing so. It never kills by process name. It stops simulator, `hearthd`, and NATS in that order, copies runtime evidence, removes `.data/verify-hearth/runs/$RUN_ID`, and leaves `.data/verify-hearth/evidence/$RUN_ID` intact.

Confirm both conditions:

```bash
test ! -e ".data/verify-hearth/runs/$RUN_ID"
test -d ".data/verify-hearth/evidence/$RUN_ID"
```

## Helpers

The executable helper is `.cursor/skills/verify-hearth/scripts/control-hearth`.

```bash
# Build and start an isolated stack. Scenario defaults to happy.
"$CONTROL" launch "$RUN_ID" [scenario]

# Read-only instance and ownership check.
"$CONTROL" doctor "$RUN_ID"

# Capture one HTTP exchange and require its status.
"$CONTROL" request "$RUN_ID" <label> <GET|POST|PATCH> <path> <status> [json-body]

# Stop only this run and retain its evidence.
"$CONTROL" cleanup "$RUN_ID"
```

The helper requires Bash, `mise`, Go and `nats-server` from `mise.toml`, `curl`, Python 3, and Linux `/proc`. Run `mise install` if the pinned Go or NATS tools are missing.
