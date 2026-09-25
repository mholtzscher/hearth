# Hearth

Hearth is a home automation system for technical self-hosters. It is being designed to replace Home Assistant functionally for one household per deployment while retaining mature specialist protocol services where useful.

**Naming:** Hearth is the product and user-facing namespace. `hearthd` is reserved for the core daemon; companion processes use `hearth-` names.

The project has two equal gates: work must advance a useful home automation system and meaningful NATS learning, while every production use of NATS must solve a real system need.

## Status

Hearth's first vertical slice observes and controls one Home Assistant-managed light; the simulator exercises recovery and the complete failure matrix against the same contracts.

## Development

Install tools with `mise install` and start the local NATS/JetStream and Mosquitto brokers with `mise run brokers`. The validation gate is `mise run validate`; it regenerates checked-in code, formats Go files, tidies module metadata, and then checks generation, formatting, module tidiness, linting, all authoritative schemas and cross-binary fixtures, race-enabled tests (including runtime OpenAPI and recovery), and vetting. Run `mise run ko-build` for no-push multi-platform release container builds. Regenerate checked-in Entity-type and database access code after changing its inputs with `mise run generate`.

The checked-in golangci-lint config tracks [maratori/golangci-lint-config](https://github.com/maratori/golangci-lint-config) at the version of golangci-lint locked by mise. Existing findings are baselined at the commit recorded in the lint task, while validation rejects findings introduced afterward. Update the tool and config together with `mise upgrade golangci-lint && mise run update-lint-config`, then review and validate the resulting changes.

Run a small mutation-testing trial with `mise run mutation-test -- ./contracts/v1`, then pass another package or subtree after `--` to widen the run. Gremlins is much slower than the regular test suite, so it is not part of `validate`; investigate surviving mutants as missing behavioral guarantees rather than chasing the score. `.gremlins.yaml` allows extra test-startup time and excludes checked-in generated Go files.

`hearthd` requires `household_timezone` (an IANA name such as `America/New_York`, or `UTC`); timezone changes require restart.

`hearthd` also requires the household agent's model API key at the configured `agent.api_key_file` path. The agent is a required Core module, so Core fails startup when that secret file is missing or empty; see [Agent](#agent) for the configuration block.

`hearthd` accepts any configured HTTP bind address. The example remains `127.0.0.1:8080`; bind to a non-loopback address only on a trusted network because the HTTP API has no authentication.

Run the first-light simulator with `go run ./cmd/hearth-simulator -config configs/simulator.yaml` after copying `configs/simulator.example.yaml`: it declares one scripted `simulated-light` power Device that reports a healthy Adapter and available Entity before publishing State. Devices and Entities are grouped under `adapters:`; each Adapter has independent health, and the optional loopback control channel can publish, pause, and resume scripts. Device faults are config, not code: unhealthy health with omitted availability, initially unavailable Entities, rejected or outcome-less Commands, and source and received clock offsets. See `specs/simulator-harness.md`; `configs/simulator.scripted.example.yaml` adds a second Device, and `configs/simulator.full.example.yaml` covers all sixteen built-in Entity types on a healthy Adapter while isolating faults on separate Adapters. Heartbeat expiry, takeover, stale-runtime isolation, Core readiness recovery overlays, and graceful release remain deterministic process-test scenarios. Raw duplicate and malformed Observation cases remain transport-test scenarios.

For automated simulator validation inside Herdr, run `mise run simulator-start`
(or add `-- --dashboard`). It creates an isolated local NATS/JetStream, Core,
and scripted simulator stack in an owned tab; Docker and Mosquitto are not
needed. Use `-- --preset full` for the broad inventory or `-- --devices PATH`
for a YAML sequence of custom Devices. Run `mise run simulator-stop` to close
only that tab while preserving configs, logs, and data. See the
[simulator validation skill](.agents/skills/simulator-validation/SKILL.md).

### Entity Event recovery recipe

Entity Events are named occurrences, not State. The scripted `simulated-button` Device in `configs/simulator.scripted.example.yaml` registers an `events` Entity beside the `simulated-light` power Device; `power` still accepts `set` Commands and reports State, while `events` advertises `single_press` and `double_press` and reads `state: null` forever. This is the proof recipe for the Core-offline guarantee and requires no hardware and no automations:

```sh
cp configs/hearthd.example.yaml configs/hearthd.yaml
cp configs/simulator.scripted.example.yaml configs/simulator.yaml
mkdir -p .data
printf '%s\n' '<model-api-key>' > .data/agent-api-key   # ignored; the required agent secret
mise run brokers
go run ./cmd/hearthd -config configs/hearthd.yaml
go run ./cmd/hearth-simulator -config configs/simulator.yaml
```

Each loopback control-channel request publishes one report for the `events` Entity and returns its canonical publication ID. Stop `hearthd` (Ctrl-C) with the simulator still running and connected, publish a few more reports, then start `hearthd` again with the same `sqlite_path`. After the restart, Core records the backlog and the history endpoint returns each report exactly once:

```sh
curl -X POST http://127.0.0.1:8181/v1/sim/entities/<events_ent_id>/publish -d '{"name":"single_press"}'
curl http://127.0.0.1:8080/v1/entities/<power_ent_id>
curl http://127.0.0.1:8080/v1/entities/<events_ent_id>
curl 'http://127.0.0.1:8080/v1/entities/<events_ent_id>/events?limit=50'
```

The registration log reports both canonical Entity IDs. The event history response shows the schema-constrained reported name together with whether Core recorded the report (`accepted`) or why Core rejected it (`stale_runtime`, `unknown_entity`, `wrong_adapter`, `entity_disabled`, or `unsupported_event`). It never proves that a physical press happened, and it is not State history: `GET /v1/entities/{entity_id}/state/history` never contains events. Readiness covers the session, the Observation consumer, the Entity Event stream and consumer configuration, and both consumers' activity; it never waits for unread backlog. The guarantee covers broker-acknowledged input only while NATS and JetStream remain available and the Adapter process is alive: initial claim and registration need Core, an Adapter restart during a Core outage loses unacknowledged work, a NATS process loss is not covered, and stream limits can discard old input during a long outage. Reports are not replayed into Commands, and nothing in this path executes work.

### Device Facts

Core publishes **Device Facts** for accepted Observations and accepted Entity Events. The devices SQLite transaction that records the evidence also writes a pending fact row in the same commit, and one relay publishes pending facts oldest-first into the JetStream stream `HEARTH_DEVICE_FACTS_V1` (`hearth.v1.core.fact.>`), deleting each row only after the broker acknowledges it. The durable SQLite record and the HTTP read APIs stay authoritative: a fact reports what Core recorded, not physical truth, and it never turns an HTTP history read into automatic catch-up.

Publication is at-least-once from that durable outbox. A broker outage, a relay restart or a Core restart delays publication but does not lose a queued fact, and a retry reuses the same fact identity, subject and payload bytes. The broker collapses a repeated fact identity only inside the stream's two-hour duplicate window, so a retry after a long outage can store the same fact twice: treat the fact `id` as an idempotency key. The stream retains facts for seven days or one GiB, whichever comes first, and evicts the oldest first, so a reader that falls further behind than that permanently misses the evicted facts.

Command lifecycle is deliberately not a fact family. Requested, accepted, satisfied, dispatched and every failure or interrupted status are exposed only through durable Command history (`GET /v1/commands/{command_id}` and `GET /v1/entities/{entity_id}/commands`), never as a fact, and a linked Observation fact is not a substitute for a Command's status transition. A stateless `dispatched` or a failure outcome has no fact at all.

With NATS and `hearthd` running, subscribe live with any NATS client while driving the Entity Event or simulator paths above:

```sh
nats sub 'hearth.v1.core.fact.>'                              # every Device Fact
nats sub 'hearth.v1.core.fact.entity.<ent_id>.>'              # one Entity, every family
nats sub 'hearth.v1.core.fact.entity.*.entity-event.>'        # accepted Entity Events
nats sub 'hearth.v1.core.fact.entity.*.observation.applied'   # state-changing Observations
```

A plain subscription is live-only: it receives only facts published while it is connected, and it can neither acknowledge nor recover one. When a missed fact is unacceptable, read the stream `HEARTH_DEVICE_FACTS_V1` with a **named durable JetStream consumer** created in your own client library:

- choose `DeliverAll` with an explicit acknowledgement policy to resume from your own acknowledgement floor after a restart, which is the choice that does not miss a stored fact;
- choose `DeliverNew` when a new consumer should see only facts published after it asked;
- narrow the consumer's filter subject to one family or one Entity when that is all you need;
- acknowledge only after your side effect commits, and stay idempotent on the fact `id` because a duplicate is possible;
- remember the seven-day/one-GiB bound: a reader that falls further behind than the stream's retention loses the evicted facts.

Core provisions the stream and no consumer, and never resumes or repairs a reader's position for it. Readiness requires the shared NATS connection, the validated fact stream and an active relay, but never a subscriber and never a fact consumer Core does not own.

The Hearth Debug dashboard exposes `#/device-facts`. It reads a bounded recent snapshot directly from JetStream over the configured NATS websocket, and live streaming remains off until explicitly enabled. Its temporary `DeliverNew` consumer is advisory and non-durable: switching live off or leaving the page deletes it, and reconnecting resumes at the current tail rather than recovering missed facts.

Subjects are `hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>` with Entity-first routing, where `<family>` is `observation` or `entity-event`. Each fact validates against one strict `contracts/v1` schema — `urn:hearth:schema:observation-fact:v1` or `urn:hearth:schema:entity-event-fact:v1` — carries a stable `fct_<uuidv7>` `id` that is also published as `Nats-Msg-Id`, and keeps the durable `obs_`/`evt_` source ID as `causation_id`. `emitted_at` is Core's commit time, not publication time, and the inbound W3C `traceparent`/`tracestate` are restored on every (re)publication. Subscribers must reject a payload whose Entity, family or variant disagrees with its subject, and must not sort by UUID or envelope time to invent a global order.

When a pending fact cannot be mapped to a valid message, the relay preserves the row, stops, logs `device_fact.poison` and fails readiness instead of discarding durable evidence. A transient outbox, publish, acknowledgement or delete failure keeps the row and retries, logging `device_fact.retry`. Neither event contains State values, raw envelopes or full subjects. See [the logging guide](docs/logging.md).

Anyone with broker access can read canonical State values, forge a fact or publish directly into `HEARTH_DEVICE_FACTS_V1`. Facts are unsigned, Hearth adds no fact authentication or authorization, and this feature widens no deployment boundary: trust the broker exactly as for the rest of Hearth's trusted network, and reserve `hearth.v1.core.fact.>` publish permission for Core when NATS authorization exists.

### Automation Conditions

An Automation may require current Entity State before it runs. A Trigger decides *when* an Automation is considered; an optional `conditions` tree decides *whether* that consideration may admit a Run, evaluated once at admission against a coherent batch of retained State. Nodes compose with `all`, `any`, and `not` under three-valued (`true`/`false`/`unknown`) logic, and only a true root admits. Missing, expired, or incompatible evidence is `unknown`, so absence is not permission; a false or unknown decision records an explainable Skip rather than silently doing nothing.

Conditions are optional, and a definition without them keeps its existing unconditional-after-Trigger behavior. Manual invocation applies Conditions unless the operator explicitly requests `{"bypass_conditions": true}`, which bypasses only Conditions — never admission gates, busy checks, or execution-time Command validation. See [the Automation Conditions guide](docs/automation-conditions.md) for the definition field contract, a complete create request, manual and bypass requests, and history inspection.

### Agent

The household agent is a required Core module beside Devices and Automations: Core always constructs it, serves `/v1/agent` on the same listener, and fails startup rather than running without it. Configure it in `configs/hearthd.yaml`:

```yaml
agent:
  api_key_file: .data/agent-api-key   # required local secret file
  model: gpt-5.6-luna                 # default
  base_url: ""                        # provider default; absolute http(s) when set
  reasoning_effort: none              # none, minimal, low, medium, or high; empty selects none for this default model
  max_steps: 20                       # bounds one turn's model plus tools steps
  history_retention: 720h             # conversation retention (default 30d, minimum 24h)
```

Write the model API key to `agent.api_key_file` before starting Core. The key is read once at startup and never logged, persisted, or echoed in diagnostics; the file path and contents stay out of process records. A missing or empty file fails startup at the agent stage. `reasoning_effort: none` matters for the default model, which rejects function tools at higher reasoning levels.

Conversations are durable in Core's SQLite. `POST /v1/agent/conversations` opens one, `POST /v1/agent/conversations/{id}/messages` runs one turn synchronously, `GET /v1/agent/conversations/{id}/messages` reads it back, and `POST /v1/agent/conversations/{id}/messages/stream` streams the same turn as SSE (`turn.started`, `tool.started`, `tool.finished`, `turn.finished`/`turn.failed`). The stream route is a plain Echo handler and stays out of `openapi.json`. The agent's tools are the `/mcp` catalog, so a turn executes the same Commands REST and MCP do. `/readyz` requires open agent admission, and shutdown closes admission, cancels and joins running turns, then closes the agent's MCP sessions before SQLite. The shared history pruning worker deletes whole conversations past `agent.history_retention` in the same pass as Devices and Automations retention.

The dashboard's `/agent` page drives these routes server-side; the browser holds no model key. Each turn rebuilds only the newest complete turns inside a fixed history budget, so an active conversation cannot grow until the provider rejects it. See `internal/modules/agent`.

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

For adding device capabilities, see [Extending Zigbee2MQTT capabilities](docs/zigbee2mqtt-capabilities.md): typed mapping tables for repetitive sensors/settings, with family planning and conversions kept in Go.

`hearth-adapter-zigbee2mqtt` connects an operator-managed Zigbee2MQTT service to Hearth. Tested versions are Zigbee2MQTT 2.13.0 and 2.14.1 with Mosquitto 2.0.22. Other versions are not runtime-blocked, but must provide the same retained MQTT payloads and behavior.

Configure Zigbee2MQTT to use MQTT 3.1.1 and to publish explicit availability while global optimistic updates are disabled. The effective `bridge/info` settings must contain:

```yaml
mqtt:
  version: 4
availability:
  enabled: true
device_options:
  optimistic: false
```

Every Zigbee2MQTT `friendly_name` considered by the adapter must be a single MQTT topic level: 1 to 255 bytes of valid UTF-8 containing no `/`, `+`, `#`, or NUL, and never `bridge`. Spaces, uppercase, punctuation, and non-ASCII text are supported. `mqtt.base_topic` remains a subject-safe slug matching `^[a-z0-9][a-z0-9_-]{0,62}$`. Set a human-readable Zigbee2MQTT `description` when a display name distinct from the routed friendly name is wanted.

The local Compose stack runs file-backed JetStream on NATS and Mosquitto for MQTT 1883, publishing every broker port on loopback only. MQTT is not a NATS listener. Copy the adapter example, then verify that its MQTT URL, base topic, and Zigbee2MQTT broker settings refer to the Mosquitto listener:

```sh
cp configs/hearthd.example.yaml configs/hearthd.yaml
cp configs/zigbee2mqtt.example.yaml configs/zigbee2mqtt.yaml
mkdir -p .data
printf '%s\n' '<model-api-key>' > .data/agent-api-key   # ignored; the required agent secret
mise run brokers
go run ./cmd/hearthd -config configs/hearthd.yaml
go run ./cmd/hearth-adapter-zigbee2mqtt -config configs/zigbee2mqtt.yaml
```

Run Zigbee2MQTT separately under the operator's normal supervision. The adapter configuration accepts only plain `mqtt://` or `tcp://` endpoints with an explicit host and port. It has no MQTT username, password, TLS, or certificate settings. The NATS listener, Mosquitto MQTT, Hearth HTTP, and Zigbee2MQTT management endpoints must remain on loopback or a trusted private network; exposing this configuration to an untrusted network is unsupported.

The adapter remains `unknown` until it has claimed a Hearth session, connected and subscribed to MQTT, received retained `bridge/state`, `bridge/info`, and `bridge/devices`, and completed registration. It becomes healthy after an online bridge and compatible configuration are reconciled. Device availability comes only from explicit `<friendly_name>/availability` messages; State does not imply availability.

Use the HTTP API to discover the registered power, brightness, color-temperature, color, color-mode, ambient-temperature, humidity, illuminance, battery, occupancy, smart-plug electrical, smart-plug setting, and reset-action Entity IDs, inspect adapter health and Entity availability, and send typed commands. Lights, relays, and sensors register as Device kinds `light`, `relay`, and `sensor`; one IEEE address produces one Device, with temperature, humidity, illuminance, battery, and occupancy Entities supplementing either actuator family (a light or relay that also reports occupancy keeps its actuator kind, so an occupancy-only Device is a sensor), and with electrical sensors, numeric settings, power-on behavior, and the reset action supplementing the relay family on smart plugs. Color temperature uses Zigbee2MQTT's native integer mired unit and each Entity reports its discovered range in `support` (for example, 153–500 mireds). Color-capable lights add native `hearth.colorxy/v1` and/or `hearth.colorhs/v1` Entities plus a read-only `hearth.colormode/v1` Entity reporting `xy`, `hs`, or `color_temp`. XY State uses scaled integers in ten-thousandths (`3125` means `0.3125`); HS State uses whole degrees `0..359` and whole percentage points. Color-temperature State is the object form `{"active":true,"value":370}`: this unreleased type changed from scalar State, so operators with databases holding the old contract discard them explicitly. Each coordinate Entity carries an `active` flag for the mode selected by the same-message `color_mode`. Ambient temperature is read-only `hearth.temperature/v1` with integer milli-Celsius State (for example, `21.5` °C reports `21500`): its support declares the fixed canonical unit as `{"state":{"unit":"mCel"},"operations":{}}`, so the unit comes from Entity support rather than from the type identifier, and the empty operations keep it read-only. Relative humidity, battery level, and ambient illuminance are read-only `hearth.numericsensor/v1` Entities whose support declares a per-Entity unit and bounds and whose operations are empty: humidity and battery report percent State in 0–100 (unit `%`) and illuminance reports lux State (unit `lx`, 0–1000000000 validation envelope), with fractional readings preserved in each. Occupancy is a read-only `hearth.binarysensor/v1` Entity with support `{"state":{},"operations":{}}`: its boolean State decodes only the expose's declared `value_on` and `value_off` scalars (for example Zigbee2MQTT `true`/`false`), and any other value is rejected rather than coerced:

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

A color-temperature set publishes `{"color_temp":370}` to the Device's Zigbee2MQTT `/set` topic, then requests `{"color_temp":""}` from `/get`. The HTTP request succeeds only after a fresh, non-retained report returns exactly 370 mireds with temperature mode active. A color set publishes `{"color":{"x":0.3125,"y":0.3291}}` (HS: `{"color":{"hue":120,"saturation":80}}`), then requests `{"color":""}` from `/get`. Set payloads never include power, brightness, transition, or unrelated properties. Color commands succeed only on active, within-tolerance evidence: ±1 per XY axis, ±2° hue and ±1 saturation point. Satisfaction means `active:true` plus a within-tolerance value under the existing freshness gates, an operational contract rather than a promise of exact physical color. Zigbee2MQTT caching or `color_sync` conversions may carry cached fields into a fresh publication, and Hearth receives no per-field provenance. Ambient-temperature, humidity, illuminance, battery, and occupancy Entities never create command routes. Startup `/get` covers only Entities whose expose grants get access, so a publish-only sensor refreshes from live reports while a gettable sensor also receives active `/get`.

Bulbs exposing the Third Reality `3RCB01057Z` attributes add four more Entities: read-only link quality as generic `hearth.numericsensor/v1` (integer 0–255, unit `lqi`), power-restore behavior as observable `hearth.enumsetting/v1` (`set {"value": ...}`), startup color temperature as observable `hearth.numericsetting/v1` (`set {"mode":"value","value":N}` or `{"mode":"choice","choice":"previous"}`), and effects as stateless `hearth.enumaction/v1` (`trigger {"name": ...}`, for example `"blink"`). Setting commands complete as `satisfied` only after a fresh matching observation, as usual. An effect trigger instead completes as `dispatched`: adapter MQTT acceptance is durably recorded, but no observation is requested, no state is stored (effect Entities always read `state: null`), and dispatch alone makes no claim about any physical effect. MQTT QoS 1 may deliver duplicates and Hearth performs no automatic retry. Command reads therefore distinguish `dispatched` from `satisfied` results. The current inventory fixture covering these exposes is synthetic and Wanda-shaped; startup on-wire values and effect dispatch outcomes are unobserved until separately authorized live validation, so real-hardware behavior is not guaranteed.

Smart plugs exposing the Third Reality `3RSP02028BZ` attributes register relay power plus twelve more Entities. Relay power shares the light power constructor, so `set {"value":true}` publishes the discovered `ON` scalar and succeeds only on fresh matching evidence under the unchanged command-evidence gates. Power-on behavior is the shared observable `hearth.enumsetting/v1` (`set {"value": ...}`). Six read-only `hearth.numericsensor/v1` electrical sensors report AC frequency (`Hz`), electrical power (`W`, key `electricalpower` to distinguish it from boolean relay power), power factor (empty upstream unit mapped explicitly to `ratio`), energy (`kWh`), current (`A`), and voltage (`V`); fractional readings are preserved and get access alone controls startup refresh. Three observable `hearth.numericsetting/v1` settings cover LED brightness (`%`, 0–100) and both turn on/off countdowns (`s`, 0–65535) in numeric value mode (`set {"mode":"value","value":N}`), with decimals preserved and generated `SetSatisfied` matching. Reset Total Energy is a stateless `hearth.enumaction/v1` (`trigger {"name":"Reset"}`) that completes as `dispatched` with no observation, like an effect trigger. Because `hearth.numericsensor/v1` requires finite min/max while Zigbee2MQTT omits electrical bounds, sensors use conservative validation envelopes (not claimed operating ranges) unless the expose carries both valid finite bounds, which are then preferred. All plug attributes attach only under surviving relay power and isolate malformed siblings individually.

For diagnostics, check adapter logs together with the adapter and Entity reads:

- `hearth.external_system_unavailable` indicates that the MQTT broker cannot be reached or the connection was lost; verify the listener, URL, and network boundary.
- `adapter.hearth-adapter-zigbee2mqtt.bridge_offline` indicates an explicit offline `bridge/state`; check Zigbee2MQTT and its coordinator.
- `adapter.hearth-adapter-zigbee2mqtt.incompatible_configuration` indicates an MQTT version other than 4, disabled availability, or optimistic behavior not explicitly false.
- `adapter.hearth-adapter-zigbee2mqtt.invalid_inventory` indicates malformed retained bridge information or Device inventory. Republish valid retained `bridge/info` and `bridge/devices` data by correcting or restarting Zigbee2MQTT.
- Invalid friendly names, groups, disabled or unsupported Devices, incomplete interviews, ambiguous endpoint exposes, and unsupported capabilities are isolated and logged rather than registered. Correct Zigbee2MQTT Device metadata and expose definitions.
- An Entity that stays unknown is missing an explicit availability message. Unavailable reasons distinguish `adapter.hearth-adapter-zigbee2mqtt.device_offline`, `adapter.hearth-adapter-zigbee2mqtt.device_missing`, `adapter.hearth-adapter-zigbee2mqtt.device_disabled`, and `adapter.hearth-adapter-zigbee2mqtt.capability_missing`.
- A Command timeout means no fresh, non-retained matching State arrived after dispatch. Verify the Device can answer Zigbee2MQTT `/get` requests and that the reported property and value match its discovered expose.

### Ecowitt adapter

`hearth-adapter-ecowitt` connects an operator-managed Ecowitt GW2000 gateway to Hearth through the gateway's **Customized Server** MQTT upload. The adapter targets a `GW2000`-prefixed station with a WS90 outdoor array; the captured household firmware it is validated against is a GW2000B running V3.3.2. The station PASSKEY prevents accidental cross-station ingestion on a shared broker.

V1 uses plain MQTT 3.1.1 with no username, password, TLS, or WebSocket transport, and it never publishes to MQTT. The MQTT listener must bind to loopback or a trusted private network, and the gateway, broker, adapter, and native NATS connection must stay inside that boundary; exposing this configuration to an untrusted network is unsupported. The adapter configuration accepts only plain `mqtt://` or `tcp://` endpoints with an explicit host and port, normalizes `mqtt://` to `tcp://`, subscribes at QoS 1 to exactly one two-segment topic, and uses a clean session with a deterministic 23-character client ID so reconnects create no durable adapter state.

Configure the GW2000 Customized Server to match the adapter configuration:

```text
Enabled:         yes
Protocol:        MQTT
Broker host:     trusted-private address of the MQTT listener
Broker port:     1883
Topic:           exact value from mqtt.topic
Upload interval: exact value from station.upload_interval_seconds
```

Store the 32-character hexadecimal PASSKEY in a separate secret file and point `station.passkey_file` at it. The adapter reads the PASSKEY only at startup, compares it in constant time, and never logs, exports, persists, or writes it into Device or Entity identity. The gateway chooses its own MQTT client ID and keepalive; a subscriber cannot observe those portably, so v1 does not gate them, and gateway publish QoS and retain behavior are otherwise not assumed.

The local Compose stack runs Mosquitto for MQTT 1883 on loopback only. Copy the example, keeping its loopback broker URL, fake two-segment topic, and non-secret placeholders:

```sh
cp configs/hearthd.example.yaml configs/hearthd.yaml
cp configs/ecowitt.example.yaml configs/ecowitt.yaml
mkdir -p .secrets
printf '%s\n' '<32-hex-passkey>' > .secrets/ecowitt-passkey   # ignored; never commit a real PASSKEY
mkdir -p .data
printf '%s\n' '<model-api-key>' > .data/agent-api-key   # ignored; the required agent secret
# edit configs/ecowitt.yaml: set station.passkey_file to .secrets/ecowitt-passkey
mise run brokers
go run ./cmd/hearthd -config configs/hearthd.yaml
go run ./cmd/hearth-adapter-ecowitt -config configs/ecowitt.yaml
```

The adapter registers both Device slots before it connects MQTT, so identity exists before any report arrives:

| Device slot (Binding key and external ID) | Kind | Entities |
| --- | --- | --- |
| `gateway` | `sensor` | indoor temperature, indoor humidity, relative pressure, absolute pressure |
| `outdoor-array` | `sensor` | outdoor temperature, outdoor humidity, wind direction, wind speed, wind gust, maximum daily gust, solar radiation, UV index, rain rate, event rain, hourly rain, daily rain, weekly rain, monthly rain, yearly rain |

Entity external IDs are `<device external ID>/<entity key>` (for example `gateway/indoor-temperature`), and each Entity is read-only with empty operations. Every semantic measurement Entity declares its fixed canonical unit in structured State support—temperature `hearth.temperature/v1` in milli-Celsius (`mCel`, for example `22.2` °C reports `22200`), indoor and outdoor humidity `hearth.relativehumidity/v1` in percent (`%`), relative and absolute pressure `hearth.pressure/v1` in hPa, and wind speed, gust, and maximum daily gust `hearth.speed/v1` in m/s—while each State and Observation value stays a bare number. Wind direction, solar radiation, UV index, and the seven rain Entities use `hearth.numericsensor/v1`, whose support declares a per-Entity unit and validation envelope. Fahrenheit, inches of mercury, miles per hour, and inches normalize with fixed constants, and an out-of-envelope value is rejected rather than clamped.

Because Ecowitt reports no stable attached-sensor identifiers, the adapter models configured slots rather than replaceable radio hardware. PASSKEY, MQTT topic, station MAC, firmware, and display name do not participate in Binding or Entity identity, so replacing hardware in a configured slot preserves canonical IDs; reusing one adapter configuration for different physical hardware is an explicit operator statement that it is the same household station, and a separately meaningful station requires a different `adapter_id`.

The adapter remains `unknown` until it has positive or negative external-system evidence. It becomes healthy only after a live, non-retained, non-duplicate, structurally valid, compatible report arrives on the exact topic. Use the HTTP API to inspect adapter health, the registered slot Devices and Entity IDs, and current typed State:

```sh
curl http://127.0.0.1:8080/v1/adapters/ecowitt
curl http://127.0.0.1:8080/v1/devices
curl http://127.0.0.1:8080/v1/entities
curl http://127.0.0.1:8080/v1/entities/ent_...
```

Entity availability comes only from explicit measurement evidence, never from State. A valid field in an accepted report reports its Entity available; an Entity without a valid measurement for three configured upload intervals becomes unavailable, and a never-observed Entity stays unknown until its first valid measurement or three intervals after the first accepted report. Every unhealthy transition clears availability, so recovery requires a fresh live report, a healthy acknowledgement, and fresh availability before Observations.

For diagnostics, check adapter logs together with the adapter and Entity reads:

- `hearth.external_system_unavailable` indicates that the MQTT broker cannot be reached or the connection was lost; verify the listener, URL, and trusted-network boundary.
- `adapter.hearth-adapter-ecowitt.station_silent` indicates an established subscription with no accepted fresh report before three upload intervals; check gateway power, its Customized Server settings, and the configured topic and upload cadence.
- `adapter.hearth-adapter-ecowitt.measurement_stale` indicates one Entity's field has not produced a valid value for three upload intervals; an absent or malformed field is isolated and never fails a valid sibling or station health.
- An Entity that stays unknown is missing its first valid measurement. A retained, wrong-topic, wrong-PASSKEY, wrong-station-type, malformed, oversized, or over-field-limit report produces no evidence; fix the gateway destination or firmware and confirm the PASSKEY matches.
- Runtime diagnostics log fixed codes and payload lengths only and never include the configured topic (whose second segment is commonly a station MAC), raw payload, PASSKEY, secret-file contents, or weather values.

Real evidence compatibility: the checked-in fixture `internal/adapters/ecowitt/testdata/gw2000-ws90-report.txt` preserves the field names and representative value shapes of a captured 712-byte GW2000B V3.3.2 report for a WS90 array, with the PASSKEY and source timestamp sanitized. The capture arrived roughly every eight seconds, unretained, at effective QoS 0 while the subscriber requested QoS 1; the example's 16-second upload interval is an operator cadence choice, and unknown firmware works only when it keeps the specified field semantics.

### Z-Wave JS adapter

`hearth-adapter-zwavejs` connects an operator-managed Z-Wave JS UI service to Hearth through the embedded Z-Wave JS server's WebSocket API. It targets API schema 29, and its wire shapes are derived from Z-Wave JS server 3.10.1 sources (driver 15.x, schema range 0–50); no live Z-Wave JS UI instance or physical device has been exercised yet. A server is compatible when its version frame contains `minSchemaVersion <= 29 <= maxSchemaVersion` and its Home ID agrees with the `start_listening` snapshot. The Adapter never reads or receives S0/S2 keys, never changes network membership, and never sends inclusion, exclusion, SmartStart, interview, healing, route-rebuild, association, configuration, firmware, or controller-backup operations.

Enable the Z-Wave JS server in Z-Wave JS UI and point the adapter at it. The local Compose stack publishes NATS on loopback only; Z-Wave JS UI runs separately under the operator's normal supervision:

```sh
cp configs/hearthd.example.yaml configs/hearthd.yaml
cp configs/zwavejs.example.yaml configs/zwavejs.yaml
mise run brokers
go run ./cmd/hearthd -config configs/hearthd.yaml
go run ./cmd/hearth-adapter-zwavejs -config configs/zwavejs.yaml
```

The configuration accepts only an absolute lowercase `ws://` URL with an explicit host and port, no user info, query, or fragment, and an empty or root path. It has no credential, token, TLS, or certificate field at all: `wss://` is rejected. The embedded Z-Wave JS server offers no authentication and no TLS, so its listener, Hearth NATS, Hearth HTTP, and the Adapter must stay on loopback or a trusted private network. Exposing this configuration to an untrusted network is unsupported. Disabling optimistic value updates in Z-Wave JS UI is recommended but is not a correctness dependency.

Discovery is automatic and needs no allowlist. Every ready, always-listening, completely interviewed non-controller node with a valid grounded Value pair registers one Device:

- Binary Switch (Command Class 37) `currentValue`/`targetValue` registers `hearth.power/v1` with support `{"state":{},"operations":{"set":{}}}` and writes a boolean.
- Multilevel Switch (Command Class 38) registers `hearth.brightness/v1` with support `{"state":{"maximum":99},"operations":{"set":{"step":1}}}` and writes the native 0–99 level, and also provides power when the endpoint has no valid Binary Switch pair. `off` writes `0` and `on` writes `255`, the Command Class restore-previous-level value.
- Binary Switch owns power when both Command Classes are valid on one endpoint.

Device kind is `light` when any brightness Entity exists and `relay` otherwise. One Z-Wave node is one Device; root Entities are named `Power` and `Brightness`, and endpoint Entities prefix a valid endpoint label with keys such as `power-ep1`. Sleeping and frequently-listening actuators are excluded because Z-Wave JS could defer a write into a wake-up queue beyond Hearth's deadline, and a `sleep` Event makes a node temporarily unroutable until `wake up` plus a fresh node state prove it eligible again.

Identity is the network slot, not the hardware: the Binding key is `zwave-<homeId>-node-<nodeId>` and the Device external ID is `<homeId>/node/<nodeId>`. Node names, labels, locations, and manufacturer/product identifiers are mutable metadata and never enter identity. Re-inclusion normally assigns a new node ID and therefore a new Binding and Device, while the previous Device stays unavailable. A controller may reuse a removed node's ID, and Z-Wave exposes no universal immutable physical-device identifier, so **before removing a node, disable its Hearth Entities**; reusing that slot safely requires explicit reconciliation support, which v1 defers. If this Adapter ID's owned mappings carry a different Home ID than the connected controller, the Adapter registers nothing and reports `adapter.hearth-adapter-zwavejs.network_identity_mismatch` instead of silently reusing node-number-shaped identities.

The Adapter remains `unknown` until it has connected, negotiated schema 29, validated one Home ID, received and reconciled a complete snapshot, and installed routes. An empty network with a valid snapshot is healthy. Entity availability is explicit and never inferred from State.

Command evidence is poll-linked. A power or brightness `set` is accepted only after a recognized successful numeric `node.set_value` result and is satisfied only by a fresh correlated `node.poll_value` read; a post-set `value updated` event is at most a wake hint for an extra poll and is never linked evidence, because Z-Wave JS UI may emit that update optimistically. Z-Wave JS exposes no cancellation for a write already handed to the driver, so a timed-out write is reported as an ambiguous attempt rather than as proof that no physical effect can occur later.

Use the HTTP API to inspect adapter health, Entity availability, and canonical IDs, then verify a Command:

```sh
curl http://127.0.0.1:8080/v1/adapters/zwavejs
curl http://127.0.0.1:8080/v1/entities
curl http://127.0.0.1:8080/v1/entities/ent_...
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}'
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":99}}'
```

`99` is the native top of the Multilevel Switch range: brightness uses native 0–99 rather than percent 0–100 because Hearth observes exact equality and 101 canonical values cannot round-trip through 100 native levels.

For diagnostics, check adapter logs together with the adapter and Entity reads:

- `hearth.external_system_unavailable` indicates that the WebSocket endpoint cannot be reached or the connection was lost; verify the Z-Wave JS server listener, `zwave_js.url`, and the trusted-network boundary.
- `adapter.hearth-adapter-zwavejs.incompatible_protocol` indicates a version frame whose schema range excludes 29; update Z-Wave JS UI or Z-Wave JS server, or extend the Adapter deliberately.
- `adapter.hearth-adapter-zwavejs.invalid_snapshot` indicates a malformed version frame or snapshot; check the Z-Wave JS UI driver state and its logs.
- `adapter.hearth-adapter-zwavejs.network_identity_mismatch` indicates that owned mappings and the connected controller describe different Z-Wave networks; restore the intended endpoint or configure a new `adapter_id` for the other network.
- Unavailable reasons distinguish `adapter.hearth-adapter-zwavejs.node_dead`, `adapter.hearth-adapter-zwavejs.node_asleep`, `adapter.hearth-adapter-zwavejs.node_not_ready`, `adapter.hearth-adapter-zwavejs.node_missing`, `adapter.hearth-adapter-zwavejs.node_unknown`, and `adapter.hearth-adapter-zwavejs.capability_missing`. A node that returns to `Unknown` after being assessed reports unavailable rather than available.
- A Command timeout means no fresh matching polled value arrived before the Entity-type deadline. Check that the node is always-listening and alive, that the device reports `currentValue`, and whether the physical device is slow enough to need a longer poll window. A timeout does not cancel radio work; re-read State before assuming the command had no effect.

## Application logs

Hearth executables write to stderr with `--log-level info --log-format text` by default. Use `--log-format json` for filtering, or `--log-level debug` for Observation and Entity Event progress:

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
- [`docs/automation-conditions.md`](./docs/automation-conditions.md): Automation Conditions contract and operator examples
- [`docs/logging.md`](./docs/logging.md): application logging and operator diagnosis
- [`docs/adr/`](./docs/adr/): durable architectural decisions and their rationale
- [`docs/plans/`](./docs/plans/): implementation plans
- [`specs/`](./specs/): approved implementation-ready specifications

The approved first-slice contract is [`specs/first-light.md`](./specs/first-light.md).
