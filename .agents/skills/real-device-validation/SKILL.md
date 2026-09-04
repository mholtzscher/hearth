---
name: real-device-validation
description: Validate Hearth against REAL Zigbee devices through the homelab's shared NATS and Zigbee2MQTT dev environment, and collect real device payloads for fixtures. Use this skill whenever the user mentions real devices, real hardware, the homelab, physical lights or sensors, validating against Zigbee2MQTT, capturing live device data, or debugging behavior the simulator cannot reproduce. Do NOT use it for pure simulator runs, unit tests, or local-loopback development.
---

# Real-Device Validation

Run local `hearthd` + `hearth-adapter-zigbee2mqtt` + the `web/` debug UI from
the current worktree against the shared dev stack on the homelab server,
validate real Zigbee hardware, and collect device data for fixtures.

`HOMELAB` below means the homelab server's hostname. If you don't know it,
check existing `configs/homelab-*.yaml` first (they already encode it from
a prior session), then `~/.ssh/config`, then ask the user — never assume.
The hostname already resolves over the home network; no VPN or tunnel
setup is needed.

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
  - `web/` debug UI on `http://127.0.0.1:5173`, proxying Hearth API requests
    to local `hearthd` and NATS monitoring requests to `HOMELAB:8222`.

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
anything. Each check prints an explicit result and the script stops at the
first failed requirement:

```sh
set -eu

if nc -z -w 3 127.0.0.1 8080; then
  echo local-8080-in-use
  exit 1
else
  echo local-8080-free
fi

if nc -z -w 3 127.0.0.1 5173; then
  echo local-5173-in-use
  exit 1
else
  echo local-5173-free
fi

curl -fsS -m 5 -o /dev/null http://HOMELAB:8222/varz
echo nats-monitor-ok

z2m_code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' http://HOMELAB:8082/ 2>/dev/null || true)
printf 'z2m-frontend:%s\n' "${z2m_code:-000}"
test "$z2m_code" = 200

core_code=$(curl -s -m 5 -o /dev/null -w '%{http_code}' http://HOMELAB:8081/healthz 2>/dev/null || true)
printf 'homelab-hearthd:%s\n' "${core_code:-000}"
test "${core_code:-000}" = 000

nc -z -w 5 HOMELAB 1883
echo mqtt-tcp-ok
```

Proceed only when every check passes: local `:8080` and `:5173` are free,
NATS and MQTT answer, the Z2M frontend is 200, and `HOMELAB:8081` does not
answer. A stale local `hearthd` or debug UI would shadow the new one; a remote
core would compete on the shared NATS. SSH access is not part of this workflow
and is not required.

## Step 2 — Homelab configs (create once, reuse)

These configs are intentionally gitignored, so git-aware file search may omit
them. Check for existing files with shell path tests instead:

```sh
for config in configs/homelab-hearthd.yaml configs/homelab-zigbee2mqtt.yaml; do
  test ! -f "$config" || echo "$config"
done
```

Before creating or updating them, verify that both paths are ignored:

