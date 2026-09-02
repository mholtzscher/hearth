# Zigbee2MQTT Adapter implementation spec

**Status:** Draft for review
**Type:** Feature plan
**Effort:** XL, approximately 4 to 8 focused days at 65% confidence
**Date:** 2026-09-01
**Baseline:** branch `z2m` at `fd9d55b`
**Depends on:** `adapter-owned-mapping-inventory.md`
**Live evidence:** Zigbee2MQTT 2.13.0, Third Reality 3RCB01057Z, NATS Server 2.12 MQTT 3.1.1 listener

## Problem

Hearth can observe and control one Home Assistant-managed light through a disposable migration Adapter, but it cannot natively own lights paired through Zigbee2MQTT. Removing Home Assistant requires an Adapter that discovers Zigbee lights, preserves canonical Hearth identity, projects power and brightness State, reports health and availability, and translates Commands without adding Zigbee2MQTT concepts to Core.

Zigbee2MQTT, the coordinator, and the MQTT broker remain operator-managed specialist services. The Adapter bridges Zigbee2MQTT's MQTT 3.1.1 contract to the Hearth Adapter SDK rather than reimplementing Zigbee or owning those services.

The first household Device is a Third Reality 3RCB01057Z on Zigbee2MQTT 2.13.0. The deployment uses one file-backed NATS 2.12 server for native Hearth NATS and its MQTT listener. Live payloads include finite fractional brightness values, so decoding cannot require integer JSON syntax.

## Decision and scope

Add `hearth-adapter-zigbee2mqtt`, a stateless Go process with a Hearth SDK Session and a Paho MQTT 3.1.1 connection:

```text
Hearth HTTP -> hearthd -> native Core NATS -> Go Adapter SDK Session
    -> hearth-adapter-zigbee2mqtt -> MQTT 3.1.1 -> NATS MQTT listener
    -> Zigbee2MQTT -> Zigbee coordinator -> light
```

The Adapter registers every eligible physical light expose in Zigbee2MQTT's retained `bridge/devices` inventory. One IEEE address maps to one canonical Hearth Device. Unscoped and endpoint-scoped light exposes map to power and optional brightness Entities on that Device.

Core remains the only owner of Bindings and canonical identity. At startup, the Adapter pages through `Session.ListOwnedMappings`, reconciles persisted ownership against the complete Zigbee2MQTT inventory, and reports missing Devices or capabilities unavailable. It owns no state file or checkpoint.

The private Zigbee2MQTT package owns vendor payloads, topic rules, MQTT lifecycle and retries, discovery, State translation, and Command correlation. Paho provides MQTT. This work does not add a generic MQTT package.

V1 includes:

- automatic discovery of all eligible unscoped and resolvable endpoint-scoped physical lights;
- power and brightness Entities with stable IEEE and endpoint identity;
- mutable friendly-name routing and display metadata;
- Adapter health, explicit Entity availability, retained or cached State, and startup refresh;
- serialized per-Device power and brightness Commands with fresh post-dispatch correlation;
- clean-session MQTT 3.1.1 at QoS 1 through NATS MQTT;
- loopback local configuration, operator documentation, and captured, official, synthetic, integration, and real-bulb tests.

V1 defers:

- Mosquitto and EMQX compatibility;
- Zigbee2MQTT groups, pairing, permit-join, interview, removal, and rename endpoints;
- color temperature, color, effects, transitions, scenes, and power-on behavior;
- MQTT authentication, TLS, client certificates, and untrusted-network exposure;
- Adapter state files, checkpoints, and Device or Entity retirement;
- full Zigbee2MQTT in CI;
- production supervision, Compose, container topology, host cutover scripts, and reconstruction rollback scripts.

## Operator and configuration contract

### Security and required Zigbee2MQTT settings

V1 MQTT is plain TCP with no username, password, or TLS configuration. Native NATS and MQTT listeners must bind to loopback or a trusted private network. Untrusted-network exposure is unsupported.

The Adapter uses Paho for every Zigbee2MQTT message. It never uses `nats.go` to publish or subscribe to translated MQTT subjects, even when both protocols share one NATS server.

Before registration, the Adapter validates these effective global settings from retained `bridge/info`:

```yaml
mqtt:
  version: 4
availability:
  enabled: true
device_options:
  optimistic: false
```

