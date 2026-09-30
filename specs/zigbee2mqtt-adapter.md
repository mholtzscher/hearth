# Zigbee2MQTT Adapter implementation spec

**Status:** Draft for review
**Type:** Feature plan
**Effort:** XL, approximately 4 to 8 focused days at 65% confidence
**Date:** 2026-09-01
**Baseline:** branch `z2m` at `fd9d55b`
**Depends on:** `adapter-owned-mapping-inventory.md`
**Live evidence:** Zigbee2MQTT 2.13.0 and 2.14.1; Third Reality 3RCB01057Z, 3RSB22BZ, and 3RSNL02043Z; Mosquitto 2.0.22 MQTT 3.1.1 broker

## Problem

Hearth can observe and control one Home Assistant-managed light through a disposable migration Adapter, but it cannot natively own lights, relays, sensors, and button Event sources paired through Zigbee2MQTT. Removing Home Assistant requires an Adapter that discovers Zigbee lights, relays, sensors, and buttons, preserves canonical Hearth identity, projects power, brightness, color-temperature, native XY and HS color, color-mode, ambient-temperature, ambient-illuminance, binary-occupancy, link-quality, startup-temperature, and power-on-behavior State, reports named button Events, reports health and availability, and translates Commands without adding Zigbee2MQTT concepts to Core.

Zigbee2MQTT, the coordinator, and the MQTT broker remain operator-managed specialist services. The Adapter bridges Zigbee2MQTT's MQTT 3.1.1 contract to the Hearth Adapter SDK rather than reimplementing Zigbee or owning those services.

The first household Device is a Third Reality 3RCB01057Z on Zigbee2MQTT 2.13.0. A Third Reality 3RSB22BZ button on Zigbee2MQTT 2.14.1 supplies the first physical Event-source evidence. A passive capture from a Third Reality 3RSNL02043Z night light on host Wanda on 2026-09-11 (firmware `v1.00.86`) supplies the ambient-illuminance and binary-occupancy evidence; no physical command was exercised there. The deployment uses one file-backed NATS 2.12 server for native Hearth NATS and a Mosquitto 2.0.22 broker for MQTT. Live payloads include finite fractional brightness values, so decoding cannot require integer JSON syntax.

## Decision and scope

Add `hearth-adapter-zigbee2mqtt`, a stateless Go process with a Hearth SDK Session and a Paho MQTT 3.1.1 connection:

```text
Hearth HTTP -> hearthd -> native Core NATS -> Go Adapter SDK Session
    -> hearth-adapter-zigbee2mqtt -> MQTT 3.1.1 -> Mosquitto broker
    -> Zigbee2MQTT -> Zigbee coordinator -> device
```

The Adapter registers every eligible physical light, relay, sensor, and button expose in Zigbee2MQTT's retained `bridge/devices` inventory. One IEEE address maps to one canonical Hearth Device with Device kind `light`, `relay`, or `sensor`. Explicit light, relay, sensor, link-quality, and action planners share one expose index: a non-empty light result is the primary family, otherwise a non-empty relay result wins, and every later plan is supplemental, so temperature, humidity, illuminance, battery, and occupancy Entities supplement either actuator family and form a sensor-only Device only when no light or relay plan exists, because binary capability planning runs inside the sensor family. Unscoped and endpoint-scoped light exposes map to power plus optional brightness, color-temperature, native XY color, native HS color, read-only color-mode, and optional startup-temperature Entities; switch exposes map to power through the same power constructor; numeric Celsius exposes map to read-only temperature Entities; other exact-unit ambient numeric exposes (`humidity` in `%`, `illuminance` in `lx`, `battery` in `%`) map to read-only number sensors and a resolved root binary expose named by a binary capability record (`occupancy` first) maps to a read-only boolean sensor; device-root `linkquality`, `power_on_behavior`, `effect`, and publish-only `action` exposes map to a read-only link-quality sensor, a power-on-behavior setting, a stateless effect action, and a stateless Event source. The color implementation contract, including satisfaction tolerances and activity semantics, is `specs/z2m-bulb-color.md`. The bulb-attribute contract for linkquality, startup temperature, power-on behavior, and dispatched effects is `specs/z2m-bulb-attributes.md`; the button Event mapping and freshness rule and the ambient numeric and binary capability tables are documented in `docs/zigbee2mqtt-capabilities.md`.

Core remains the only owner of Bindings and canonical identity. At startup, the Adapter pages through `Session.ListOwnedMappings`, reconciles persisted ownership against the complete Zigbee2MQTT inventory, and reports missing Devices or capabilities unavailable. It owns no state file or checkpoint.

The private Zigbee2MQTT package owns vendor payloads, topic rules, MQTT lifecycle and retries, discovery, State translation, and Command correlation. Paho provides MQTT. This work does not add a generic MQTT package.

V1 includes:

- automatic discovery of all eligible unscoped and resolvable endpoint-scoped physical lights, relays, and sensors with light-over-relay primary precedence and every supplemental temperature, humidity, illuminance, battery, occupancy, link-quality, and action plan merged into one IEEE registration;
- power, brightness, color-temperature, native XY color, native HS color, read-only color-mode, read-only ambient-temperature, humidity, illuminance, and battery readings, read-only binary occupancy, read-only link-quality, startup-temperature and power-on-behavior settings, stateless effect actions, and stateless button Event sources with stable IEEE and endpoint identity;
- mutable friendly-name routing and display metadata;
- Adapter health, explicit Entity availability, retained or cached State, and startup refresh;
- a private generic runtime coordinator that serializes power, brightness, color-temperature, color, setting, and effect Commands per IEEE Device and publishes fresh post-dispatch evidence for observed Commands while completing dispatched effect Commands on acceptance;
- clean-session MQTT 3.1.1 at QoS 1 through Mosquitto;
- loopback local configuration, operator documentation, and captured, official, synthetic, integration, and real-bulb tests.

V1 defers:

