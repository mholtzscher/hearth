---
name: real-device-validation
description: Validate Hearth against REAL Zigbee devices through the homelab's shared NATS and Zigbee2MQTT dev environment, and collect real device payloads for fixtures. Use this skill whenever the user mentions real devices, real hardware, the homelab, physical lights or sensors, validating against Zigbee2MQTT, capturing live device data, or debugging behavior the simulator cannot reproduce. Do NOT use it for pure simulator runs, unit tests, or local-loopback development.
---

# Real-Device Validation

Run local `hearthd` + `hearth-adapter-zigbee2mqtt` from the current worktree
against the shared dev stack on the homelab server, validate real Zigbee
hardware, and collect device data for fixtures.

`HOMELAB` below means the homelab server's hostname. If you don't know it,
check `~/.ssh/config` or ask the user — never assume. The hostname already
resolves over the home network; no VPN or tunnel setup is needed.

## Topology (do not change this)

- On `HOMELAB` (shared, operator-managed — never modify):
  - NATS + JetStream + MQTT: `HOMELAB:4222`, monitor `HOMELAB:8222`, MQTT `HOMELAB:1883`
  - Zigbee2MQTT frontend: `http://HOMELAB:8082/` (read-only use unless asked)
  - Config: MQTT `mqtt://nats:1883`, base topic `zigbee2mqtt`, availability
    enabled, global optimistic `false`.
- Locally (this worktree, agent-managed):
  - `hearthd` from `configs/homelab-hearthd.yaml` → `nats://HOMELAB:4222`
  - `hearth-adapter-zigbee2mqtt` from `configs/homelab-zigbee2mqtt.yaml` →
    `nats://HOMELAB:4222` + `tcp://HOMELAB:1883`
  - HTTP API on `127.0.0.1:8080` (loopback; the API has no auth).

## Rules

- NEVER run `mise run nats` in real-device mode. Local NATS is for simulator
  and loopback work only and will shadow nothing while confusing diagnosis.
- NEVER edit `configs/*.example.yaml`. Write ignored homelab configs instead
  (see below); `configs/*.yaml` is gitignored except examples.
- NEVER change anything on the homelab server: no compose/stack edits, no
  Zigbee2MQTT settings changes, no pairing/unpairing, renaming, OTA, or
  coordinator touches without explicit user approval.
- READS are always safe. COMMANDS move physical hardware (lights turn on/off).
  Announce each planned command and get explicit user confirmation first.
- NEVER copy homelab secrets into the repo: `network_key`, `pan_id`,
  `ext_pan_id`, database internals. Model minimal fixtures by hand from
  observed shapes instead of pasting raw dumps.
- A `hearthd` container on the homelab server is normally stopped. If
  `HOMELAB:8081` answers, a second core is live on the same NATS — stop and
  ask the user before starting a local `hearthd`.

## Step 1 — Preflight (fast fail)

Substitute the real hostname for `HOMELAB` and run these before launching
anything:

```sh
curl -s -m 5 http://HOMELAB:8222/varz | head -c 200; echo
curl -s -m 5 -o /dev/null -w "z2m-frontend:%{http_code}\n" http://HOMELAB:8082/
curl -s -m 5 -o /dev/null -w "homelab-hearthd:%{http_code}\n" http://HOMELAB:8081/healthz
ssh -o ConnectTimeout=5 HOMELAB true && echo ssh-ok
```

Proceed only if NATS answers, the Z2M frontend is 200, `HOMELAB:8081` does NOT
answer, and ssh works. Also confirm MQTT TCP is open:

```sh
nc -z -w 5 HOMELAB 1883 && echo mqtt-tcp-ok
```

## Step 2 — Homelab configs (create once, reuse)

`configs/homelab-hearthd.yaml` (separate sqlite file from loopback runs):

```yaml
http_addr: 127.0.0.1:8080
nats_url: nats://HOMELAB:4222
sqlite_path: .data/homelab-hearthd.db
```

`configs/homelab-zigbee2mqtt.yaml`:

```yaml
adapter_id: zigbee2mqtt
nats_url: nats://HOMELAB:4222
mqtt:
  url: tcp://HOMELAB:1883
  base_topic: zigbee2mqtt
```

Both paths are gitignored, so they never leak into commits.

## Step 3 — Launch in a named Herdr tab

Verify you are inside Herdr first (`test "${HERDR_ENV:-}" = 1`). Keep the
user's focus unchanged with `--no-focus` throughout.

Keep pane commands bare — logs flow to the pane and you read them with
`herdr pane read`. Do NOT add shell redirects or `tee`: Herdr panes here run
Nushell, where POSIX `2>&1` is a syntax error, and log files (when wanted)
are captured from your own shell via `pane read` (see Step 5).

```sh
herdr tab create --label real-device-validation --cwd "$PWD" --no-focus
```