Version 4 means MQTT 3.1.1. Zigbee2MQTT owns this configuration, and the Adapter never changes it. Missing or malformed `bridge/info`, another protocol version, disabled availability, or failure to prove global optimistic behavior false keeps the Adapter unhealthy. Command routes remain unusable, existing Core registrations remain intact, and the Adapter waits for compatible evidence.

### Route-safe names and tested versions

`mqtt.base_topic` and every v1 `friendly_name` must match:

```text
^[a-z0-9][a-z0-9_-]{0,62}$
```

Whitespace, MQTT wildcards, slashes, dots, and ambiguous NATS topic conversions are invalid. One invalid Device is diagnosed and isolated. Other Devices remain usable. The first live Device uses:

```yaml
friendly_name: office-table-lamp
description: Office Table Lamp
```

V1 records but does not runtime-gate Zigbee2MQTT 2.13.0, NATS Server 2.12, MQTT 3.1.1, or the observed Third Reality 3RCB01057Z firmware. The Adapter logs Zigbee2MQTT's reported version and ignores unknown fields. Other versions may work when all required fields and behavior remain compatible, but v1 promises no broader range.

### Static configuration

Owner: `internal/app/zigbee2mqtt/config.go`.

```go
type Config struct {
    AdapterID string     `yaml:"adapter_id"`
    NATSURL   string     `yaml:"nats_url"`
    MQTT      MQTTConfig `yaml:"mqtt"`
}

type MQTTConfig struct {
    URL       string `yaml:"url"`
    BaseTopic string `yaml:"base_topic"`
}
```

Validation requires:

- `adapter_id` and `mqtt.base_topic` satisfy the Hearth slug rule;
- `nats_url` satisfies existing `nats://` validation;
- `mqtt.url` is absolute, uses only `mqtt://` or `tcp://`, has an explicit host and port, and has no user info, path, query, or fragment;
- no secret or client-ID fields.

The implementation normalizes `mqtt://` to Paho's `tcp://` transport. It derives this deterministic 23-character client ID:

```text
hearth-z2m-<first 12 lowercase hex characters of SHA-256(adapter_id)>
```

The stable ID helps broker diagnosis. `CleanSession=true` prevents it from becoming durable Adapter state.

Example `configs/zigbee2mqtt.example.yaml`:

```yaml
adapter_id: zigbee2mqtt
nats_url: nats://127.0.0.1:4222
mqtt:
  url: tcp://127.0.0.1:1883
  base_topic: zigbee2mqtt
```

## Zigbee2MQTT wire model

All DTOs are private to `internal/adapters/zigbee2mqtt`. Decoding ignores unknown JSON properties, then validates required identity and capability fields.

```go
type bridgeInfo struct {
    Version string `json:"version"`
    Config  struct {
        MQTT struct {
            Version int `json:"version"`
        } `json:"mqtt"`
        Availability struct {
            Enabled bool `json:"enabled"`
        } `json:"availability"`
        DeviceOptions struct {
            Optimistic *bool `json:"optimistic"`
        } `json:"device_options"`
    } `json:"config"`
}

type bridgeState struct {
    State string `json:"state"`
}

type upstreamDevice struct {
    IEEEAddress    string                      `json:"ieee_address"`
    Type           string                      `json:"type"`
    Supported      bool                        `json:"supported"`
    Disabled       bool                        `json:"disabled"`
    FriendlyName   string                      `json:"friendly_name"`
    Description    string                      `json:"description"`
    InterviewState string                      `json:"interview_state"`
    Endpoints      map[string]upstreamEndpoint `json:"endpoints"`
    Definition     *upstreamDefinition         `json:"definition"`
}

type upstreamEndpoint struct {
    Name string `json:"name"`
}

type upstreamDefinition struct {
    Model       string           `json:"model"`
    Vendor      string           `json:"vendor"`
    Description string           `json:"description"`
    Exposes     []upstreamExpose `json:"exposes"`
}

type upstreamExpose struct {
    Type      string           `json:"type"`
    Name      string           `json:"name"`
    Property  string           `json:"property"`
    Endpoint  string           `json:"endpoint"`
    Access    int              `json:"access"`
    ValueOn   json.RawMessage  `json:"value_on"`
    ValueOff  json.RawMessage  `json:"value_off"`
    ValueMin  *float64         `json:"value_min"`
    ValueMax  *float64         `json:"value_max"`
    ValueStep *float64         `json:"value_step"`
    Features  []upstreamExpose `json:"features"`
}

type availabilityPayload struct {
    State string `json:"state"`
}
```