- EMQX compatibility;
- Zigbee2MQTT groups, pairing, permit-join, interview, removal, and rename endpoints;
- transitions and scenes;
- MQTT authentication, TLS, client certificates, and untrusted-network exposure;
- Adapter state files, checkpoints, and Device or Entity retirement;
- full Zigbee2MQTT in CI;
- production supervision, Compose, container topology, host cutover scripts, and reconstruction rollback scripts.

## Operator and configuration contract

### Security and required Zigbee2MQTT settings

V1 MQTT is plain TCP with no username, password, or TLS configuration. The native NATS listener and the Mosquitto MQTT broker must bind to loopback or a trusted private network. Untrusted-network exposure is unsupported.

The Adapter uses Paho for every Zigbee2MQTT message. It never uses `nats.go` to publish or subscribe to translated MQTT subjects; the MQTT broker is a separate service and is not a NATS listener.

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

### Routed names and tested versions

`mqtt.base_topic` must match:

```text
^[a-z0-9][a-z0-9_-]{0,62}$
```

Every v1 `friendly_name` must be one MQTT topic level: 1 to 255 bytes of valid UTF-8 with no `/`, `+`, `#`, or NUL, and never `bridge`. Spaces, uppercase, punctuation, and non-ASCII text are supported, so a Zigbee2MQTT name routes verbatim. MQTT wildcards, topic separators, the reserved bridge route, and invalid UTF-8 are invalid. One invalid Device is diagnosed and isolated. Other Devices remain usable. The first live Device uses:

```yaml
friendly_name: office-table-lamp
description: Office Table Lamp
```

V1 records but does not runtime-gate Zigbee2MQTT 2.13.0 or 2.14.1, Mosquitto 2.0.22, MQTT 3.1.1, or the observed Third Reality 3RCB01057Z and 3RSB22BZ firmware. The Adapter logs Zigbee2MQTT's reported version and ignores unknown fields. Other versions may work when all required fields and behavior remain compatible, but v1 promises no broader range.

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
    Values    []string
    Presets   []upstreamPreset
    Features  []upstreamExpose `json:"features"`
}

