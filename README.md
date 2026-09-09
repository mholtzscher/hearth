# Hearth

Hearth is a home automation system for technical self-hosters. It is being designed to replace Home Assistant functionally for one household per deployment while retaining mature specialist protocol services where useful.

**Naming:** Hearth is the product and user-facing namespace. `hearthd` is reserved for the core daemon; companion processes use `hearth-` names.

The project has two equal gates: work must advance a useful home automation system and meaningful NATS learning, while every production use of NATS must solve a real system need.

## Status

Hearth's first vertical slice observes and controls one Home Assistant-managed light; the simulator exercises recovery and the complete failure matrix against the same contracts.

## Development

Install tools with `mise install` and start local NATS/JetStream with `mise run nats`. The validation gate is `mise run validate`; it regenerates checked-in code, formats Go files, tidies module metadata, and then checks generation, formatting, module tidiness, linting, all authoritative schemas and cross-binary fixtures, race-enabled tests (including runtime OpenAPI and recovery), and vetting. Run `mise run ko-build` for no-push multi-platform release container builds. Regenerate checked-in Entity-type and database access code after changing its inputs with `mise run generate`.

The checked-in golangci-lint config tracks [maratori/golangci-lint-config](https://github.com/maratori/golangci-lint-config) at the version of golangci-lint locked by mise. Existing findings are baselined at the commit recorded in the lint task, while validation rejects findings introduced afterward. Update the tool and config together with `mise upgrade golangci-lint && mise run update-lint-config`, then review and validate the resulting changes.

Run a small mutation-testing trial with `mise run mutation-test -- ./contracts/v1`, then pass another package or subtree after `--` to widen the run. Gremlins is much slower than the regular test suite, so it is not part of `validate`; investigate surviving mutants as missing behavioral guarantees rather than chasing the score. `.gremlins.yaml` allows extra test-startup time and excludes checked-in generated Go files.

`hearthd` requires `household_timezone` (an IANA name such as `America/New_York`, or `UTC`); timezone changes require restart. `automation_history_retention` defaults to `720h` and must be at least `24h`. Terminal Run history is pruned hourly, never on startup; active Runs remain until recovery or completion.

`hearthd` accepts any configured HTTP bind address. The example remains `127.0.0.1:8080`; bind to a non-loopback address only on a trusted network because the HTTP API has no authentication.

Run the first-light simulator with `go run ./cmd/hearth-simulator -config configs/simulator.yaml` after copying `configs/simulator.example.yaml`. Its `scenario` may be `happy`, `adapter-unhealthy`, `entity-unavailable`, `delayed-source-time`, `future-clock-skew`, `upstream-rejection`, `no-op-refresh`, `overlapping-opposite-command`, `outcome-timeout`, `interrupted-command`, or `restart-before-ack`. `happy` reports a healthy Adapter and available Entity before publishing State. `adapter-unhealthy` proves that Core rejects a Command before dispatch. `entity-unavailable` proves that availability is advisory: Core dispatches the Command, and the simulator reports the Entity available after the recovery attempt succeeds. Heartbeat expiry, takeover, stale-runtime isolation, Core readiness recovery overlays, and graceful release remain deterministic process-test scenarios. Raw duplicate and malformed Observation cases remain transport-test scenarios.

### Automation management and manual execution

Automations support schema-backed definitions and inspectable manual Runs. Cron expressions are validated and retained, but **automatic scheduling is not implemented yet** (spec 2). Use the [canonical definition schema](internal/modules/automations/automation-definition.schema.json) or `/openapi.json` for authoring. The [example definition](internal/modules/automations/testdata/automation-definitions/valid-power.json) validates against that schema; replace its example Entity ID with a registered canonical ID before saving.

```sh
base=http://127.0.0.1:8080
# Copy the example and replace entity_id; saving validates every Step without dispatch.
curl -i -X POST "$base/v1/automations" \
  -H 'content-type: application/json' --data-binary @automation.json
curl "$base/v1/automations?limit=50"
# Use the aut_ ID returned by POST, and the current revision from GET.
automation_id=aut_...
curl "$base/v1/automations/$automation_id"
curl -X PUT "$base/v1/automations/$automation_id?expected_revision=1" \
  -H 'content-type: application/json' --data-binary @automation.json
# This explicitly actuates devices, even when the Automation is disabled.
curl -i -X POST "$base/v1/automations/$automation_id/runs" \
  -H 'Idempotency-Key: evening-test-1'
curl "$base/v1/automation-runs?automation_id=$automation_id&limit=50"
curl "$base/v1/automation-runs/arn_..."
# Delete the live definition at its current revision; history remains retained.
curl -X DELETE "$base/v1/automations/$automation_id?expected_revision=2"
```

POST returns 201 and a Location. PUT fully replaces the definition and increments its revision; omitted `enabled` defaults to false for both POST and PUT. Revision metadata is not part of the JSON definition. Missing/nonpositive revisions are 422; stale revisions and active-Run admission/deletion conflicts are 409 with stable codes.

Manual invocation takes **no body** and requires a 1–128 character printable ASCII key without whitespace. A new invocation returns 202 immediately with a Run Location. Repeating a retained key returns 200 and the original Run, even after edits, completion, disablement, or deletion; use a new key for another execution. One Run per Automation may be active. Client disconnects do not cancel admitted work, and there is no cancellation endpoint. Steps execute sequentially without retries or rollback; inspect the Run for failures rather than expecting a delayed HTTP gateway error. `satisfied`/`observed` and `dispatched` describe Command evidence, not guaranteed physical effects.

Collections return `items: []` when empty and an optional `next_cursor`. Limits default to 50 (1–200); pass the opaque cursor with the same collection and Automation filter. Definitions sort by ID ascending, Runs by start time/ID descending. Continuations are not snapshots; concurrent inserts and pruning can change later pages. History summaries omit Step parameters; full Run detail retains the immutable definition snapshot and owned Command evidence, but never idempotency keys or internal correlation markers.

Terminal history is eligible for hourly pruning strictly before the retention cutoff (no startup pruning). Once a key is pruned, reusing it starts a new Run only if the live definition still exists; deleted definitions and pruned Runs return 404. Shutdown closes Run and next-Step admission and drains current Commands before tearing down dependencies. Executor persistence faults degrade `/readyz` and return `automation_unavailable` (503) for admission; uncertain Runs retain active claims until restart recovery interrupts them without replay.

### Home Assistant migration adapter

Copy `configs/homeassistant.example.yaml` to the ignored `configs/homeassistant.yaml`, configure one Home Assistant light, and place a long-lived access token at the configured ignored `token_file` path. With NATS and `hearthd` running, start the disposable adapter:

```sh
go run ./cmd/hearth-adapter-homeassistant -config configs/homeassistant.yaml
```

The registration log reports the canonical Entity ID. The Adapter reports healthy only after its WebSocket subscription, snapshot, and buffered-event reconciliation are ready. Home Assistant `on` and `off` values report the Entity available and publish State. `unavailable` and `unknown` report it unavailable without replacing the last State.

Use the Adapter slug and canonical Entity ID to inspect current evidence, then verify a real on/off command. A successful command response is returned only after the adapter publishes its linked refresh Observation:

```sh
curl http://127.0.0.1:8080/v1/adapters/homeassistant
curl http://127.0.0.1:8080/v1/entities/ent_...
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}'
```

### Zigbee2MQTT adapter

`hearth-adapter-zigbee2mqtt` connects an operator-managed Zigbee2MQTT service to Hearth. Zigbee2MQTT 2.13.0 and NATS Server 2.12 are the tested versions. Other versions are not runtime-blocked, but must provide the same retained MQTT payloads and behavior.

Configure Zigbee2MQTT to use MQTT 3.1.1 and to publish explicit availability while global optimistic updates are disabled. The effective `bridge/info` settings must contain:

```yaml
mqtt:
  version: 4
availability:
  enabled: true
device_options:
  optimistic: false
```

Every Zigbee2MQTT `friendly_name` considered by the adapter must match `^[a-z0-9][a-z0-9_-]{0,62}$`; slashes, dots, whitespace, wildcards, and uppercase letters are not supported. Set a human-readable Zigbee2MQTT `description` when a display name distinct from the route-safe friendly name is wanted.

The checked-in NATS configuration enables file-backed JetStream and both native NATS and MQTT listeners on loopback. Copy the adapter example, then verify that its MQTT URL, base topic, and Zigbee2MQTT broker settings refer to the same MQTT listener:

```sh
cp configs/hearthd.example.yaml configs/hearthd.yaml
cp configs/zigbee2mqtt.example.yaml configs/zigbee2mqtt.yaml
mise run nats
go run ./cmd/hearthd -config configs/hearthd.yaml
go run ./cmd/hearth-adapter-zigbee2mqtt -config configs/zigbee2mqtt.yaml
```

Run Zigbee2MQTT separately under the operator's normal supervision. The adapter configuration accepts only plain `mqtt://` or `tcp://` endpoints with an explicit host and port. It has no MQTT username, password, TLS, or certificate settings. Native NATS, MQTT, Hearth HTTP, and Zigbee2MQTT management endpoints must remain on loopback or a trusted private network; exposing this configuration to an untrusted network is unsupported.

The adapter remains `unknown` until it has claimed a Hearth session, connected and subscribed to MQTT, received retained `bridge/state`, `bridge/info`, and `bridge/devices`, and completed registration. It becomes healthy after an online bridge and compatible configuration are reconciled. Device availability comes only from explicit `<friendly_name>/availability` messages; State does not imply availability.

Use the HTTP API to discover the registered power, brightness, color-temperature, color, color-mode, ambient-temperature, humidity, battery, smart-plug electrical, smart-plug setting, and reset-action Entity IDs, inspect adapter health and Entity availability, and send typed commands. Lights, relays, and sensors register as Device kinds `light`, `relay`, and `sensor`; one IEEE address produces one Device, with temperature, humidity, and battery Entities supplementing either actuator family, and with electrical sensors, numeric settings, power-on behavior, and the reset action supplementing the relay family on smart plugs. Color temperature uses Zigbee2MQTT's native integer mired unit and each Entity reports its discovered range in `support` (for example, 153–500 mireds). Color-capable lights add native `hearth.colorxy/v1` and/or `hearth.colorhs/v1` Entities plus a read-only `hearth.colormode/v1` Entity reporting `xy`, `hs`, or `color_temp`. XY State uses scaled integers in ten-thousandths (`3125` means `0.3125`); HS State uses whole degrees `0..359` and whole percentage points. Color-temperature State is the object form `{"active":true,"value":370}`: this unreleased type changed from scalar State, so operators with databases holding the old contract discard them explicitly. Each coordinate Entity carries an `active` flag for the mode selected by the same-message `color_mode`. Ambient temperature is read-only `hearth.temperature/v1` with integer milli-Celsius State (for example, `21.5` °C reports `21500`) and empty operation support. Relative humidity and battery level are read-only `hearth.numericsensor/v1` Entities with percent State in 0–100 (unit `%`, fractional readings preserved) and empty operation support:

```sh
curl http://127.0.0.1:8080/v1/adapters/zigbee2mqtt
curl http://127.0.0.1:8080/v1/entities
curl http://127.0.0.1:8080/v1/entities/ent_...
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":50}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":370}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"x":3125,"y":3291}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"trigger","parameters":{"name":"blink"}}'
```

A color-temperature set publishes `{"color_temp":370}` to the Device's Zigbee2MQTT `/set` topic, then requests `{"color_temp":""}` from `/get`. The HTTP request succeeds only after a fresh, non-retained report returns exactly 370 mireds with temperature mode active. A color set publishes `{"color":{"x":0.3125,"y":0.3291}}` (HS: `{"color":{"hue":120,"saturation":80}}`), then requests `{"color":""}` from `/get`. Set payloads never include power, brightness, transition, or unrelated properties. Color commands succeed only on active, within-tolerance evidence: ±1 per XY axis, ±2° hue and ±1 saturation point. Satisfaction means `active:true` plus a within-tolerance value under the existing freshness gates, an operational contract rather than a promise of exact physical color. Zigbee2MQTT caching or `color_sync` conversions may carry cached fields into a fresh publication, and Hearth receives no per-field provenance. Ambient-temperature, humidity, and battery Entities never create command routes. Startup `/get` covers only Entities whose expose grants get access, so a publish-only sensor refreshes from live reports while a gettable sensor also receives active `/get`.

Bulbs exposing the Third Reality `3RCB01057Z` attributes add four more Entities: read-only link quality as generic `hearth.numericsensor/v1` (integer 0–255, unit `lqi`), power-restore behavior as observable `hearth.enumsetting/v1` (`set {"value": ...}`), startup color temperature as observable `hearth.numericsetting/v1` (`set {"mode":"value","value":N}` or `{"mode":"choice","choice":"previous"}`), and effects as stateless `hearth.enumaction/v1` (`trigger {"name": ...}`, for example `"blink"`). Setting commands complete as `satisfied` only after a fresh matching observation, as usual. An effect trigger instead completes as `dispatched`: adapter MQTT acceptance is durably recorded, but no observation is requested, no state is stored (effect Entities always read `state: null`), and dispatch alone makes no claim about any physical effect. MQTT QoS 1 may deliver duplicates and Hearth performs no automatic retry. Command reads therefore distinguish `dispatched` from `satisfied` results. The current inventory fixture covering these exposes is synthetic and Wanda-shaped; startup on-wire values and effect dispatch outcomes are unobserved until separately authorized live validation, so real-hardware behavior is not guaranteed.

Smart plugs exposing the Third Reality `3RSP02028BZ` attributes register relay power plus twelve more Entities. Relay power shares the light power constructor, so `set {"value":true}` publishes the discovered `ON` scalar and succeeds only on fresh matching evidence under the unchanged command-evidence gates. Power-on behavior is the shared observable `hearth.enumsetting/v1` (`set {"value": ...}`). Six read-only `hearth.numericsensor/v1` electrical sensors report AC frequency (`Hz`), electrical power (`W`, key `electricalpower` to distinguish it from boolean relay power), power factor (empty upstream unit mapped explicitly to `ratio`), energy (`kWh`), current (`A`), and voltage (`V`); fractional readings are preserved and get access alone controls startup refresh. Three observable `hearth.numericsetting/v1` settings cover LED brightness (`%`, 0–100) and both turn on/off countdowns (`s`, 0–65535) in numeric value mode (`set {"mode":"value","value":N}`), with decimals preserved and generated `SetSatisfied` matching. Reset Total Energy is a stateless `hearth.enumaction/v1` (`trigger {"name":"Reset"}`) that completes as `dispatched` with no observation, like an effect trigger. Because `hearth.numericsensor/v1` requires finite min/max while Zigbee2MQTT omits electrical bounds, sensors use conservative validation envelopes (not claimed operating ranges) unless the expose carries both valid finite bounds, which are then preferred. All plug attributes attach only under surviving relay power and isolate malformed siblings individually.

### Profile catalog

Entity mappings live in embedded JSON profiles under `internal/adapters/zigbee2mqtt/profiles/`, validated by `profile.schema.json` (`urn:hearth:schema:zigbee2mqtt-profile:v1`) and compiled into one immutable `ProfileCatalog` before any NATS or MQTT connection. Profiles declare mapping intent only; a closed set of twelve Go strategies (`binary-power`, `brightness`, `color-temperature`, `color-xy`, `color-hs`, `color-mode`, `startup-color-temperature`, `temperature`, `numeric-sensor`, `numeric-setting`, `enum-setting`, `enum-action`) owns all typed State, Command, and outcome behavior. The profile catalog is the sole production mapping path: there are no operator-supplied profile paths, plugins, feature flags, or dual planning paths.

Candidate groups select roots by exact expose type and optional exact name in retained inventory order. A root selector with `"cardinality": "unique"` instead requires exactly one device-wide match and plans nothing when the match is missing or duplicated; link quality uses this to preserve its duplicate-roots-are-ambiguous contract without a mapping-specific Go branch.

To add a mapping covered by an existing strategy, edit the embedded profile JSON, add or extend a `bridge-devices`/`state` fixture pair under `internal/adapters/zigbee2mqtt/testdata/`, and run `mise run validate` (which also recompiles the catalog). A mapping needing new behavior requires a new Go strategy plus a schema-version review, never profile-executable logic.

Whole-catalog compilation fails adapter startup on any schema, limit, reference, strategy, or override conflict and never returns a partial catalog; malformed runtime inventory stays isolated per Device and expose. A catalog failure exits before the adapter claims a Hearth session or dials MQTT and is reported as startup stage `load_profile_catalog` with `error_code=profile_catalog_invalid` (safe IDs and JSON pointer only, never profile contents or payloads). Fix the repository profile and restart; no operator configuration change can work around it.

Vendor, model, and firmware quirks live in `profiles/overrides/` as exact-match patches backed by captured fixture evidence, never as model-specific generic rules. For one target rule the base layer is overlaid by at most one general vendor/model patch and then at most one exact-build patch. A patch may disable a rule, replace its source or expose match, or fully replace strategy parameters against the target strategy; it cannot add rules or change rule IDs, Entity keys, display names, strategy names, contribution, group, dependency, order, or identity.

For diagnostics, check adapter logs together with the adapter and Entity reads:

- `hearth.external_system_unavailable` indicates that the MQTT broker cannot be reached or the connection was lost; verify the listener, URL, and network boundary.
- `adapter.hearth-adapter-zigbee2mqtt.bridge_offline` indicates an explicit offline `bridge/state`; check Zigbee2MQTT and its coordinator.
- `adapter.hearth-adapter-zigbee2mqtt.incompatible_configuration` indicates an MQTT version other than 4, disabled availability, or optimistic behavior not explicitly false.
- `adapter.hearth-adapter-zigbee2mqtt.invalid_inventory` indicates malformed retained bridge information or Device inventory. Republish valid retained `bridge/info` and `bridge/devices` data by correcting or restarting Zigbee2MQTT.
- Invalid friendly names, groups, disabled or unsupported Devices, incomplete interviews, ambiguous endpoint exposes, and unsupported capabilities are isolated and logged rather than registered. Correct Zigbee2MQTT Device metadata and expose definitions.
- An Entity that stays unknown is missing an explicit availability message. Unavailable reasons distinguish `adapter.hearth-adapter-zigbee2mqtt.device_offline`, `adapter.hearth-adapter-zigbee2mqtt.device_missing`, `adapter.hearth-adapter-zigbee2mqtt.device_disabled`, and `adapter.hearth-adapter-zigbee2mqtt.capability_missing`.
- A Command timeout means no fresh, non-retained matching State arrived after dispatch. Verify the Device can answer Zigbee2MQTT `/get` requests and that the reported property and value match its discovered expose.

## Application logs

Hearth executables write to stderr with `--log-level info --log-format text` by default. Use `--log-format json` for filtering, or `--log-level debug` for Observation progress:

```sh
log_dir=$(mktemp -d /tmp/hearth-logs.XXXXXX)
go run ./cmd/hearthd --config configs/hearthd.yaml --log-format json 2>"$log_dir/core.log"
jq -c 'select(.event == "command.created")' "$log_dir/core.log"
```

See [the logging guide](docs/logging.md) for startup/failure diagnosis and safety rules. Use Command-history APIs for durable outcomes, and `/readyz` plus Adapter/Entity reads for current health; logs do not reconstruct those lifecycles.

## Documentation

- [`CONTEXT.md`](./CONTEXT.md): canonical project language
- [`docs/product.md`](./docs/product.md): audience, goals, boundaries, and success
- [`docs/architecture.md`](./docs/architecture.md): current accepted architectural constraints
- [`docs/logging.md`](./docs/logging.md): application logging and operator diagnosis
- [`docs/adr/`](./docs/adr/): durable architectural decisions and their rationale
- [`docs/plans/`](./docs/plans/): implementation plans
- [`specs/`](./specs/): approved implementation-ready specifications

The approved first-slice contract is [`specs/first-light.md`](./specs/first-light.md).