```sh
git check-ignore -q configs/homelab-hearthd.yaml
git check-ignore -q configs/homelab-zigbee2mqtt.yaml
```

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
```

`go run` compiles first, so allow ~60s. (`pane run` returning no output is
expected — proceed to HTTP polling.) Poll `/healthz` then `/readyz` —
HTTP is the readiness gate. (`pane wait-output --match "listening"` is not
reliable here: pane output may show nothing but the echoed command while
the server is already up.)

```sh
health_ok=0
for i in $(seq 1 12); do
  if body=$(curl -fsS -m 3 http://127.0.0.1:8080/healthz 2>/dev/null); then
    echo "$body"
    health_ok=1
    break
  fi
  sleep 5
done
test "$health_ok" -eq 1 || { echo healthz-timeout; exit 1; }

ready_ok=0
for i in $(seq 1 12); do
  if body=$(curl -fsS -m 3 http://127.0.0.1:8080/readyz 2>/dev/null); then
    echo "$body"
    ready_ok=1
    break
  fi
  sleep 5
done
test "$ready_ok" -eq 1 || { echo readyz-timeout; exit 1; }
```

Only when `/readyz` is 200 (SQLite migrated, NATS connected, JetStream
provisioned, observation consumer active), split a second pane and start the
adapter:

```sh
herdr pane split --pane <root-pane-id> --direction right --cwd "$PWD" --no-focus
herdr pane run <new-pane-id> "go run ./cmd/hearth-adapter-zigbee2mqtt -config configs/homelab-zigbee2mqtt.yaml"
```

Poll until the adapter is healthy (unknown → healthy after retained
`bridge/state`, `bridge/info`, `bridge/devices` plus registration).
Usually healthy within seconds — loop and stop at the first `healthy`:

```sh
adapter_healthy=0
body=''
for i in $(seq 1 12); do
  body=$(curl -s -m 3 http://127.0.0.1:8080/v1/adapters/zigbee2mqtt || true)
  if echo "$body" | grep -q '"status":"healthy"'; then
    echo "$body"
    adapter_healthy=1
    break
  fi
  sleep 5
done
test "$adapter_healthy" -eq 1 || {
  printf 'adapter-health-timeout:%s\n' "$body"
  exit 1
}
```

Healthy adapter status proves transport and registration, but individual
device properties may still be rejected. Always inspect the adapter's recent
output after it becomes healthy and report relevant warnings or errors:

```sh
herdr pane read <adapter-pane-id> --source recent-unwrapped --lines 120
```

Split a third pane and launch the debug UI. The mise task installs its npm
dependencies before starting Vite. Point the UI's NATS monitoring proxy at the
shared homelab monitor; its Hearth API proxy already defaults to local
`hearthd`:

```sh
herdr pane split --pane <adapter-pane-id> --direction down --cwd "$PWD" --no-focus
herdr pane run <dashboard-pane-id> "NATS_MONITOR_URL=http://HOMELAB:8222 mise run web-dev"
```

Poll until Vite is serving the dashboard, then report its URL to the user:

```sh
dashboard_ok=0
for i in $(seq 1 12); do
  if curl -fsS -m 3 -o /dev/null http://127.0.0.1:5173/; then
    echo debug-ui:http://127.0.0.1:5173/
    dashboard_ok=1
    break
  fi
  sleep 5
done
test "$dashboard_ok" -eq 1 || { echo debug-ui-timeout; exit 1; }
```

Do not start local NATS to enable the dashboard's live NATS WebSocket view;
real-device mode must continue using only the shared homelab NATS server.

## Step 4 — Validate against real devices

Discover and inspect (safe, no confirmation needed). Device names identify the
physical hardware; entity names such as `Power` are only meaningful when joined
to their `device_id`. Collection responses use an `items[]` envelope:

```sh
mkdir -p .data
curl -fsS http://127.0.0.1:8080/v1/devices |
  tee .data/homelab-devices.json >/dev/null
curl -fsS http://127.0.0.1:8080/v1/entities |
  tee .data/homelab-entities.json >/dev/null

python3 - <<'PY'
import json

with open(".data/homelab-devices.json") as stream:
    devices = {device["id"]: device["name"] for device in json.load(stream)["items"]}
with open(".data/homelab-entities.json") as stream:
    entities = json.load(stream)["items"]

for entity in entities:
    state = (entity.get("state") or {}).get("value")
    device = devices.get(entity["device_id"], entity["device_id"])
    print(device, entity["id"], entity["type"], entity["name"], state)
PY

curl -fsS http://127.0.0.1:8080/v1/devices/dev_<id>
curl -fsS http://127.0.0.1:8080/v1/entities/ent_<id>
```

Ask the user which test device to exercise (e.g. a table lamp with
power/brightness/color-temperature entities plus any ambient-temperature
entity). Temperature entities are read-only (`hearth.temperature/v1`,
milli-Celsius state, empty operation support) and never create command
routes.

Commands (require explicit user confirmation — physical actuation). Batch the
announce-plus-confirm into one structured question when the harness offers
it, with one option per capability. A command
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

Prior-session `.data/homelab-*` files and the sqlite db are safe to reuse —
`tee` overwrites evidence snapshots in place. Save evidence under ignored
`.data/` first, then hand-craft repo fixtures:

- Adapter + hearthd logs: pull from the panes when needed —
  `herdr pane read <pane-id> --source recent-unwrapped --lines 500 >
  .data/homelab-<name>.log` (run from your own shell; `.data/` is ignored).
  Fresh worktrees may need `mkdir -p .data` first.
- HTTP evidence: `curl -s <url> | tee .data/homelab-<what>.json`.
- Raw MQTT evidence (`mqtt_sub` ships via mise as `npm:mqtt`; bare in
  activated shells and Herdr panes, otherwise `mise x -- mqtt_sub`).
  This CLI has no message-count flag, so take one-shot reads with
  background + sleep + kill, and omit `-v` so the file stays pure JSON:

```sh
mqtt_sub -h HOMELAB -t 'zigbee2mqtt/bridge/devices' > .data/homelab-bridge-devices.json & sub=$!; sleep 8; kill $sub 2>/dev/null; wait $sub 2>/dev/null
```

  Retained `bridge/devices` gives per-device exposes plus model, vendor,
  and `software_build_id` (correlate to Hearth entities via
  `friendly_name`; the adapter log also prints it on warnings). Retained
  `bridge/info` gives the Z2M/coordinator versions — extract only the
  fields you need, never persist the whole payload: it carries the real
  `pan_id`/`ext_pan_id`. Redact `ieee_address` to a short prefix
  anywhere you save it. Device state topics (`zigbee2mqtt/<name>`) are
  NOT retained, so catching a live report needs a longer watch; do not
  block fixture work on it — the adapter log is the tripwire for
  rejected values.
- SUBSCRIBE ONLY with `mqtt_sub`. `mqtt_pub` actuates physical hardware
  exactly like an API command, so it needs the same explicit user
  confirmation first.
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

Closing the tab stops everything: Herdr reaps the pane processes on
tab close (verified — no separate stop step needed), so this is the
whole cleanup when this session created the tab:

```sh
herdr tab close <real-device-validation-tab-id>
```

If you want graceful shutdown first (lets `hearthd` flush cleanly
instead of an abrupt kill), SIGINT the processes before closing:

```sh
pkill -INT -f "homelab-zigbee2mqtt\.yaml"; sleep 4
pkill -INT -f "homelab-hearthd\.yaml"; sleep 4
```

(`herdr pane send-keys` does not accept `ctrl-c` as a key name, so
Ctrl+C-via-the-pane is not available.)

Leave the homelab server exactly as found. Local `configs/homelab-*.yaml`
and `.data/homelab-*` files are ignored and safe to keep for the next
session.