Device State decodes as `map[string]json.RawMessage` because discovery determines properties. Numeric decoding uses `json.Decoder.UseNumber`. Conversion rejects syntax errors, overflow, NaN, and infinity. JSON cannot encode NaN or infinity, but conversion still checks `math.IsNaN` and `math.IsInf` before normalization.

The Adapter decodes `bridge/event` only enough for safe structured diagnostics. Events never create, mutate, or remove identity.

## Discovery, identity, and reconciliation

### Inventory and Device eligibility

Retained `bridge/devices` is the complete external inventory authority. Each successfully decoded top-level update replaces the current inventory generation.

A Device is considered for registration only when:

- `type` is not `Coordinator`;
- `supported` is true and `disabled` is false;
- `interview_state` is `SUCCESSFUL`;
- `definition` is non-nil;
- its normalized IEEE address is exactly `0x` plus 16 lowercase hexadecimal characters;
- `friendly_name` satisfies the route-safe slug rule;
- at least one eligible `light` expose exists.

A light expose may be unscoped or endpoint-scoped. Its nested features determine Entities.

A power Entity requires exactly one unambiguous binary `state` feature with:

- publish, set, and get access bits, expressed as `access & 7 == 7`;
- a non-empty property unique within the Device;
- present, unequal `value_on` and `value_off` values that are valid JSON scalars;
- a resolvable endpoint when scoped.

State comparison uses the discovered values rather than hard-coded `ON` and `OFF`. Other values are diagnosed and skipped.

An eligible power expose also produces brightness when it has exactly one unambiguous numeric `brightness` feature with:

- publish, set, and get access bits;
- a non-empty property unique within the Device;
- finite `value_min` equal to zero;
- finite `value_max` of at least 100;
- enough range to represent every Hearth integer percentage after command scaling and observation rounding.

`value_step` does not change the canonical Hearth step. V1 support has maximum 100 and step 1. Missing or incompatible brightness does not disqualify power. Color, color-temperature, effect, transition, diagnostic, and configuration features neither create Entities nor disqualify valid power or brightness.

### Endpoint resolution

- Unscoped exposes use Entity keys `power` and `brightness`.
- Scoped exposes resolve `expose.endpoint` against numeric endpoint keys and `endpoints[*].name`.
- Resolution requires exactly one numeric endpoint.
- Scoped keys are `power-ep<N>` and `brightness-ep<N>`.
- Duplicate root exposes, duplicate numeric endpoints for one Entity kind, unresolved names, or duplicate MQTT properties isolate the ambiguous expose.
- One malformed expose does not discard independent valid exposes on the Device unless their identity or property routes conflict.

One registration contains every currently eligible Entity for an IEEE Device. More than 64 eligible Entities is an unsupported Device shape and is rejected rather than split. This is the registration protocol bound, not a practical v1 Device limit.

### Canonical identity and metadata

For normalized IEEE `0x00124b0024abcdef`:

| Resource | Root | Endpoint 1 |
|---|---|---|
| Binding key | `z2m-00124b0024abcdef` | same Binding |
| Device external ID | `0x00124b0024abcdef` | same Device |
| Entity key | `power`, `brightness` | `power-ep1`, `brightness-ep1` |
| Entity external ID | `0x00124b0024abcdef/root/power` | `0x00124b0024abcdef/ep1/power` |

Brightness external IDs replace the final `power` segment with `brightness`. Binding keys and external IDs never include `friendly_name`, so a rename changes routing and mutable metadata without changing identity.

The Device name is trimmed `description` when non-empty, otherwise the exact valid `friendly_name`. Root Entity names are `Power` and `Brightness`. Scoped names are `<endpoint label> Power` and `<endpoint label> Brightness`; the expose label is preferred, with `ep<N>` as fallback. A descriptor over Hearth's 128-rune limit is rejected, never truncated.

Registration uses Device kind `light`, generated `powerv1` and `brightnessv1` descriptors, and additive Core reconciliation. Re-registration updates names, external IDs, and normalized support without changing canonical IDs. The Adapter updates its own MQTT route snapshot.

### Owned-mapping reconciliation

At each new Hearth runtime, before reporting healthy, the Adapter:

1. pages through every `Session.ListOwnedMappings` result;
2. indexes rows by Binding and Entity key;
3. decodes and validates the latest complete Zigbee2MQTT inventory;
4. registers every eligible Device and collects canonical IDs;
5. compares owned rows with the current route set;
6. stages unavailable reports for known missing, disabled, or capability-removed Entities;
7. stages retained availability and State evidence for current Entities.