Read the new tab and root pane IDs from `.result.tab.tab_id` and
`.result.root_pane.pane_id`, then start `hearthd` in the root pane:

```sh
herdr pane run <root-pane-id> "go run ./cmd/hearthd -config configs/homelab-hearthd.yaml"
herdr pane wait-output <root-pane-id> --match "listening" --timeout 60000
curl -s -m 5 http://127.0.0.1:8080/healthz -w "\n"
curl -s -m 5 http://127.0.0.1:8080/readyz -w "\n"
```

Only when `/readyz` is 200 (SQLite migrated, NATS connected, JetStream
provisioned, observation consumer active), split a second pane and start the
adapter:

```sh
herdr pane split --pane <root-pane-id> --direction right --cwd "$PWD" --no-focus
herdr pane run <new-pane-id> "go run ./cmd/hearth-adapter-zigbee2mqtt -config configs/homelab-zigbee2mqtt.yaml"
```

Poll until the adapter is healthy (unknown → healthy after retained
`bridge/state`, `bridge/info`, `bridge/devices` plus registration):

```sh
curl -s http://127.0.0.1:8080/v1/adapters/zigbee2mqtt
```

## Step 4 — Validate against real devices

Discover and inspect (safe, no confirmation needed):

```sh
curl -s http://127.0.0.1:8080/v1/entities
curl -s http://127.0.0.1:8080/v1/entities/ent_<id>
```

Ask the user which test device to exercise (e.g. a table lamp with
power/brightness/color-temperature entities plus any ambient-temperature
entity). Temperature entities are read-only (`hearth.temperature/v1`,
milli-Celsius state, empty operation support) and never create command
routes.

Commands (require explicit user confirmation — physical actuation). A command
succeeds only after a fresh, non-retained device report matches the request:

```sh
curl -X POST http://127.0.0.1:8080/v1/entities/ent_<power-id>/commands \
  -H 'content-type: application/json' -d '{"operation":"set","parameters":{"value":true}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_<brightness-id>/commands \
  -H 'content-type: application/json' -d '{"operation":"set","parameters":{"value":50}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_<colortemp-id>/commands \
  -H 'content-type: application/json' -d '{"operation":"set","parameters":{"value":370}}'
```

Validate one capability at a time and re-read the entity after each command.
Return lights to their prior state when done if the user wants the room
restored. Cross-check surprising state in the Z2M frontend (`HOMELAB:8082`).

## Step 5 — Collect device data

Save evidence under ignored `.data/` first, then hand-craft repo fixtures:

- Adapter + hearthd logs: pull from the panes when needed —
  `herdr pane read <pane-id> --source recent-unwrapped --lines 500 >
  .data/homelab-<name>.log` (run from your own shell; `.data/` is ignored).
  Fresh worktrees may need `mkdir -p .data` first.
- HTTP evidence: `curl -s <url> | tee .data/homelab-<what>.json`.
- For new testdata (e.g. `internal/adapters/zigbee2mqtt/testdata/`), write a
  MINIMAL payload modeled on the observed shape — one device, relevant
  exposes only — never paste `database.db`, `configuration.yaml`, or keys.
- Note in the fixture or PR description: device model, firmware/`swBuildId`
  if visible, which entity kinds were exercised, and the date.

## Troubleshooting

| Symptom | Likely cause → fix |
|---|---|
| `hearth.external_system_unavailable` | MQTT unreachable → recheck `tcp://HOMELAB:1883` and `nc -z -w 5 HOMELAB 1883`; verify Z2M container live via frontend |
| `bridge_offline` | Explicit offline `bridge/state` → check coordinator/Z2M on the homelab server with the user; do not restart anything yourself |
| `incompatible_configuration` | MQTT version ≠ 4, availability disabled, or optimistic not false → read-only inspection only; ask the user before touching Z2M config |
| `invalid_inventory` | Malformed retained `bridge/info`/`bridge/devices` → ask the user to correct/restart Z2M on the homelab server |
| Adapter stuck `unknown` | Retained bridge topics or registration incomplete → inspect adapter log tail via `herdr pane read <pane-id>`; confirm `/readyz` 200 |
| `/readyz` flapping | Local hearthd competing with a `HOMELAB:8081` core → stop local, ask user which core should own NATS |
| Empty entity list | Wrong base topic or no supported exposes → confirm `base_topic: zigbee2mqtt` and device interviews completed in Z2M frontend |

## Cleanup

Stop the adapter pane first, then `hearthd` (Ctrl+C via the pane or closing
its process), and close the tab only if this session created it:

```sh
herdr tab close <real-device-validation-tab-id>
```

Leave the homelab server exactly as found. Local `configs/homelab-*.yaml`
and `.data/homelab-*` files are ignored and safe to keep for the next
session.