type upstreamPreset struct {
    Name  string
    Value int64 // exact-integer decoded; non-integral entries rejected
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
- `friendly_name` is a valid single MQTT topic level;
- at least one eligible Entity plan from the light, relay, sensor, link-quality, or button Event planners exists.

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

`value_step` does not change the canonical Hearth step. V1 support has maximum 100 and step 1. Missing or incompatible brightness does not disqualify power. An eligible light power expose also produces color temperature when it has exactly one unambiguous numeric `color_temp` feature with publish, set, and get access, a Device-unique non-empty property, and an integer mired range inside 100–1000 mireds with minimum below maximum; support reports the discovered range with step 1. Color-temperature State is the object form `{active, value}` and a command is satisfied only on an exact active match.

A color candidate is a direct light composite feature named `color_xy` or `color_hs` with a nonempty property and state, set, and get access, carrying exactly one numeric child per required coordinate (`x`/`y` or `hue`/`saturation`) with matching child properties and the same access. Explicit child bounds must match the upstream domain (XY 0..1, hue 0..360, saturation 0..100); standard boundless children are allowed. One XY and one HS composite may share the `color` property within the same resolved light root; duplicate same-representation claims, cross-root claims, and unrelated claims disqualify the affected candidates. Dual bulbs plan both native Entities with no conversion. Mode is the companion `color_mode` property at root (or `color_mode_<endpoint-label>` when scoped); discovery omits the affected color and mode capabilities when companion ownership is ambiguous. A temperature-only root with no reported mode stays active through the missing-mode fallback; an advertised but unplannable color composite keeps the root mode-sensitive with no silent fallback. A numeric `temperature` expose with unit `°C`, a Device-unique non-empty property, publish access, and no set access produces a read-only `hearth.temperature/v1` Entity with integer milli-Celsius State, structured State support declaring its fixed canonical unit `mCel`, and empty operations; get access alone controls its get properties. Other ambient numeric exposes map through the capability catalog documented in `docs/zigbee2mqtt-capabilities.md`: a resolved root numeric expose named `humidity` (unit `%`), `illuminance` (unit `lx`), or `battery` (unit `%`) with publish access, no set access, and a non-empty Device-unique property supplied by inventory produces a read-only `hearth.numericsensor/v1` Entity whose finite State preserves fractions, with get access alone controlling refresh; humidity and battery use 0–100 percent bounds and illuminance uses a fixed 0–1000000000 lx validation envelope because Zigbee2MQTT omits ambient bounds. A resolved root binary expose whose name matches a binary capability record (`occupancy` first, documented in `docs/zigbee2mqtt-capabilities.md`) and that carries publish access, no set access, a non-empty Device-unique State property, and present distinct `value_on`/`value_off` scalars produces a read-only `hearth.binarysensor/v1` Entity with `{"state":{},"operations":{}}` support, taking its State property and endpoint from inventory and decoding exactly the declared scalars to `true` and `false`; an absent, non-scalar, or identical pair, an empty property, a foreign property claim, set access, or a same-key duplicate root omits only that Entity without affecting valid siblings, while two roots of one capability on distinct resolved endpoints remain distinct `-ep<N>` Entities. Transition, scene, and unrelated configuration features neither create Entities nor disqualify valid siblings. `linkquality`, `color_temp_startup`, `power_on_behavior`, and `effect` plan Entities per §Bulb attributes below; all other color, diagnostic, and configuration features neither create Entities nor disqualify valid siblings.

### Bulb attributes (linkquality, startup temperature, power-on behavior, effect)

The full wire-to-entity contract is `specs/z2m-bulb-attributes.md` §Interfaces; this section binds those four exposes to adapter planning. Exact-match eligibility in this section covers only `linkquality`, `color_temp_startup`, `power_on_behavior`, and `effect`; §Discovery and `docs/zigbee2mqtt-capabilities.md` define the separate `temperature`, `humidity`, `illuminance`, `battery`, and `occupancy` mappings, and §Button action Events defines the `action` Event source. Malformed siblings are omitted without suppressing valid ones.

- `linkquality`: device-root numeric expose with the publish bit required and the set bit forbidden (`access & 1 != 0 && access & 2 == 0`); plans a `hearth.numericsensor/v1` Entity. Decode admits exact integers 0–255 only; fractional or out-of-range payloads are per-property decode issues with siblings intact. Support is `{"state": {"minimum": 0, "maximum": 255, "unit": "lqi"}}`; unit `""` maps to `"lqi"`, `"lqi"` passes through, and any other unit omits the Entity. No command translator; get access alone controls its get properties, so gettable linkquality joins startup refresh while publish-only linkquality does not. A device-kind-agnostic supplement that never gates or joins the power family.
- `color_temp_startup`: nested `light`-feature numeric with `access & 7 == 7`; plans a `hearth.numericsetting/v1` Entity with discovered mired bounds from exact-integer `value_min`/`value_max` within 100–1000 and minimum below maximum. Exactly one `previous`↔65535 preset yields choice `previous`; no `previous` preset yields empty choices; an advertised but invalid or duplicate `previous` mapping omits only that Entity. Planned in `planner_light.go` as an optional sibling of a power-eligible root. Key `startupcolortemp`.
- `power_on_behavior`: device-root enum with `access & 7 == 7` and non-empty unique `values` (at most 64); plans a `hearth.enumsetting/v1` Entity with dynamic choices. Set publishes the value property plus refresh; outcome matching is exact equality. Key `poweronbehavior`.
- `effect`: device-root enum with access exactly 2, a device-unique property, and non-empty unique `values` (at most 64); plans a stateless `hearth.enumaction/v1` Entity with `Values` from the expose. The plan carries empty state/get properties, no decoder, and a dispatched translator publishing `{"effect": "<name>"}` with empty refresh: no `/get`, no matcher, no observation. Key `effect`.

`values`/`presets` metadata comes from tolerant `discovery_wire.go` parsing: non-integral preset values are rejected per entry and malformed siblings never suppress valid Entities.

### Button action Events

A single device-root or endpoint-scoped enum expose named `action`, with access exactly publish-only (`access == 1`), a Device-unique `action` or `action_*` property covered by Zigbee2MQTT's cache exclusion, a resolvable endpoint, and valid unique `values`, plans a stateless `hearth.enumevent/v1` Entity. Its support names pass through unchanged from `values`; the generated facade validates registration support and every reported name. The plan claims no State or get properties and has no Command translator, so it produces no Observation, startup `/get`, or command route. The contribution is supplemental and keeps Device kind `sensor` when it is the only non-link-quality capability.

Each non-retained Device message containing a present supported action produces one Entity Event, including consecutive messages with the same action. Retained messages, absent actions, empty strings, nulls, malformed values, and unsupported names produce no Event and never suppress valid sibling State. This freshness rule depends on the verified Zigbee2MQTT 2.14.1 `CACHE_IGNORE_PROPERTIES` entries for `action` and `action_.*`, which prevent later cache-expanded or startup-cache messages from carrying stale actions. MQTT-retained messages are rejected independently from that cache rule.

### Endpoint resolution

- Unscoped exposes use Entity keys `power`, `brightness`, `colortemp`, `colorxy`, `colorhs`, `colormode`, `temperature`, `humidity`, `illuminance`, `battery`, `occupancy`, `linkquality`, `startupcolortemp`, `poweronbehavior`, `effect`, and `action`.
- Scoped exposes resolve `expose.endpoint` against numeric endpoint keys and `endpoints[*].name`.
- Resolution requires exactly one numeric endpoint.
- Scoped keys are `power-ep<N>`, `brightness-ep<N>`, `colortemp-ep<N>`, `colorxy-ep<N>`, `colorhs-ep<N>`, `colormode-ep<N>`, `temperature-ep<N>`, `humidity-ep<N>`, `illuminance-ep<N>`, `battery-ep<N>`, `occupancy-ep<N>`, `linkquality-ep<N>`, `startupcolortemp-ep<N>`, `poweronbehavior-ep<N>`, `effect-ep<N>`, and `action-ep<N>`.
- Same-key duplicate root exposes, duplicate numeric endpoints for one Entity kind, unresolved names, or duplicate MQTT properties isolate the ambiguous expose; distinct resolved endpoints for one capability stay distinct Entities.
- One malformed expose does not discard independent valid exposes on the Device unless their identity or property routes conflict.

One registration contains every currently eligible Entity for an IEEE Device. More than 64 eligible Entities is an unsupported Device shape and is rejected rather than split. This is the registration protocol bound, not a practical v1 Device limit.

### Canonical identity and metadata

For normalized IEEE `0x00124b0024abcdef`:

| Resource | Root | Endpoint 1 |
|---|---|---|
| Binding key | `z2m-00124b0024abcdef` | same Binding |
| Device external ID | `0x00124b0024abcdef` | same Device |
| Entity key | `power`, `brightness`, `colortemp`, `colorxy`, `colorhs`, `colormode`, `temperature`, `humidity`, `illuminance`, `battery`, `occupancy`, `linkquality`, `startupcolortemp`, `poweronbehavior`, `effect`, `action` | `power-ep1`, `brightness-ep1`, `colortemp-ep1`, `colorxy-ep1`, `colorhs-ep1`, `colormode-ep1`, `temperature-ep1`, `humidity-ep1`, `illuminance-ep1`, `battery-ep1`, `occupancy-ep1`, `linkquality-ep1`, `startupcolortemp-ep1`, `poweronbehavior-ep1`, `effect-ep1`, `action-ep1` |
| Entity external ID | `0x00124b0024abcdef/root/power` | `0x00124b0024abcdef/ep1/power` |

Non-power external IDs replace the final `power` segment with `brightness`, `colortemp`, `colorxy`, `colorhs`, `colormode`, `temperature`, `humidity`, `illuminance`, `battery`, `occupancy`, `linkquality`, `startupcolortemp`, `poweronbehavior`, `effect`, or `action`. Binding keys and external IDs never include `friendly_name`, so a rename changes routing and mutable metadata without changing identity.

The Device name is trimmed `description` when non-empty, otherwise the exact valid `friendly_name`. Root Entity names are `Power`, `Brightness`, `Color Temperature`, `Color XY`, `Color Hue/Saturation`, `Color Mode`, `Temperature`, `Humidity`, `Illuminance`, `Battery`, `Occupancy`, `Link Quality`, `Startup Color Temperature`, `Power-On Behavior`, `Effect`, and `Action`. Scoped names prefix the endpoint label, with `ep<N>` as fallback. A descriptor over Hearth's 128-rune limit is rejected, never truncated.

Registration uses Device kind `light`, `relay`, or `sensor`, generated `powerv1`, `brightnessv1`, `colortempv1`, `colorxyv1`, `colorhsv1`, `colormodev1`, `temperaturev1`, `numericsensorv1`, `binarysensorv1`, `enumsettingv1`, `numericsettingv1`, `enumactionv1`, and `enumeventv1` descriptors, and additive Core reconciliation. Re-registration updates names, external IDs, and normalized support without changing canonical IDs. Reconciliation sends the coordinator an immutable MQTT route snapshot.

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

Each connection is newly created, subscribed, and synchronized from retained bridge topics. MQTT callbacks copy messages into an internal relay queue. The serial connection loop owns parsing, inventory generations, registration, availability evidence, pending MQTT messages, and its immutable runtime-Device snapshot. Before route activation, ordinary State and availability coalesce to the latest message per topic, while each non-retained message carrying a planned Event property remains a separate ordered occurrence. A later ordinary State message cannot erase it; replay publishes each occurrence exactly once after activation. Before inventory arrives, top-level `action` and `action_*` properties are conservatively treated as occurrences. That pending queue is bounded (see below). A separate runtime coordinator owns route activation, Command queues and attempts, matchers, deadlines, and State disposition. Command handlers submit to that coordinator and never read routes directly.

A slow callback must not silently drop MQTT messages, so pre-activation device messages queue in the connection loop. That pending queue is bounded in entries by the fixed `pendingMessageLimit` constant of 1024, chosen because a broker replays retained State and availability for every Device on subscribe and low-rate physical events (button presses, sensor reports) fill occurrences slowly; the bound keeps queue memory proportional to a device-sized MQTT payload and keeps the per-message latest-per-topic coalescing scan linear over a bounded queue. The bound counts entries, not occurrences, so pre-inventory unique ordinary topics cannot grow it without limit. Coalescing an ordinary same-topic message replaces its entry in place and stays allowed at the bound because it does not increase the count.

Admitting an occurrence or a new ordinary topic that would exceed the bound returns a dedicated pending-limit error from ingestion instead of evicting any queued message: the serial connection loop ends that MQTT generation, releases the generation-local pending queue, reports `hearth.external_system_unavailable`, logs one fixed warning carrying only the limit and a stable error code, and reconnects with the existing bounded backoff. No occurrence is ever silently selected for eviction while the connection stays alive. The queue is a connection-lifetime buffer only, not a durable outbox: queued occurrences survive neither a disconnect nor a process restart.

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
11. Publish `/get` for every current Entity with non-empty get properties (power, brightness, color-temperature, color, gettable temperature, humidity, illuminance, and battery sensors, gettable occupancy, gettable linkquality, and gettable settings); publish-only sensors and publish-only linkquality receive no `/get`. Color and mode refresh under the shared `color` attribute, so the read-only mode plan carries no independent get properties.
12. Begin live operation. Messages received during steps 5 through 11 remain queued by generation and topic.

SDK health remains unknown through step 7. A valid synchronized inventory with no eligible Entities becomes healthy and logs that fact.

Use these Adapter health reasons:

```text
hearth.external_system_unavailable
adapter.hearth-adapter-zigbee2mqtt.bridge_offline
adapter.hearth-adapter-zigbee2mqtt.incompatible_configuration
adapter.hearth-adapter-zigbee2mqtt.invalid_inventory
```

MQTT connection failure or loss uses `hearth.external_system_unavailable`. Explicit offline `bridge/state` uses `bridge_offline`. Invalid MQTT version, availability, or optimistic settings use `incompatible_configuration`. Malformed complete bridge info or devices documents use `invalid_inventory`.

An unhealthy transition waits for coordinator route invalidation, which makes routes nondispatchable, cancels MQTT work, rejects unaccepted attempts when possible, and preserves one State disposition for any held report. Registrations remain, and the process reconnects or waits for corrected retained data. Static YAML errors and terminal SDK fencing terminate the process.

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

A State object may contain several endpoint properties. Entity plans remain property-local by default; multi-property plans are appropriate only when the upstream value is genuinely atomic and complete in one MQTT message. Each Entity plan produces one typed Observation only when the same MQTT message contains all of its claimed State properties; partial plans are skipped without caching. Zigbee2MQTT cache-expanded payload completeness does not prove sibling properties were freshly observed together, and the Adapter must not require or depend on `cache_state=true` to assemble Entity State. Unknown properties are ignored. An invalid complete plan is logged once with all claimed properties and skipped without suppressing valid sibling plans or changing Device availability or Adapter health.

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

Color temperature uses native integer mireds within the Entity's discovered range, published as object State `{active, value}` with outcome matching on exact active equality. XY color uses scaled integers in ten-thousandths (`3125` means `0.3125`) with per-axis tolerance 1; HS color uses whole degrees `0..359` (observed `360` canonicalizes to `0`) and whole percentage points with circular hue tolerance 2 and saturation tolerance 1. Each coordinate Entity is active exactly when the same-message `color_mode` selects it; messages missing the mode or value skip that observation without cached assembly, and malformed values are per-Entity decode issues that leave valid siblings intact. The read-only mode Entity publishes reported `xy`, `hs`, and `color_temp` values. Ambient temperature uses integer milli-Celsius State from -273150 through 1000000 (for example, `21.5` becomes `21500`); its support declares the fixed canonical unit `mCel` with an empty `operations` object, and upstream values are parsed exactly while values requiring sub-milli precision, numeric strings, or out-of-range values are rejected rather than clamped, truncated, or rounded. Humidity, illuminance, and battery use `hearth.numericsensor/v1`: JSON numbers become finite State with fractions preserved, publish is required, set is forbidden, and get access alone controls refresh; humidity and battery use percent State in 0–100 while illuminance uses lux State under a fixed 0–1000000000 validation envelope. Occupancy uses `hearth.binarysensor/v1`: the expose's declared `value_on` and `value_off` scalars decode to `true` and `false`, and any other value, including a scalar of the wrong JSON type, is a per-property decode issue that leaves valid siblings intact. Temperature, humidity, illuminance, battery, occupancy, color-mode, and link-quality Entities are read-only: their plans carry a nil command translator, never enter command routes, and multi-property plans require complete same-message evidence with no cross-message State cache. Link-quality, startup-temperature, and power-on-behavior values decode per the bulb-attribute contract (§Discovery): integer-only linkquality and startup payloads, the 65535↔`previous` mapping only when supported, and per-Entity decode issues that leave valid siblings intact. Effect and action Event Entities hold no State and publish no Observations.

The Adapter does not clamp out-of-range values, parse numeric strings, infer power from zero brightness, or infer brightness from power. A brightness Command publishes only its brightness property.

## Entity Event projection

The connection loop parses each Device message once, publishes valid State candidates in registration order, then considers Event plans only when Paho marks the MQTT message non-retained. Each valid action is built through the generated `enumeventv1` facade and sent through the runtime coordinator as a tracked SDK `Session.PublishEntityEvent` effect. The connection loop waits for each publication disposition before processing the next Event, preserving upstream message order. JetStream acknowledgement confirms storage, not Core acceptance. A non-cancellation SDK publication failure is terminal in the same way as an ordinary Observation publication failure.

The Adapter does not infer occurrence identity from value changes. Two separate non-retained MQTT messages with the same supported action are two Entity Events with independently minted IDs. It does not emit an Event for a retained MQTT replay or for an action copied into a future cache-expanded message; the latter guarantee depends on the tested Zigbee2MQTT cache exclusion and must be revalidated before adding a tested version.

## Command runtime and correlation

A private runtime coordinator is the only owner of active routes and route revisions, MQTT generations and the dispatchable connection, per-IEEE FIFO queues, Command attempts, matchers, claimed State, and deadline timers. The connection loop sends it immutable route snapshots, State candidates carrying the connection generation and route revision, and Entity Event candidates. The coordinator changes only its state and completion events; blocking MQTT and JetStream work runs in tracked effect goroutines. The MQTT relay keeps its mutex because Paho callbacks, queue consumption, and closure remain concurrent.

One long-lived generic SDK handler submits each Command to the coordinator and waits only for its buffered result, caller cancellation, or coordinator shutdown. It never reads routes directly. Generated `powerv1`, `brightnessv1`, `colortempv1`, `colorxyv1`, `colorhsv1`, `enumsettingv1`, `numericsettingv1`, and `enumactionv1` facades decode and validate parameters and build the MQTT payload and normalized target; the temperature, color-mode, numeric-sensor, and binary-sensor facades build descriptors and Observations only and their read-only plans never enter command routes. The Adapter does not decode Hearth parameters by hand.

The coordinator queues Commands FIFO by IEEE address. Queue time consumes the existing absolute deadline; an expired queued Command never reaches MQTT. Different IEEE Devices may dispatch concurrently. When a Device becomes idle, the coordinator records dispatch immediately before it launches the QoS 1 `/set` effect with one deterministic JSON object containing every planned set property at `<base>/<friendly_name>/set`; observed Commands additionally install their matcher first. It keeps handling State, route changes, deadlines, and other Device queues while PUBACK is pending.

After PUBACK, the coordinator calls `Accept`, stores the returned `CommandEvidence`, and completes the handler result. For observed Commands it starts the QoS 1 `/get` effect with one deterministic object containing every ordered refresh property as `""` at `<base>/<friendly_name>/get` before completing, without waiting for `/get`, a State match, or JetStream acknowledgement. Every accepted observed Command has at least one refresh property. A dispatched effect attempt publishes no `/get`, installs no matcher, publishes no observation, releases the per-IEEE FIFO slot on accept, and completes with the dispatched result. A failed `/get` is logged and cannot retract acceptance. A non-context `/set` failure rejects the unaccepted Command as unavailable and cancels the connection so the reconnect loop recovers it. Failed acceptance does not consume the responder and leaves the held report, if any, for ordinary publication.

A State candidate can satisfy the active matcher only when it has the same MQTT generation and route revision, is non-retained, belongs to the exact Entity, contains and validly decodes all properties claimed by that plan, normalizes to the target semantic value, arrived after dispatch and no later than the Command deadline, and has not already been claimed or begun linked publication. A match after `/set` launch but before PUBACK is held. The coordinator claims at most one eligible report; every other valid report, including siblings and nonmatching values, is published ordinarily by the connection loop.

A claimed report has exactly one disposition: before linked publication starts, terminal paths such as `/set` failure, failed acceptance, deadline, route replacement, or MQTT disconnect publish it once through ordinary `Session.PublishObservation`; once linked publication starts, it is published only through `CommandEvidence.PublishObservation`. The evidence capability supplies the accepted Command's linkage after the handler returns. A lost linked acknowledgement remains ambiguous, so the SDK retries its original envelope and Observation ID until the evidence deadline and the Adapter never sends an ordinary duplicate. The Device remains active until its linked or fallback publication finishes, or until a terminal path with no claim finishes.

Route replacement and connection loss invalidate the old revision or generation before exposing new routes. They reject only unaccepted attempts when a response remains possible; accepted Commands receive no second response. Attempt, revision, and generation checks discard stale State and effect completions. Coordinator shutdown cancels and joins tracked effects without waiting for a send to an undrained event channel. Terminal Session or fencing errors stop the Adapter runtime.

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
    PublishEntityEvent(context.Context, adapter.EntityEvent) (adapter.EntityEventID, error)
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

`Run` supervises the MQTT reconnect loop and runtime coordinator under one child context, alongside subscription, reconciliation, registration, State, Entity Events, availability, and health. `HandleCommand` submits typed work to the coordinator; the coordinator owns serialization, translation, matcher lifecycle, evidence publication, and refresh correlation.

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

Simulator validation uses a worktree-local NATS/JetStream daemon through
`mise run simulator-start` and does not start MQTT. Manual Zigbee2MQTT operation
uses operator-managed NATS and Mosquitto. The
`configs/zigbee2mqtt.example.yaml` loopback broker URLs are placeholders for
manual setups, not listeners started by this repository.
Real-Mosquitto integration tests use disposable containers and their own
`configs/mosquitto.test.conf`.

Generic docs may show an equivalent trusted-private-network fragment for a separately supervised deployment. They must not include household hosts, Docker network names, destructive cutover, or reconstruction rollback scripts.

README instructions cover operator-managed broker and Zigbee2MQTT prerequisites; required Zigbee2MQTT version, availability, optimistic, slug, and description settings; manually starting Core and the Adapter; discovering Entities; checking health and availability; issuing approved power, brightness, color-temperature, color, setting, and effect Commands; and diagnosing bridge config, invalid topics, missing availability, unsupported exposes, and timeouts.

```text
cmd/
└── hearth-adapter-zigbee2mqtt/
    └── main.go
configs/
├── mosquitto.test.conf
└── zigbee2mqtt.example.yaml
internal/app/
└── zigbee2mqtt/
    ├── config.go
    ├── config_test.go
    ├── run.go
    └── run_integration_test.go
entitytypes/
└── temperaturev1/
    ├── entitytype.json
    ├── examples.json
    ├── state.schema.json
    └── support.schema.json
└── numericsensorv1/
    ├── entitytype.json
    ├── examples.json
    ├── state.schema.json
    └── support.schema.json
└── binarysensorv1/
    ├── entitytype.json
    ├── examples.json
    ├── state.schema.json
    └── support.schema.json
└── enumsettingv1/
    ├── entitytype.json
    ├── examples.json
    ├── state.schema.json
    └── support.schema.json
└── numericsettingv1/
    ├── entitytype.json
    ├── examples.json
    ├── state.schema.json
    └── support.schema.json
└── enumactionv1/
    ├── entitytype.json
    ├── examples.json
    ├── state.schema.json
    └── support.schema.json
sdk/adapter/
└── temperaturev1/
    └── zz_generated_*.go
└── numericsensorv1/
    └── zz_generated_*.go
└── binarysensorv1/
    └── zz_generated_*.go
└── enumsettingv1/
    └── zz_generated_*.go
└── numericsettingv1/
    └── zz_generated_*.go
└── enumactionv1/
    └── zz_generated_*.go
internal/adapters/
└── zigbee2mqtt/
    ├── adapter.go
    ├── availability.go
    ├── command.go
    ├── connection.go
    ├── device_planner.go
    ├── discovery.go
    ├── discovery_wire.go
    ├── entity_plan.go
    ├── entity_power.go
    ├── entity_brightness.go
    ├── entity_colortemp.go
    ├── entity_color.go
    ├── entity_colorxy.go
    ├── entity_colorhs.go
    ├── entity_colormode.go
    ├── entity_temperature.go
    ├── entity_linkquality.go
    ├── entity_startupcolortemp.go
    ├── entity_poweronbehavior.go
    ├── entity_effect.go
    ├── entity_binarysensor.go
    ├── expose_index.go
    ├── mqtt.go
    ├── observation.go
    ├── planner_light.go
    ├── planner_relay.go
    ├── planner_sensor.go
    ├── reconcile.go
    ├── runtime.go
    ├── runtime_routes.go
    ├── runtime_commands.go
    ├── runtime_observations.go
    ├── state.go
    ├── topics.go
    ├── *_test.go
    └── testdata/
        ├── bridge-info-2.13.0.json
        ├── bridge-devices-3rcb01057z.json
        ├── state-3rcb01057z.json
        ├── multi-endpoint-light.json
        ├── bridge-devices-relay-plug.json
        ├── state-relay-plug.json
        ├── bridge-devices-temperature.json
        ├── state-temperature.json
        ├── bridge-devices-3rsnl02043z.json
        └── state-3rsnl02043z.json
README.md
mise.toml
.ko.yaml
internal/app/hearthd/*integration_test.go
specs/zigbee2mqtt-adapter.md
docs/architecture.md
go.mod
go.sum
```

Files in the Adapter package split along distinct protocol and change pressure. The expose index and immutable Entity plans are private in-process abstractions; explicit planners own family selection while generic State, Command, route, and Observation coordination remains independent of Entity type. No public subpackage or runtime plugin mechanism is added.

## Delivery and verification

| Deliverable | Effort | Depends on |
|---|---:|---|
| D1. Config, Paho MQTT transport, local Mosquitto broker config, protocol integration | L | owned-mapping spec |
| D2. Inventory, eligibility, identity, registration, restart reconciliation | L | D1 |
| D3. Health, availability, retained or live State, startup `/get` | L | D2 |
| D4. Runtime coordinator, per-IEEE queues, matcher claiming, and asynchronous command evidence | XL | D3 |
| D5. Docs, fixtures, real-bulb acceptance, mutation testing, validation | L | D4 |

Implementation proceeds from the Core prerequisite through observation, D1 to D3, then control, D4 and D5. Each stage passes focused tests. There is no temporary configured-single-light path.

Tests must state the protected behavior and plausible defect. Oracles come from Hearth domain contracts, authoritative JSON Schemas, official Zigbee2MQTT documentation, captured 2.13.0 payloads, MQTT 3.1.1 and Mosquitto behavior, or independent normalization math.

| Layer | Required behavior and likely defects |
|---|---|
| Config | Plain MQTT URL shape, secret rejection, and slug routes catch unsupported security claims and ambiguous topics. |
| Discovery | Root and endpoint fixtures map deterministically without friendly-name identity, wrong endpoints, color leakage, or access-bit mistakes. XY-only, HS-only, dual, temperature-only, and color-without-temperature Devices discover exactly the intended optional Entities. Bulb-attribute fixtures prove linkquality/startup/power/effect eligibility, `values`/`presets` handling, and sibling isolation, while the captured night-light fixture proves illuminance and occupancy eligibility on a light Device with sibling isolation. Binary fixtures additionally prove that the inventory State property and the declared scalar pair select the mapping without the property matching the expose name, and that distinct endpoint-scoped roots stay distinct while same-key duplicates are omitted. |
| Brightness | Exhaustive or property tests prove every Hearth 0 to 100 Command normalizes back after scaling, including fractions and boundaries. |
| State | Multi-property examples prevent endpoint cross-talk, inferred power, and malformed-value fanout failure. Ambient numeric fixtures preserve fractional humidity, illuminance, and battery values, and occupancy decodes only its declared on/off scalars. |
| Entity Events | Captured button inventory and payloads prove exact support names, stateless registration, no route or `/get`, one Event per fresh non-retained action, repeated-equal occurrences, retained replay rejection, and invalid-action sibling isolation. |
| Reconciliation | Present registrations and absent owned mappings converge without canonical ID loss, deletion, or restart ambiguity. |
| Health and availability | Recoverable bridge failures, isolated Device errors, explicit reports, stale-report clearing, and exact reason codes prevent false health or availability. |
| Commands | Coordinator ownership, freshness, exact-once claiming, property/generation/revision matching, one report disposition, no-op refresh, and cross-IEEE concurrency tests catch duplicate, retained, stale-event, cross-Command, and blocked-effect defects. Effect dispatch publishes no `/get`/matcher/observation and terminates `dispatched`. |
| MQTT integration | Real Paho against Mosquitto proves MQTT 3.1.1, clean session, QoS 1, SUBACK and PUBACK handling, and disconnect recovery. |
| Process integration | An embedded NATS server carries SDK registration, Observation, Entity Event, and Command flows while a real Mosquitto broker carries Zigbee2MQTT MQTT traffic, without schema, subject, assembly, or lifecycle mismatch. |
| Manual | The real bulb proves power, brightness, color temperature, color mode-switch, setting control, effect dispatch, restart, offline recovery, and no-op refresh behavior; the real 3RSB22BZ button proves event discovery and accepted single, double, hold, and release history. |

Native fuzzing covers inventory, exposes, State, and availability with these invariants: no panic, no accepted non-finite brightness, no accepted fractional or out-of-range linkquality, no accepted sub-milli or out-of-range temperature, no invalid slug output, and no duplicate Entity keys. Rapid or exhaustive integer iteration covers brightness round trips.

After adding tests, run Gremlins against `./internal/adapters/zigbee2mqtt` and the smallest affected Core mapping package. Investigate behavioral survivors rather than adding syntax-only assertions. Finish each implementation stage with `mise run validate`.

### Real-bulb acceptance

Against the shared file-backed NATS server and Mosquitto broker, Zigbee2MQTT 2.13.0, and Third Reality 3RCB01057Z:

1. Stop Home Assistant, confirm the operator-managed brokers and Zigbee2MQTT are available, then start local Core and the Adapter with required config.
2. Verify the light remains discoverable, observable, and controllable, with healthy Adapter status and one Device containing power, brightness, color-temperature, link-quality, startup-temperature, power-on-behavior, and effect Entities.
3. Verify `GET /v1/entities` shows display name, support, availability, and current State.
4. Issue power off and on Commands and require linked post-dispatch satisfaction.
5. When safe, issue brightness 0, 25, 50, 75, and 100. Verify integer Hearth State and fractional or integer upstream acceptance, then restore initial power and brightness.
6. Issue no-op power, brightness, and color-temperature Commands and require active `/get` evidence.
7. Mark or observe the Device offline and prove Core still dispatches while the Adapter attempts MQTT.
8. Restart the Adapter and verify clean-session inventory, availability, and State recovery.
9. Remove or hide a capability during downtime in a controlled fixture or process test. Owned mappings must become unavailable rather than unknown.
10. With operator approval, restart the shared brokers and Zigbee2MQTT. Recovery must transition unhealthy to healthy and require fresh availability.
11. Verify normal logs contain no raw household inventory, credentials, or payload dumps.
12. Passive evidence only: the sanitized Third Reality 3RSNL02043Z night-light fixtures (`testdata/bridge-devices-3rsnl02043z.json`, `testdata/state-3rsnl02043z.json`) record the 2026-09-11 capture on host Wanda (firmware `v1.00.86`) for illuminance and occupancy discovery and State; no physical command was exercised against that Device and both Entities are read-only.

The unfinished disposable Paho smoke test from discovery is not evidence. D1 replaces it with a checked-in deterministic real-Mosquitto integration test.

### Acceptance criteria

#### Process and configuration

- [ ] `hearth-adapter-zigbee2mqtt` follows existing config, signal, logging, and exit conventions.
- [ ] MQTT URLs accept only explicit plain `mqtt://` or `tcp://` host and port values without credentials.
- [ ] The base topic requires a subject-safe slug and every friendly name is a valid single MQTT topic level.
- [ ] Client ID derivation is stable, bounded, and collision-tested for representative Adapter IDs.
- [ ] Paho negotiates MQTT 3.1.1, clean session, and QoS 1 against Mosquitto 2.0.22.
- [ ] Zigbee2MQTT traffic crosses MQTT, never native `nats.go` publication.

#### Discovery and identity

- [ ] Complete retained inventory registers every eligible physical root and endpoint light, relay, and sensor expose with one IEEE registration and light-over-relay precedence plus supplemental temperature, humidity, illuminance, battery, occupancy, link-quality, and action plans.
- [ ] One IEEE address creates one Device with power plus optional brightness, color-temperature, temperature, humidity, illuminance, battery, occupancy, link-quality, startup-temperature, power-on-behavior, and effect sibling Entities.
- [ ] IEEE and numeric endpoint identity preserve canonical IDs across restart and friendly-name changes.
- [ ] Description changes update the Device name without changing identity.
- [ ] Disabled, unsupported, incomplete, malformed, and ambiguous Devices or exposes follow the stated isolation rules.
- [ ] Transition, scene, and unrelated color, diagnostic, and configuration features neither create Entities nor disqualify eligible siblings; linkquality, startup-temperature, power-on-behavior, and effect Entities follow the bulb-attribute contract.
- [ ] Groups never register.

#### Health and availability

- [ ] Health waits for MQTT, online bridge State, valid info, owned mappings, and one complete reconciliation.
- [ ] Disabled availability or optimistic behavior not proven false keeps the Adapter unhealthy with the exact reason.
- [ ] A valid zero-Entity inventory is healthy.
- [ ] MQTT and bridge failures recover through bounded backoff without process exit.
- [ ] Explicit online or offline reports map every current Device Entity, and State never implies availability.
- [ ] Missing, disabled, and capability-removed owned mappings receive exact reasons, including after downtime.
- [ ] Recovery reports healthy before fresh availability.

#### State

- [ ] Retained or cached State is accepted after registration, then active `/get` refreshes every Entity with non-empty get properties; publish-only sensors and publish-only linkquality receive no `/get`.
- [ ] Ambient humidity, illuminance, and battery values normalize to finite numeric State with fractions preserved, and occupancy decodes exactly the declared `value_on`/`value_off` scalars to boolean State while every other value stays a per-property decode issue that leaves siblings intact.
- [ ] One multi-property payload projects each valid current Entity independently with one receive timestamp.
- [ ] Power values use expose metadata.
- [ ] Finite integer and fractional brightness values normalize to integer State from 0 through 100.
- [ ] Native integer-mired color-temperature values normalize within their discovered range to object State `{active, value}`.
- [ ] XY, HS, and mode values decode with exact numeric handling; missing mode or value skips without cached assembly and malformed values stay per-Entity decode issues.
- [ ] Celsius JSON numbers normalize to integer milli-Celsius State and sub-milli, string, or out-of-range values are rejected.
- [ ] Link-quality, startup-temperature, and power-on-behavior values decode per the bulb-attribute contract with per-Entity decode issues leaving siblings intact; effect Entities hold no State and publish no Observations.
- [ ] Out-of-range, numeric-string, non-finite conversion, and malformed values do not affect siblings or health.
- [ ] Observations invent no source time.

#### Commands

- [ ] Power, brightness, color-temperature, color, setting, and effect parameters use generated typed SDK facades; temperature, humidity, illuminance, battery, occupancy, color-mode, and link-quality plans carry no command translator and never enter command routes.
- [ ] Same-IEEE Commands remain FIFO until their linked or ordinary claimed-State disposition finishes, while different IEEE Devices may make progress concurrently.
- [ ] Offline availability does not prevent an attempted `/set`.
- [ ] Missing or replaced routes and non-context `/set` failures return `entity_unavailable` when a response remains possible; queued or `/set` deadline expiry sends no late response.
- [ ] `HandleCommand` returns after QoS 1 `/set` PUBACK and successful acceptance, before `/get`, State matching, or linked acknowledgement; every accepted observed Command triggers active `/get`, while dispatched effect Commands publish no `/get`, install no matcher, publish no observation, and release the per-IEEE FIFO slot on acceptance.
- [ ] An early eligible match is held until acceptance, then uses its returned evidence capability for linked publication.
- [ ] One matching upstream property report has exactly one linked or ordinary disposition; nonmatching and sibling properties remain ordinary Observations.
- [ ] Retained replay, pre-dispatch State, stale routes or connection generations, wrong endpoints, wrong normalized brightness, wrong mireds, wrong milli-Celsius values, and wrong-mode exact coordinates cannot satisfy a Command.
- [ ] A held report falls back ordinarily on every pre-link terminal path, but never after linked publication starts.
- [ ] No-op Commands can satisfy through active refresh.
- [ ] Effect Commands terminate `dispatched` with no observation ID or value; rejected effect observations appear as value-less diagnostic rows in State-history `all`/`rejected` filters, never as State.
- [ ] Without a match, Core reaches its existing outcome timeout without replay or synthetic success; an ambiguous linked acknowledgement creates no ordinary duplicate.

#### Delivery

- [ ] README and example YAML document trusted-network and Zigbee2MQTT prerequisites.
- [ ] The repository contains no host-specific destructive cutover or rollback artifacts.
- [ ] Sanitized fixtures preserve payload shape without household IEEE addresses or friendly names.
- [ ] Real-Mosquitto integration, real-bulb acceptance, focused mutation tests, and `mise run validate` pass.
- [ ] Release or container command enumeration includes the executable where applicable.

## Key rationale and risks

- Optimistic Zigbee2MQTT echoes could look like outcome evidence, so registration requires global optimistic behavior false.
- MQTT has no Device Command ID. The coordinator's per-IEEE FIFO queues, connection generations, route revisions, post-dispatch matcher, active `/get`, and one claimed report provide correlation without blocking unrelated Devices.
- Core-owned mapping inventory preserves absent identity after restart without Adapter state.
- `UseNumber`, finite conversion, explicit percent math, and property tests protect observed fractional brightness behavior.
- Numeric endpoint IDs survive converter label drift. Friendly names remain routing metadata.
- Complete-document failures affect health, while malformed Devices and exposes are isolated.
- Clean sessions can miss non-retained State, so startup accepts cached evidence and refreshes every readable property.
- Context-bounded Paho waits and real disconnect tests protect against hangs.
- Strict slugs trade naming flexibility for deterministic MQTT and NATS classification.
- Plain MQTT is acceptable only on the stated trusted network boundary.
- Native NATS and the Mosquitto broker are separate processes, so one can fail without the other; readiness, health, file-backed JetStream, and restart acceptance must still surface either failure and recover.
- Eligibility requires `/get` for observed Commands; when a Device cannot confirm a no-op Command, timeout is the honest result. Dispatched effect Commands carry no refresh property by plan.

## Open questions

None.