Later complete inventory updates reconcile against both the startup ownership set and every Binding returned by registration. Omission from registration never means deletion. Absent Entities retain canonical identity, enablement, metadata, State, and history.

A permanently rejected registration or malformed Device is logged with safe fields such as normalized IEEE, model, and rejection code, then isolated. Previously mapped Entities for that Device become unavailable. A rejected new Device has no canonical resource to report.

## MQTT runtime

### Topics and connection behavior

With base topic `zigbee2mqtt`, subscribe at QoS 1 to `zigbee2mqtt/#`. Classify only exact topics derived from the configured base and current inventory:

```text
zigbee2mqtt/bridge/info
zigbee2mqtt/bridge/state
zigbee2mqtt/bridge/devices
zigbee2mqtt/bridge/event
zigbee2mqtt/<friendly_name>
zigbee2mqtt/<friendly_name>/availability
```

Ignore `/set`, `/get`, bridge request and response topics, groups, and unknown topics. One-segment slugs keep classification unambiguous.

The MQTT client uses MQTT 3.1.1, `CleanSession=true`, the deterministic client ID, and QoS 1 for subscription and `/set` or `/get` publication. Adapter publications are not retained. Every Paho token wait is context-bounded. Paho automatic reconnect is disabled because the Adapter owns reconnect with bounded exponential backoff and jitter.

Each connection is newly created, subscribed, and synchronized from retained bridge topics. MQTT callbacks copy messages into an internal relay queue. The serialized Adapter run loop owns parsing, inventory generations, registration, and evidence routing. Command handlers use bounded request and result channels to communicate with that loop.

A slow callback must not silently drop MQTT messages. The relay queue is unbounded only for one connection's lifetime and is released on disconnect. NATS and Zigbee2MQTT packet limits bound each message. Explicit backpressure waits for measurement.

### Startup, health, and recovery

Startup order is fixed:

1. Load and validate YAML. Invalid configuration terminates the process.
2. Connect and claim the Hearth SDK Session.
3. List all owned mappings.
4. Connect MQTT and subscribe before interpreting snapshots.
5. Wait for retained `bridge/state`, `bridge/info`, and `bridge/devices`.
6. Require an online bridge and compatible global configuration.
7. Reconcile and register the complete inventory.
8. Call `Session.SetHealth(healthy)` and wait for acknowledgement.
9. Publish staged fresh availability batches.
10. Publish staged retained or cached State.
11. Publish `/get` for every current readable power and brightness property.
12. Begin live operation. Messages received during steps 5 through 11 remain queued by generation and topic.

SDK health remains unknown through step 7. A valid synchronized inventory with no eligible lights becomes healthy and logs that fact.

Use these Adapter health reasons:

```text
hearth.external_system_unavailable
adapter.hearth-adapter-zigbee2mqtt.bridge_offline
adapter.hearth-adapter-zigbee2mqtt.incompatible_configuration
adapter.hearth-adapter-zigbee2mqtt.invalid_inventory
```

MQTT connection failure or loss uses `hearth.external_system_unavailable`. Explicit offline `bridge/state` uses `bridge_offline`. Invalid MQTT version, availability, or optimistic settings use `incompatible_configuration`. Malformed complete bridge info or devices documents use `invalid_inventory`.

An unhealthy transition disables current Command routes and cancels active matchers. Registrations remain, and the process reconnects or waits for corrected retained data. Static YAML errors and terminal SDK fencing terminate the process.

After recovery, the Adapter repeats complete mapping and inventory reconciliation, waits for health acknowledgement, then sends fresh availability. It never relies on reports that Core cleared during the unhealthy transition.

### Entity availability

Only explicit Zigbee2MQTT payloads provide availability:

```json
{"state":"online"}
{"state":"offline"}
```

A live or retained `online` report maps every current Entity for the IEEE Device to available. `offline` maps each to unavailable. State never implies availability.

Use these Entity reasons:

```text
adapter.hearth-adapter-zigbee2mqtt.device_offline
adapter.hearth-adapter-zigbee2mqtt.device_missing
adapter.hearth-adapter-zigbee2mqtt.device_disabled
adapter.hearth-adapter-zigbee2mqtt.capability_missing
```

