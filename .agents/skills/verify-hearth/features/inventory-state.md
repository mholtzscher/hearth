# Inventory and State

An operator discovers Devices and Entities, reads current typed State, and
checks which Observation Core recorded. Simulator publication is input, not the
authoritative result.

## Sub-features

- `inventory.device`: Device selection exposes its Entities.
- `inventory.entity`: list/filter, jump, and detail agree on canonical identity.
- `state.observation`: a temperature report appears with its exact Observation ID.

## How to get to it (user POV)

- **Devices** → **Simulated light** → **Temperature**.
- **Entities**, optionally filter by Device ID, then open the Entity.
- Toolbar **Jump to entity id**, or a shareable `#/entities/<id>` URL.
- HTTP `GET /v1/devices`, `/v1/entities`, and `/v1/entities/{id}`.

## Driving it with agent-browser and curl

Preconditions: baseline and shared shell setup from [the index](README.md).

- **Discover identity:**

  ```sh
  TEMPERATURE=$(entity_id sim-healthy simulated-light temperature)
  curl -fsS --max-time 2 "$CORE/v1/entities/$TEMPERATURE" > "$EVIDENCE/temperature-before.json"
  DEVICE=$(jq -er .device_id "$EVIDENCE/temperature-before.json")
  curl -fsS --max-time 2 "$CORE/v1/devices/$DEVICE?entity_limit=200" > "$EVIDENCE/light-device.json"
  jq -e --arg id "$TEMPERATURE" 'any(.entities[]; .id == $id)' "$EVIDENCE/light-device.json"
  ```

- **Device navigation:**

  ```sh
  agent-browser open "$WEB/#/devices"
  agent-browser wait --text 'Simulated light'
  agent-browser find role button click --name 'Simulated light' --exact
  agent-browser wait --text 'Device detail'
  agent-browser find role link click --name Temperature --exact
  agent-browser wait --url "**/entities/$TEMPERATURE"
  agent-browser snapshot > "$EVIDENCE/temperature-device-route.txt"
  ```

- **Other browser entry points:** open `"$WEB/#/entities"`; fill label
  `device_id filter` with `"$DEVICE"` and click button **Apply**. Snapshot and
  choose the **Temperature** link in the row with the discovered ID. For jump,
  `agent-browser find label 'Jump to entity id' fill "$TEMPERATURE"` then
  `agent-browser press Enter`. Save each route separately; opening the direct
  URL alone does not prove navigation/filter/jump.
- **Inject one report and prove Core recorded it:**

  ```sh
  curl -fsS --max-time 2 -X POST "$SIM/v1/sim/entities/$TEMPERATURE/pause" > "$EVIDENCE/temperature-paused.json"
  printf '%s\n' '{"value":25000}' > "$EVIDENCE/temperature-input.json"
  curl -fsS --max-time 2 -X POST "$SIM/v1/sim/entities/$TEMPERATURE/publish" \
    -H 'content-type: application/json' --data-binary @"$EVIDENCE/temperature-input.json" \
    > "$EVIDENCE/temperature-published.json"
  OBS=$(jq -er .observation_id "$EVIDENCE/temperature-published.json")
  wait_core "/v1/entities/$TEMPERATURE" "$EVIDENCE/temperature-core.json" \
    '.state.observation_id == $id and .state.value == 25000' --arg id "$OBS"
  wait_core "/v1/entities/$TEMPERATURE/state/history?limit=50" "$EVIDENCE/temperature-history.json" \
    'any(.items[]; .observation_id == $id and .value == 25000 and
      (.disposition == "applied" or .disposition == "unchanged"))' --arg id "$OBS"
  agent-browser find role button click --name Refresh --exact
  agent-browser wait --text "$OBS"
  agent-browser snapshot > "$EVIDENCE/temperature-after.txt"
  agent-browser screenshot "$EVIDENCE/temperature-after.png"
  ```

  Expect typed State `25000`, or 25°C in milli-Celsius, matching canonical ID,
  Observation ID, and a read-only Entity without a Command sender.

## Gotchas

- **Power** and **Temperature** occur on multiple Devices. Disambiguate by the
  discovered canonical ID, not the first text match on the household list.
- Refresh detail explicitly: these views are not all live-polling. The header's
  readiness polling does not refresh State.
- A repeated value can be `unchanged`, not `applied`. Pause may race an in-flight
  ticker; exact-ID history remains useful when current State is superseded.
- Missing **Simulated light** or **Temperature** means wrong preset, registration
  failure, or wrong target. Diagnose; do not manufacture canonical IDs.
