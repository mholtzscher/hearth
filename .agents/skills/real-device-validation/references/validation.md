# Read-only validation and device evidence

Load after startup readiness. `HOMELAB` is the host used by the startup task.
All physical commands still require explicit user confirmation.

## Discover devices and entities

Collection responses use an `items[]` envelope. Join entity `device_id` to the
device name: entity names such as `Power` alone do not identify hardware.

```sh
mkdir -p .data
curl -fsS http://127.0.0.1:8080/v1/devices > .data/homelab-devices.json
curl -fsS http://127.0.0.1:8080/v1/entities > .data/homelab-entities.json
python3 - <<'PY'
import json
with open('.data/homelab-devices.json') as stream:
    devices = {d['id']: d['name'] for d in json.load(stream)['items']}
with open('.data/homelab-entities.json') as stream:
    entities = json.load(stream)['items']
for entity in entities:
    print(devices.get(entity['device_id'], entity['device_id']), entity['id'],
          entity['type'], entity['name'], (entity.get('state') or {}).get('value'))
PY
curl -fsS http://127.0.0.1:8080/v1/devices/dev_<id>
curl -fsS http://127.0.0.1:8080/v1/entities/ent_<id>
```

For browser smoke tests, inspect the entity/device/adapter views and NATS
monitoring. Do not click enablement switches, command presets, or Send command
without confirmation. Measurement entities (`hearth.measurement/v1`)
— temperature, humidity, illuminance, and battery — are read-only, have empty
operation support, and never create a command route. Each reads a finite JSON
number in the canonical unit declared by its own support (`Cel` for temperature,
`%` for humidity and battery, `lx` for illuminance), so read the descriptor
instead of inferring meaning from the Entity name or unit; the web client labels
`Cel` as °C. `measurement_kind` is immutable support and a changed kind rejects
re-registration.

## Physical commands — confirmation required

Ask which device and capabilities to exercise. Batch the planned command values
into one structured confirmation question, with an option per capability.
Execute one capability at a time, re-read its entity, and verify that the command
is satisfied by a fresh, non-retained device report matching the request.

```sh
curl -X POST http://127.0.0.1:8080/v1/entities/ent_<power-id>/commands \
  -H 'content-type: application/json' -d '{"operation":"set","parameters":{"value":true}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_<brightness-id>/commands \
  -H 'content-type: application/json' -d '{"operation":"set","parameters":{"value":50}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_<colortemp-id>/commands \
  -H 'content-type: application/json' -d '{"operation":"set","parameters":{"value":370}}'
```

Record prior state. Restore it if the user wants the room restored. Cross-check
surprising state in the Z2M frontend at `http://HOMELAB:8082/` (read-only).

## Collect minimal evidence

Reuse ignored `.data/homelab-*` snapshots; overwriting them is safe. Pull logs
from the task's pane IDs, using redirects in your own shell, not Nushell panes:

```sh
herdr pane read <pane-id> --source recent-unwrapped --lines 500 > .data/homelab-adapter.log
```

`mqtt_sub` is available through mise (`npm:mqtt`). Subscribe only; `mqtt_pub`
requires the same confirmation as an API command. This CLI has no message-count
flag, so take one-shot reads with background + sleep + kill; omit `-v` for JSON:

```sh
mise x -- mqtt_sub -h HOMELAB -t 'zigbee2mqtt/bridge/devices' > .data/homelab-bridge-devices.json & sub=$!; sleep 8; kill $sub 2>/dev/null; wait $sub 2>/dev/null
```

Retained `bridge/devices` supplies exposes, model, vendor, and
`software_build_id`. Correlate using `friendly_name`. Redact `ieee_address` to
a short prefix in saved evidence. Retained `bridge/info` supplies Z2M and
coordinator versions, but contains real PAN IDs: extract only needed fields,
never persist the entire payload. Device state topics are not retained; a live
report may require a longer watch. Do not block fixture work on that watch.
Adapter logs reveal rejected values even when adapter health is healthy.

Hand-craft minimal fixtures under `internal/adapters/zigbee2mqtt/testdata/`:
one device, relevant exposes only, no raw database/configuration dumps or keys.
Document model, visible firmware/build ID, exercised entity kinds, and date.

## Troubleshooting

| Symptom | Next step |
|---|---|
| `hearth.external_system_unavailable` | Recheck MQTT `HOMELAB:1883` and Z2M frontend reachability. |
| `bridge_offline` | Ask the operator to inspect coordinator/Z2M; do not restart it. |
| `incompatible_configuration` | Read-only check: MQTT version 4, availability enabled, optimistic false. Ask before changes. |
| `invalid_inventory` | Ask the operator to correct malformed retained bridge topics. |
| Adapter `unknown` | Read adapter logs; check retained bridge topics and core `/readyz`. |
| `/readyz` flapping | Check competing remote core at `:8081`; stop only this run's local tab and ask which core should own NATS. |
| Empty entity list | Check base topic, supported exposes, and completed device interviews. |