- A new Entity with no current availability payload remains unknown.
- An omitted Device uses `device_missing` for every owned mapping.
- A known disabled Device uses `device_disabled`; a new disabled Device is not registered.
- A prior Entity key with no current eligible capability uses `capability_missing`.
- Reports may include disabled Hearth Entities because availability and enablement are independent.
- Reports preserve request order, use at most 256 items per SDK call, and follow healthy acknowledgement.

## State projection

The MQTT callback assigns one Adapter-owned UTC receive time to each accepted message. Every Observation from that message shares it.

Retained or Zigbee2MQTT-cached startup State is valid current evidence after registration and has no `source_updated_at`. After registration and every reconnect, the Adapter sends `/get` for all readable current properties because Device State need not be retained.

A State object may contain several endpoint properties. Each recognized valid property produces one typed Observation. Unknown properties are ignored. Invalid power or brightness values are logged and skipped independently without changing Device availability or Adapter health.

Power compares the raw scalar's canonical JSON value with discovered metadata:

- `value_on` maps to Hearth `true`;
- `value_off` maps to Hearth `false`;
- any other value produces no Observation.

Brightness support is:

```json
{"state":{"maximum":100},"operations":{"set":{"step":1}}}
```

For upstream maximum `M`, finite upstream value `x`, and Hearth integer percentage `p`:

```text
valid upstream range: 0 <= x <= M
Hearth State:         floor((x * 100 / M) + 0.5)
Command upstream:     p * M / 100, where 0 <= p <= 100
```

Command JSON may contain a fraction. Observation normalization produces an integer from 0 through 100, and outcome matching uses that integer. For example, `63.75` satisfies a 25% Command when normalization yields 25.

The Adapter does not clamp out-of-range values, parse numeric strings, infer power from zero brightness, or infer brightness from power. A brightness Command publishes only its brightness property.

## Command routing and correlation

One long-lived generic SDK handler dispatches the dynamic Command wildcard by canonical Entity ID through a mutex-protected route snapshot. Generated `powerv1` and `brightnessv1` facades decode and validate parameters. The Adapter does not decode Hearth parameters by hand.

Different IEEE Devices may run Commands concurrently. One context-aware lock serializes all power and brightness Commands for the same IEEE Device. Lock wait consumes the existing absolute Command deadline.

For a valid current route, the handler:

1. acquires the per-Device lock under the SDK Command context;
2. re-reads the route generation and calls `RejectUnavailable` if the route disappeared;
3. installs one matcher for Entity, property, desired normalized value, connection generation, and Command context;
4. marks it dispatched immediately before MQTT `/set` publication;
5. publishes one-property JSON to `<base>/<friendly_name>/set` at QoS 1;
6. waits for PUBACK under the Command context;
7. on unacknowledged publication, removes the matcher, reports external-system health when appropriate, and calls `RejectUnavailable`;
8. after PUBACK, calls `responder.Accept()`;
9. publishes `<base>/<friendly_name>/get` with `{<property>: ""}` at QoS 1;
10. waits for the first eligible matching State report or context end;
11. publishes that report once as a typed Observation with `RefreshForCommand`, using the original handler context;
12. releases the matcher and per-Device lock.

A matching report must:

- belong to the same MQTT connection generation;
- be non-retained and received after dispatch;
- contain the exact current property;
- decode to valid Hearth State;
- normalize to the requested value.

A match after `/set` dispatch but before `/get` is eligible. A match before PUBACK is held until `Accept` succeeds, then published as linked evidence. Nonmatching target values and sibling properties publish as ordinary Observations while the matcher remains active.

The run loop claims one matching property report and creates exactly one linked Observation. It never publishes both ordinary and linked copies of that property report.

If `/get` publication fails after `/set` PUBACK, the accepted Command may still complete from a natural matching report. Otherwise Core reaches its existing outcome deadline. The Adapter sends no second response. Disconnect cancels the matcher, and retained replay after reconnect cannot satisfy it.

Offline availability does not suppress `/set`; availability is advisory and a recovering Device may succeed. A missing Device or capability route, or an unreachable MQTT broker, produces typed `entity_unavailable`, not `upstream_rejected`.

## Module interfaces and assembly

### Adapter module

Owner: `internal/adapters/zigbee2mqtt/adapter.go`.

```go
type Session interface {
    ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
    Register(context.Context, adapter.Registration) (adapter.Binding, error)
    SetHealth(context.Context, adapter.HealthReport) error
    ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
    PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Config struct {
    MQTTURL   string
    BaseTopic string
    ClientID  string
}

func New(session Session, config Config, logger *slog.Logger) (*Adapter, error)
func (z2m *Adapter) Run(context.Context) error
func (z2m *Adapter) HandleCommand(context.Context, adapter.Command, adapter.Responder) error
```

`Run` owns MQTT connection and recovery, subscription, reconciliation, registration, State, availability, and health. `HandleCommand` owns typed routing, serialization, translation, matcher lifecycle, and refresh correlation.

### Private MQTT seam

```go
type mqttDialer interface {
    Dial(context.Context, mqttConfig, func(mqttMessage)) (mqttConnection, error)
}

type mqttConnection interface {
    Subscribe(context.Context, string, byte) error
    Publish(context.Context, string, byte, bool, []byte) error
    Lost() <-chan error
    Close()
}

type mqttMessage struct {
    Topic      string
    Payload    []byte
    Retained   bool
    ReceivedAt time.Time
}
```

The production Paho implementation owns token waiting, callback copying, protocol selection, clean session, and connection-loss signaling. Tests use a fake. No MQTT interface leaves the package.

### Application assembly

Owner: `internal/app/zigbee2mqtt/run.go`.

```go
func Run(context.Context, Config, *slog.Logger) error
```

Assembly validates config, derives the client ID, creates one SDK Session with software name `hearth-adapter-zigbee2mqtt` and version `0.1.0`, then creates the Adapter. It runs `Adapter.Run` and `Session.ServeCommands(Adapter.HandleCommand)` concurrently, cancels the sibling when either returns, and closes MQTT and the SDK Session. Parent cancellation is graceful; terminal SDK and Paho errors remain failures.

`cmd/hearth-adapter-zigbee2mqtt` follows existing flag, logging, signal, and exit-code conventions.

## Local operation and project layout

Add loopback-only, file-backed `configs/nats-server.conf`:

```hcl
listen: 127.0.0.1:4222
jetstream {
  store_dir: ".data/nats"
}
mqtt {
  listen: 127.0.0.1:1883
}
```

`mise run nats` runs:

```sh
nats-server -c configs/nats-server.conf
```

Generic docs may show an equivalent trusted-private-network fragment for a separately supervised deployment. They must not include household hosts, Docker network names, Mosquitto removal, destructive cutover, or reconstruction rollback scripts.

README instructions cover copying the example config; required Zigbee2MQTT version, availability, optimistic, slug, and description settings; starting NATS, `hearthd`, and the Adapter; discovering Entities; checking health and availability; issuing power and brightness Commands; and diagnosing bridge config, invalid topics, missing availability, unsupported exposes, and timeouts.

```text
cmd/
└── hearth-adapter-zigbee2mqtt/
    └── main.go
configs/
├── nats-server.conf
└── zigbee2mqtt.example.yaml
internal/app/
└── zigbee2mqtt/
    ├── config.go
    ├── config_test.go
    ├── run.go
    └── run_integration_test.go
internal/adapters/
└── zigbee2mqtt/
    ├── adapter.go
    ├── availability.go
    ├── availability_test.go
    ├── command.go
    ├── command_concurrency_test.go
    ├── command_test.go
    ├── connection.go
    ├── connection_test.go
    ├── discovery.go
    ├── discovery_exposes.go
    ├── discovery_test.go
    ├── discovery_wire.go
    ├── exposes_test.go
    ├── fixture_helpers_test.go
    ├── mqtt.go
    ├── mqtt_integration_test.go
    ├── observation.go
    ├── reconcile.go
    ├── reconciliation_test.go
    ├── routes.go
    ├── runtime_helpers_test.go
    ├── state.go
    ├── state_test.go
    ├── topics.go
    ├── topics_test.go
    └── testdata/
        ├── bridge-info-2.13.0.json
        ├── bridge-devices-3rcb01057z.json
        ├── state-3rcb01057z.json
        └── multi-endpoint-light.json
README.md
mise.toml
.ko.yaml
internal/app/hearthd/*integration_test.go
specs/zigbee2mqtt-adapter.md
docs/architecture.md
go.mod
go.sum
```

Files in the Adapter package split only along distinct protocol or change pressure. No public subpackage or generic abstraction is added.

## Delivery and verification

| Deliverable | Effort | Depends on |
|---|---:|---|
| D1. Config, Paho MQTT transport, local NATS MQTT config, protocol integration | L | owned-mapping spec |
| D2. Inventory, eligibility, identity, registration, restart reconciliation | L | D1 |
| D3. Health, availability, retained or live State, startup `/get` | L | D2 |
| D4. Dynamic Commands, per-Device serialization, matcher claiming, refresh | XL | D3 |
| D5. Docs, fixtures, real-bulb acceptance, mutation testing, validation | L | D4 |

Implementation proceeds from the Core prerequisite through observation, D1 to D3, then control, D4 and D5. Each stage passes focused tests. There is no temporary configured-single-light path.

Tests must state the protected behavior and plausible defect. Oracles come from Hearth domain contracts, authoritative JSON Schemas, official Zigbee2MQTT documentation, captured 2.13.0 payloads, MQTT 3.1.1 and NATS behavior, or independent normalization math.

| Layer | Required behavior and likely defects |
|---|---|
| Config | Plain MQTT URL shape, secret rejection, and slug routes catch unsupported security claims and ambiguous topics. |
| Discovery | Root and endpoint fixtures map deterministically without friendly-name identity, wrong endpoints, color leakage, or access-bit mistakes. |
| Brightness | Exhaustive or property tests prove every Hearth 0 to 100 Command normalizes back after scaling, including fractions and boundaries. |
| State | Multi-property examples prevent endpoint cross-talk, inferred power, and malformed-value fanout failure. |
| Reconciliation | Present registrations and absent owned mappings converge without canonical ID loss, deletion, or restart ambiguity. |
| Health and availability | Recoverable bridge failures, isolated Device errors, explicit reports, stale-report clearing, and exact reason codes prevent false health or availability. |
| Commands | Freshness, exact-once claiming, property and generation matching, no-op refresh, and concurrency tests catch duplicate, retained, cross-Command, and global-lock defects. |
| MQTT integration | Real Paho against NATS proves MQTT 3.1.1, clean session, QoS 1, SUBACK and PUBACK handling, and disconnect recovery. |
| Process integration | One shared NATS server carries SDK registration, Observation, and Command flows without schema, subject, assembly, or lifecycle mismatch. |
| Manual | The real bulb proves power, brightness, restart, offline recovery, and no-op refresh behavior. |

Native fuzzing covers inventory, exposes, State, and availability with these invariants: no panic, no accepted non-finite brightness, no invalid slug output, and no duplicate Entity keys. Rapid or exhaustive integer iteration covers brightness round trips.

After adding tests, run Gremlins against `./internal/adapters/zigbee2mqtt` and the smallest affected Core mapping package. Investigate behavioral survivors rather than adding syntax-only assertions. Finish each implementation stage with `mise run validate`.

### Real-bulb acceptance

Against the shared file-backed NATS server, Zigbee2MQTT 2.13.0, and Third Reality 3RCB01057Z:

1. Stop Home Assistant, then start NATS, `hearthd`, Zigbee2MQTT, and the Adapter with required config.
2. Verify the light remains discoverable, observable, and controllable, with healthy Adapter status and one Device containing power and brightness Entities.
3. Verify `GET /v1/entities` shows display name, support, availability, and current State.
4. Issue power off and on Commands and require linked post-dispatch satisfaction.
5. When safe, issue brightness 0, 25, 50, 75, and 100. Verify integer Hearth State and fractional or integer upstream acceptance, then restore initial power and brightness.
6. Issue no-op power and brightness Commands and require active `/get` evidence.
7. Mark or observe the Device offline and prove Core still dispatches while the Adapter attempts MQTT.
8. Restart the Adapter and verify clean-session inventory, availability, and State recovery.
9. Remove or hide a capability during downtime in a controlled fixture or process test. Owned mappings must become unavailable rather than unknown.
10. Restart NATS and Zigbee2MQTT. Recovery must transition unhealthy to healthy and require fresh availability.
11. Verify normal logs contain no raw household inventory, credentials, or payload dumps.

The unfinished disposable Paho smoke test from discovery is not evidence. D1 replaces it with a checked-in deterministic NATS MQTT integration test.

### Acceptance criteria

#### Process and configuration

- [ ] `hearth-adapter-zigbee2mqtt` follows existing config, signal, logging, and exit conventions.
- [ ] MQTT URLs accept only explicit plain `mqtt://` or `tcp://` host and port values without credentials.
- [ ] Base and friendly names require subject-safe slugs.
- [ ] Client ID derivation is stable, bounded, and collision-tested for representative Adapter IDs.
- [ ] Paho negotiates MQTT 3.1.1, clean session, and QoS 1 against NATS 2.12 MQTT.
- [ ] Zigbee2MQTT traffic crosses MQTT, never native `nats.go` publication.

#### Discovery and identity

- [ ] Complete retained inventory registers every eligible physical root and endpoint light.
- [ ] One IEEE address creates one Device with power and optional brightness sibling Entities.
- [ ] IEEE and numeric endpoint identity preserve canonical IDs across restart and friendly-name changes.
- [ ] Description changes update the Device name without changing identity.
- [ ] Disabled, unsupported, incomplete, malformed, and ambiguous Devices or exposes follow the stated isolation rules.
- [ ] Color and effect features neither create Entities nor disqualify eligible power or brightness.
- [ ] Groups never register.

#### Health and availability

- [ ] Health waits for MQTT, online bridge State, valid info, owned mappings, and one complete reconciliation.
- [ ] Disabled availability or optimistic behavior not proven false keeps the Adapter unhealthy with the exact reason.
- [ ] A valid zero-light inventory is healthy.
- [ ] MQTT and bridge failures recover through bounded backoff without process exit.
- [ ] Explicit online or offline reports map every current Device Entity, and State never implies availability.
- [ ] Missing, disabled, and capability-removed owned mappings receive exact reasons, including after downtime.
- [ ] Recovery reports healthy before fresh availability.

#### State

- [ ] Retained or cached State is accepted after registration, then active `/get` refreshes every readable property.
- [ ] One multi-property payload projects each valid current Entity independently with one receive timestamp.
- [ ] Power values use expose metadata.
- [ ] Finite integer and fractional brightness values normalize to integer State from 0 through 100.
- [ ] Out-of-range, numeric-string, non-finite conversion, and malformed values do not affect siblings or health.
- [ ] Observations invent no source time.

#### Commands

- [ ] Power and brightness parameters use generated typed SDK facades.
- [ ] Same-IEEE Commands serialize, while different IEEE Commands may overlap.
- [ ] Offline availability does not prevent an attempted `/set`.
- [ ] Missing routes and unacknowledged `/set` return `entity_unavailable`.
- [ ] Accepted Commands publish QoS 1 `/set`, then active `/get`, and require fresh non-retained matching State.
- [ ] Early post-dispatch matches wait for SDK acceptance.
- [ ] One matching upstream property report creates exactly one linked Observation.
- [ ] Nonmatching and sibling properties remain ordinary Observations.
- [ ] Retained replay, prior connection generations, wrong endpoints, and wrong normalized brightness cannot satisfy a Command.
- [ ] No-op Commands can satisfy through active refresh.
- [ ] Without a match, Core reaches its existing outcome timeout without replay or synthetic success.

#### Delivery

- [ ] `mise run nats` uses a loopback file-backed NATS config with native and MQTT listeners.
- [ ] README and example YAML document trusted-network and Zigbee2MQTT prerequisites.
- [ ] The repository contains no host-specific destructive cutover or rollback artifacts.
- [ ] Sanitized fixtures preserve payload shape without household IEEE addresses or friendly names.
- [ ] NATS MQTT integration, real-bulb acceptance, focused mutation tests, and `mise run validate` pass.
- [ ] Release or container command enumeration includes the executable where applicable.

## Key rationale and risks

- Optimistic Zigbee2MQTT echoes could look like outcome evidence, so registration requires global optimistic behavior false.
- MQTT has no Device Command ID. Per-IEEE serialization, connection generations, a post-dispatch matcher, active `/get`, and one claimed report provide correlation without blocking unrelated Devices.
- Core-owned mapping inventory preserves absent identity after restart without Adapter state.
- `UseNumber`, finite conversion, explicit percent math, and property tests protect observed fractional brightness behavior.
- Numeric endpoint IDs survive converter label drift. Friendly names remain routing metadata.
- Complete-document failures affect health, while malformed Devices and exposes are isolated.
- Clean sessions can miss non-retained State, so startup accepts cached evidence and refreshes every readable property.
- Context-bounded Paho waits and real disconnect tests protect against hangs.
- Strict slugs trade naming flexibility for deterministic MQTT and NATS classification.
- Plain MQTT is acceptable only on the stated trusted network boundary.
- The shared NATS process couples failure of native NATS and MQTT, so readiness, health, file-backed JetStream, and restart acceptance must make that failure visible and recoverable.
- Eligibility requires `/get`; when a Device cannot confirm a no-op Command, timeout is the honest result.

## Open questions

None.
