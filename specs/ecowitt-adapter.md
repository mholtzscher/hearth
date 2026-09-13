# Ecowitt MQTT Adapter implementation spec

**Status:** Draft for review
**Type:** Feature plan
**Effort:** XL, approximately 5 to 8 focused days at 60% confidence
**Date:** 2026-09-12
**Baseline:** branch `ecowitt` at `b4d9342`
**Depends on:** `adapter-owned-mapping-inventory.md`, `multi-entity-registration.md`, `adapter-health-and-entity-availability.md`, and the Mosquitto broker migration
**Target evidence:** Ecowitt GW2000 with a WS90 outdoor array; Ecowitt Customized Server MQTT; operator-managed Mosquitto broker

## Problem

Hearth cannot ingest observations from the household Ecowitt GW2000 gateway, whose WS90 outdoor array can publish a Customized Server report through MQTT. Hearth needs a first-party Adapter that translates that vendor report into canonical Devices, Entities, availability, and typed Observations without adding Ecowitt field names, unit conventions, or MQTT behavior to Core.

The Ecowitt MQTT payload is not JSON. Native MQTT publishes the Customized Server Ecowitt form body, a flat URL-encoded field set:

```text
PASSKEY=...&stationtype=GW2000A_V3.2.5&dateutc=2026-09-12+14%3A30%3A00&
tempinf=72.50&humidityin=45&baromrelin=29.921&baromabsin=29.104&
tempf=64.40&humidity=70&winddir=225&windspeedmph=3.58&windgustmph=6.71&
solarradiation=420.50&uv=4&rrain_piezo=0.000&drain_piezo=0.118
```

Ecowitt reports the station `PASSKEY` but no stable hardware IDs for attached sensors; channel and field names are the only sensor addresses. V1 therefore models configured slots rather than replaceable radio hardware identity.

## Decision and scope

Add `hearth-adapter-ecowitt`, a stateless, read-only Go process with one Hearth SDK Session and one private Paho MQTT 3.1.1 client:

```text
GW2000 + WS90
    -> Ecowitt Customized Server MQTT
    -> operator-managed Mosquitto on a trusted private network
    -> Paho MQTT 3.1.1 subscriber
    -> hearth-adapter-ecowitt
    -> typed Hearth Adapter SDK Observations
    -> native Core NATS
    -> hearthd
```

One Adapter instance serves one configured GW2000 station on one exact MQTT topic, and it registers these two slot Devices of kind `sensor` before any report arrives:

1. `gateway`, with indoor temperature, indoor humidity, relative pressure, and absolute pressure;
2. `outdoor-array`, with WS90 outdoor temperature, outdoor humidity, wind, solar, UV, and piezo-rain Entities.

V1 also covers:

- one exact-topic Paho MQTT 3.1.1 subscription on a trusted private network, with no authentication or transport security;
- `PASSKEY` and `GW2000` station-type validation, bounded URL-encoded parsing that isolates measurement errors, canonical unit conversion, and connection-local duplicate suppression;
- Adapter health from MQTT usability and report freshness, explicit availability from measurement freshness, and repeated Observations for every valid present measurement;
- sanitized fixtures, process integration tests, and real-station acceptance.

V1 defers:

- HTTP ingestion or pull APIs, cloud APIs, and deployment or cutover tooling;
- multiple stations per instance, wildcard topics, MQTT authentication, TLS, WebSocket transport, durable sessions, and untrusted networks;
- gateways other than GW2000, outdoor arrays other than WS90, and station fields outside this catalog (WH40 rain, extra temperature and humidity channels, soil moisture, air quality, lightning, leak, battery, signal, runtime, and heap diagnostics);
- dynamic discovery, user-defined field mappings, Device or Entity retirement, Commands, calibration, station configuration, and first-class Entity types beyond relative humidity, pressure, and speed.

## Evidence and compatibility boundary

Ecowitt's HTTP API document describes the Customized Server MQTT settings (host, port, topic, client ID, username, password, keepalive, interval, and transport) and a default-style topic `ecowitt/<station MAC>`. The WS View Plus and Web UI manual documents the Customized Server with an 8-second upload example. Community implementations and captured GW2000 payloads establish that the native MQTT payload is the same URL-encoded report used by HTTP, including the WS90 piezo-rain fields `rrain_piezo`, `erain_piezo`, `hrain_piezo`, `drain_piezo`, `wrain_piezo`, `mrain_piezo`, and `yrain_piezo`.

Authoritative and implementation references:

- Ecowitt HTTP API Interface Protocol: <https://oss.ecowitt.net/uploads/20260114/HTTP%20API%20interface%20Protocol%20(Generic)-(V1.0.6-2026-1-14)%20.pdf>
- Ecowitt WS View Plus and Web UI manual: <https://oss.ecowitt.net/uploads/20250408/WS%20View%20Plus%20%26%20Web%20UI%20Manual%20%28Generic%29.pdf>
- `aioecowitt` field mappings: <https://github.com/home-assistant-libs/aioecowitt>
- `ecowitt2mqtt` fixtures and conversion precedent: <https://github.com/bachya/ecowitt2mqtt>

A sanitized payload captured from the household GW2000 is required before the capability contract is verified: unknown firmware may work when it keeps the specified field semantics, but v1 promises only the captured firmware and payload shape.

### Captured station evidence

On 2026-09-13, a household GW2000B running firmware V3.3.2 published three native Customized Server MQTT reports through the operator-managed Mosquitto 2.0.22 broker to a Paho MQTT 3.1.1 subscriber. A temporary subject-safe topic of the form `ecowitt/<capture>` was used; the gateway's previous Customized Server configuration was restored immediately afterward. The reports arrived every 7.985 and 8.000 seconds, were not retained, and had effective QoS 0 when the subscriber requested QoS 1. The captured 712-byte form contained 44 unique keys, including every v1 catalog field except no catalog fields were absent, plus ignored gateway, soil, battery, and diagnostic fields. Its station identity was `stationtype=GW2000B_V3.3.2` and `model=GW2000B`, confirming that the specified `GW2000` prefix compatibility rule covers the observed hardware. `internal/adapters/ecowitt/testdata/gw2000-ws90-report.txt` preserves the field names and representative value shapes while replacing the PASSKEY and source timestamp. Only translated Hearth messages use native NATS.

## Operator and configuration contract

### Security and gateway settings

V1 uses plain MQTT with no username, password, TLS, client certificate, or WebSocket options. The MQTT listener must bind only to loopback or a trusted private network; the gateway, broker, Adapter, and native Hearth NATS connection stay inside that boundary. The PASSKEY check prevents accidental cross-station ingestion on a shared broker.

Configure the GW2000 Customized Server as follows:

```text
Enabled:         yes
Protocol:        MQTT
Broker host:     trusted-private address of the MQTT listener
Broker port:     1883
Topic:           exact value from mqtt.topic
Upload interval: exact value from station.upload_interval_seconds
```

The gateway chooses its own MQTT client ID and keepalive; a subscriber cannot observe those portably, so v1 does not gate them. Gateway publish QoS and retain behavior are otherwise not assumed.

The Adapter reads Ecowitt traffic only from Mosquitto through Paho. Its separate native NATS connection is owned by the Hearth SDK and carries only translated Hearth protocol messages.

### Static configuration

Owner: `internal/app/ecowitt/config.go`.

```go
type Config struct {
    AdapterID string        `yaml:"adapter_id"`
    NATSURL   string        `yaml:"nats_url"`
    MQTT      MQTTConfig    `yaml:"mqtt"`
    Station   StationConfig `yaml:"station"`
}

type MQTTConfig struct {
    URL   string `yaml:"url"`
    Topic string `yaml:"topic"`
}

type StationConfig struct {
    GatewayName           string `yaml:"gateway_name"`
    OutdoorArrayName      string `yaml:"outdoor_array_name"`
    PasskeyFile           string `yaml:"passkey_file"`
    UploadIntervalSeconds int    `yaml:"upload_interval_seconds"`
}
```

Validation requires:

- `adapter_id` satisfies the Hearth slug rule;
- `nats_url` satisfies existing `nats://` validation;
- `mqtt.url` is absolute, uses only `mqtt://` or `tcp://`, has an explicit host and port, and has no user info, path, query, fragment, or TLS scheme;
- `mqtt.topic` has exactly two subject-safe slash-separated segments matching `^[a-z0-9][a-z0-9_-]{0,62}/[a-z0-9][a-z0-9_-]{0,62}$` and contains no MQTT wildcard;
- both Device names contain 1 to 128 runes after trimming;
- `passkey_file` is non-empty and references a regular file containing exactly one trimmed 32-character hexadecimal PASSKEY;
- `upload_interval_seconds` is from 8 through 600 inclusive.

The implementation normalizes `mqtt://` to Paho's `tcp://`, compares PASSKEY values in constant time after decoding hexadecimal, and never logs, exports, hashes into metadata, or persists the PASSKEY.

The Adapter derives this 23-character MQTT client ID:

```text
hearth-eco-<first 12 lowercase hex characters of SHA-256(adapter_id)>
```

It uses `CleanSession=true`, so the client ID aids broker diagnosis without creating durable Adapter state.

Example `configs/ecowitt.example.yaml`:

```yaml
adapter_id: ecowitt
nats_url: nats://127.0.0.1:4222
mqtt:
  url: tcp://127.0.0.1:1883
  topic: ecowitt/943cc64457a7
station:
  gateway_name: Weather Station Gateway
  outdoor_array_name: Outdoor Weather Array
  passkey_file: /run/secrets/ecowitt-passkey
  upload_interval_seconds: 16
```

The example contains no real PASSKEY, station MAC, household hostname, or routable deployment address.

## Ecowitt wire model

### MQTT message seam

```go
type mqttMessage struct {
    Topic      string
    Payload    []byte
    Retained   bool
    ReceivedAt time.Time
}
```

The Paho callback assigns one UTC `ReceivedAt` and copies the topic and payload bytes before enqueueing the message, so payload ownership never remains with Paho.

### Parsed report

All wire types remain private to `internal/adapters/ecowitt`.

```go
type stationReport struct {
    Passkey     [16]byte
    StationType string
    Model       string
    DateUTC     string
    SourceTime  *time.Time
    Fields      map[string]string
}
```

Parsing rules:

- accept only the configured exact topic, and ignore retained messages before parsing;
- reject a payload larger than 64 KiB;
- parse as `application/x-www-form-urlencoded`, including `+` as space and percent decoding;
- reject malformed escaping, an empty key, more than 256 keys, or any duplicate key;
- require exactly one non-empty `PASSKEY`, `stationtype`, and `dateutc`, with the PASSKEY matching the configured secret and `stationtype` starting with `GW2000`;
- parse `dateutc` as UTC with layout `2006-01-02 15:04:05`, and keep it as `SourceTime` only when it is on or after 2000-01-01 UTC and no more than five minutes after `ReceivedAt`; otherwise accept the report with `SourceTime=nil`;
- permit an absent `model`, ignore unknown fields, and accept a structurally valid compatible report even when one or every measurement is absent or malformed.

Expected URL-encoded numeric values are decimal strings. Numeric parsing rejects empty values, whitespace, signs without digits, exponent notation, trailing data, values that cannot become a finite number, and values outside the Entity support envelope.

### Duplicate reports

The runtime retains only the most recent accepted non-retained report signature:

```go
type reportSignature struct {
    DateUTC    string
    PayloadSHA [32]byte
}
```

An immediately repeated report with both the same decoded `dateutc` string and the same SHA-256 of the original payload publishes nothing and refreshes neither report nor measurement freshness, so MQTT duplicate delivery is not mistaken for new station evidence. This is connection-local memory: no deduplication survives restart.

## Device, Entity, and identity contract

### Static registrations

Registration is idempotent and additive, and every runtime registers both Device slots before connecting MQTT. No report is required to establish identity.

| Device slot | Binding key | Device external ID | Device kind | Configured name |
|---|---|---|---|---|
| GW2000 gateway | `gateway` | `gateway` | `sensor` | `station.gateway_name` |
| WS90 outdoor array | `outdoor-array` | `outdoor-array` | `sensor` | `station.outdoor_array_name` |

Entity external IDs are `<device external ID>/<entity key>`. PASSKEY, MQTT topic, station MAC, firmware, and display name do not participate in Binding or Entity identity. Replacing hardware in a configured slot therefore preserves canonical IDs, as does reusing one Adapter configuration for a different physical station; the latter is an explicit operator statement that the replacement is the same household station. A separately meaningful station requires a different `adapter_id`.

### Capability catalog

The catalog is closed and static in v1. Registration order is the table order within `gateway`, followed by the table order within `outdoor-array`.

| Device | Entity key | Name | Ecowitt field | Hearth type | Canonical State unit and support |
|---|---|---|---|---|---|
| gateway | `indoor-temperature` | Indoor Temperature | `tempinf` | `hearth.temperature/v1` | integer milli-Celsius |
| gateway | `indoor-humidity` | Indoor Humidity | `humidityin` | `hearth.relativehumidity/v1` | decimal percent, 0..100 |
| gateway | `relative-pressure` | Relative Pressure | `baromrelin` | `hearth.pressure/v1` | decimal hPa, 0..2000 |
| gateway | `absolute-pressure` | Absolute Pressure | `baromabsin` | `hearth.pressure/v1` | decimal hPa, 0..2000 |
| outdoor-array | `outdoor-temperature` | Outdoor Temperature | `tempf` | `hearth.temperature/v1` | integer milli-Celsius |
| outdoor-array | `outdoor-humidity` | Outdoor Humidity | `humidity` | `hearth.relativehumidity/v1` | decimal percent, 0..100 |
| outdoor-array | `wind-direction` | Wind Direction | `winddir` | `hearth.numericsensor/v1` | `deg`, 0..360 |
| outdoor-array | `wind-speed` | Wind Speed | `windspeedmph` | `hearth.speed/v1` | decimal m/s, 0..200 |
| outdoor-array | `wind-gust` | Wind Gust | `windgustmph` | `hearth.speed/v1` | decimal m/s, 0..200 |
| outdoor-array | `maximum-daily-gust` | Maximum Daily Gust | `maxdailygust` | `hearth.speed/v1` | decimal m/s, 0..200 |
| outdoor-array | `solar-radiation` | Solar Radiation | `solarradiation` | `hearth.numericsensor/v1` | `W/m²`, 0..10000 |
| outdoor-array | `uv-index` | UV Index | `uv` | `hearth.numericsensor/v1` | `index`, 0..100 |
| outdoor-array | `rain-rate` | Rain Rate | `rrain_piezo` | `hearth.numericsensor/v1` | `mm/h`, 0..10000 |
| outdoor-array | `event-rain` | Event Rain | `erain_piezo` | `hearth.numericsensor/v1` | `mm`, 0..10000000 |
| outdoor-array | `hourly-rain` | Hourly Rain | `hrain_piezo` | `hearth.numericsensor/v1` | `mm`, 0..10000000 |
| outdoor-array | `daily-rain` | Daily Rain | `drain_piezo` | `hearth.numericsensor/v1` | `mm`, 0..10000000 |
| outdoor-array | `weekly-rain` | Weekly Rain | `wrain_piezo` | `hearth.numericsensor/v1` | `mm`, 0..10000000 |
| outdoor-array | `monthly-rain` | Monthly Rain | `mrain_piezo` | `hearth.numericsensor/v1` | `mm`, 0..10000000 |
| outdoor-array | `yearly-rain` | Yearly Rain | `yrain_piezo` | `hearth.numericsensor/v1` | `mm`, 0..10000000 |

`hearth.relativehumidity/v1`, `hearth.pressure/v1`, and `hearth.speed/v1` are reusable read-only physical-quantity types introduced with this Adapter. Their State is a finite JSON number in a fixed canonical unit—percent, hPa, and m/s respectively—and their support is the closed empty-operation shape `{"state":{},"operations":{}}`. Their State schemas fix the catalog envelopes at 0..100, 0..2000, and 0..200. Numeric sensor support carries an empty `operations` object and declares its unit and bounds; temperature uses the existing empty-operation `temperature/v1` contract. No Entity accepts Commands. The broad finite bounds are validation envelopes, not expected operating ranges, so v1 rejects out-of-envelope values rather than clamping them.

### Unit normalization

Parse source decimals exactly enough to apply the following constants before conversion to finite Go numbers and typed facade input:

```text
Fahrenheit to milli-Celsius: round(((F - 32) * 5 / 9) * 1000)
1 inHg:                       33.8638866667 hPa
1 mph:                        0.44704 m/s
1 inch:                       25.4 mm
1 inch/hour:                  25.4 mm/hour
```

Temperature rounding uses nearest integer milli-Celsius with half values away from zero. Other normalized numeric values preserve finite fractional results through their generated typed facade. Humidity uses `relativehumidity/v1`, pressure uses `pressure/v1`, and wind speed and gust use `speed/v1`; wind direction, solar radiation, UV, and rain remain `numericsensor/v1`.

Each Entity is independent: one absent, malformed, non-finite, or out-of-envelope field produces no Observation and leaves valid siblings, Adapter health, and report acceptance unchanged.

## MQTT runtime

### Connection behavior

The Adapter connects with Paho MQTT 3.1.1, `CleanSession=true`, automatic reconnect disabled, and the deterministic client ID, then subscribes at QoS 1 to exactly `mqtt.topic`. Every token wait is context-bounded.

Paho callbacks copy messages into a bounded 64-entry relay, and the serial connection loop owns parsing, duplicate suppression, freshness, availability, and Observation order. When a callback would exceed the relay bound, it signals overflow, ends that MQTT generation, and reconnects; it never drops or evicts a report while keeping the generation healthy.

Connection attempts use bounded exponential backoff with jitter, following the Zigbee2MQTT Adapter precedent. A disconnect invalidates the generation and reports unhealthy. Static registrations remain.

### Startup and recovery

Startup order is fixed:

1. Load and validate YAML and the PASSKEY file. Invalid configuration terminates the process.
2. Connect and claim the Hearth SDK Session.
3. Register the `gateway` and `outdoor-array` Devices and collect canonical Entity IDs.
4. Connect MQTT and subscribe to the exact topic.
5. Wait for one non-retained, non-duplicate, structurally valid, compatible report, then call `Session.SetHealth(healthy)` and wait for acknowledgement.
6. Report each valid present measurement available, publish its typed Observation in catalog order, and continue live operation.

SDK health stays unknown until the Adapter has positive or negative external-system evidence. A report with no valid measurements still proves the configured station transport is usable and permits healthy status, but produces no availability or Observations.

Every unhealthy transition clears current Entity availability in Core, so recovery requires a new live valid report, a health acknowledgement, and fresh availability before Observations. Static configuration errors and terminal SDK fencing terminate the process; MQTT and report-freshness failures remain recoverable.

### Adapter health

The report timeout is exactly three times `station.upload_interval_seconds`. Its monotonic deadline starts at successful SUBACK and resets on each accepted non-duplicate live report, so it exists before the first report as well as during steady state.

Use these Adapter health reasons:

```text
hearth.external_system_unavailable
adapter.hearth-adapter-ecowitt.station_silent
```

- MQTT connection failure, disconnect, subscription failure, and relay overflow use `hearth.external_system_unavailable`.
- An established subscription with no accepted fresh report before the report timeout uses `station_silent`.
- Retained, duplicate, wrong-PASSKEY, wrong-station-type, malformed, and wrong-topic messages do not refresh the timeout, and individual malformed or absent measurements do not affect Adapter health.

Wrong-PASSKEY and malformed reports are logged with fixed diagnostic codes and payload length only. Runtime diagnostics never include the configured topic, whose second segment is commonly a station MAC, and never include raw payload, PASSKEY, field values, or secret-file contents.

### Entity availability

Each Entity has a last-valid-measurement time, and a valid field in an accepted report is explicit evidence that its Entity is available.

While Adapter health remains healthy, an Entity whose field has not produced a valid value for exactly three upload intervals becomes unavailable with:

```text
adapter.hearth-adapter-ecowitt.measurement_stale
```

An absent or malformed field starts or continues that stale interval without failing early, since a field may be omitted transiently. A never-observed Entity stays unknown until its first valid measurement, or until three upload intervals after the first accepted station report, when it becomes unavailable with `measurement_stale`. Availability timers use local monotonic receipt time, never `dateutc`.

Availability reports preserve catalog order and carry at most 256 entries per SDK call, and repeated available reports with no status transition need not be sent. State never implies availability inside Core, so the Adapter sends the explicit availability report even when it publishes an Observation in the same cycle.

## Observation projection

One MQTT callback receipt time becomes `adapter_received_at` for every Observation from that report. A sane parsed `dateutc` becomes `source_updated_at`; an invalid or implausible source time leaves it absent.

For each accepted report, the Adapter walks the static catalog in registration order and publishes one Observation for every present valid measurement, even when its normalized value equals prior State, so Hearth records fresh evidence and timestamps under the State contract. Apart from the connection-local report signature, the Adapter owns no checkpoint, outbox, or value cache and suppresses no changes.

Observations are created only with the generated `temperaturev1`, `relativehumidityv1`, `pressurev1`, `speedv1`, and `numericsensorv1` facades, never from hand-built type-specific JSON.

An SDK publication failure other than context cancellation or shutdown is terminal, and the SDK owns publication retry, so the Adapter never creates a second Observation to compensate for an ambiguous acknowledgement.

### Runtime coordination and blocking effects

A serial runtime coordinator owns MQTT generation, report and measurement deadlines, duplicate state, availability state, and ordered report work. MQTT callbacks and timers only submit bounded events.

Every blocking SDK call runs in a tracked effect goroutine that reports completion with its generation and operation sequence, so the coordinator never blocks its event loop on NATS acknowledgement. For one report, effects stay ordered as health recovery, then availability, then Observations in catalog order. At most one ordered report chain is active, and up to 64 later copied reports may wait; exceeding that bound ends the MQTT generation with the same visible overflow behavior as the callback relay. A disconnect or deadline invalidates the generation in coordinator memory and queues the corresponding health change without discarding report evidence already received.

An in-flight SDK publication is not cancelled because MQTT disconnects. It carries an already acquired weather observation and keeps the SDK's original envelope and Observation ID through retry, and its stale generation completion cannot mutate current coordinator state. Shutdown or terminal Session failure cancels and joins all tracked effects. An availability effect invalidated by a committed unhealthy transition may return its typed rejection; the coordinator treats that generation race as superseded and sends fresh availability after recovery instead of retrying the old report.

## Module interfaces and assembly

### Adapter module

Owner: `internal/adapters/ecowitt/adapter.go`.

```go
type Session interface {
    Register(context.Context, adapter.Registration) (adapter.Binding, error)
    SetHealth(context.Context, adapter.HealthReport) error
    ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
    PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Config struct {
    MQTTURL              string
    MQTTTopic            string
    MQTTClientID         string
    GatewayName          string
    OutdoorArrayName     string
    ExpectedPasskey      [16]byte
    UploadInterval       time.Duration
}

func New(session Session, config Config, logger *slog.Logger) (*Adapter, error)
func (ecowitt *Adapter) Run(context.Context) error
func (ecowitt *Adapter) HandleUnexpectedCommand(context.Context, adapter.Command, adapter.Responder) error
```

`New` validates the invariants that direct package callers need and returns a concrete Adapter. `Run` owns static registration, the MQTT reconnect loop, report processing, health, availability, and Observation publication. `HandleUnexpectedCommand` is a defensive lifecycle handler only: an impossible Command is rejected as unavailable without publishing MQTT traffic or an Observation.

### Private MQTT seam

Owner: `internal/adapters/ecowitt/mqtt.go`.

```go
type mqttDialer interface {
    Dial(context.Context, mqttConfig, func(mqttMessage)) (mqttConnection, error)
}

type mqttConnection interface {
    Subscribe(context.Context, string, byte) error
    Lost() <-chan error
    Close()
}
```

The production Paho implementation owns protocol selection, clean session, token waits, payload copying, and connection-loss signaling, and tests use a package-private fake. No MQTT interface leaves the package, and v1 publishes nothing to MQTT.

### Capability and report seams

Owners: `internal/adapters/ecowitt/capability_catalog.go` and `internal/adapters/ecowitt/report.go`.

```go
type deviceSlot string

const (
    gatewaySlot      deviceSlot = "gateway"
    outdoorArraySlot deviceSlot = "outdoor-array"
)

type measurementPlan struct {
    Slot       deviceSlot
    Field      string
    EntityKey  string
    EntityName string
    Descriptor adapter.EntityDescriptor
    Decode     func(string) (normalizedState, error)
}

func parseStationReport(payload []byte, receivedAt time.Time, expectedPasskey [16]byte) (stationReport, error)
func ecowittMeasurementCatalog() []measurementPlan
```

The catalog is the one definition site for registration order, field ownership, support, and decoder selection, and tests may derive traversal from it.

### Application assembly

Owner: `internal/app/ecowitt/run.go`.

```go
func Run(context.Context, Config, *slog.Logger) error
```

Assembly loads the PASSKEY from its file, derives the client ID, creates one SDK Session with software name `hearth-adapter-ecowitt` and version `0.1.0`, then constructs the Adapter. It supervises `Adapter.Run` and `Session.ServeCommands(Adapter.HandleUnexpectedCommand)` under one child context, matching the existing first-party Adapter lifecycle: when either loop returns, assembly cancels and joins its sibling. Serving commands gives prompt Session fencing and closure notification even though no valid Ecowitt Command route exists. Parent cancellation is graceful, and assembly closes MQTT and the SDK Session on return.

`cmd/hearth-adapter-ecowitt` follows existing flag, logging, signal, and exit-code conventions.

## Project layout

```text
cmd/
└── hearth-adapter-ecowitt/
    ├── main.go                         # new: executable flags, logging, signals, and exit
    └── main_test.go                    # new: command-level configuration failures
configs/
└── ecowitt.example.yaml                # new: safe loopback/trusted-LAN example
internal/app/
└── ecowitt/
    ├── config.go                       # new: YAML, PASSKEY-file loading, validation, client ID
    ├── config_test.go                  # new: configuration and secret handling contract
    ├── run.go                          # new: SDK and Adapter assembly
    └── run_integration_test.go         # new: process path through Mosquitto and native NATS
internal/adapters/
└── ecowitt/
    ├── adapter.go                      # new: runtime ownership and orchestration
    ├── mqtt.go                         # new: private Paho MQTT 3.1.1 seam
    ├── report.go                       # new: bounded URL-encoded wire parser and duplicate signature
    ├── capability_catalog.go           # new: static Device/Entity plans and unit support
    ├── registration.go                 # new: two slot registrations and route snapshot
    ├── observation.go                  # new: conversion and typed Observation construction
    ├── availability.go                 # new: measurement freshness and explicit reports
    ├── runtime.go                      # new: serial connection generation and timers
    ├── *_test.go                       # new: unit, contract, fuzz, and runtime tests
    └── testdata/
        ├── gw2000-ws90-report.txt       # new: sanitized real MQTT payload
        ├── gw2000-ws90-malformed.txt   # new: synthetic sibling-isolation fixture
        └── gw2000-ws90-missing.txt     # new: synthetic freshness fixture
README.md                               # modify: adapter setup, operation, and diagnostics
mise.toml                               # modify: executable build and Ecowitt fuzz tasks
.ko.yaml                                # modify: include hearth-adapter-ecowitt image
internal/adapters/zigbee2mqtt/          # modify only if a proven domain-neutral Paho defect requires shared correction; no planned refactor
docs/architecture.md                    # modify: record accepted Ecowitt process, trust, identity, and MQTT constraints
specs/ecowitt-adapter.md                # new: this implementation contract
```

The Ecowitt package owns all vendor vocabulary and conversions, and no generic weather, MQTT, or Adapter-runtime package is introduced. Zigbee2MQTT code may be copied in shape but is not refactored to remove duplication; shared infrastructure needs a demonstrated second consumer with identical semantics.

The three reusable physical-quantity Entity types add source schemas and manifests under `entitytypes/` and generated SDK facades and Core catalog wiring. They require no database, public HTTP, or wire-envelope changes.

## Deliverables and verification

| ID | Deliverable | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---|---:|---|---|---|
| D1 | Verify GW2000 MQTT wire behavior | Capture and sanitize one real GW2000+WS90 payload; prove Mosquitto delivery, topic, retain flag, and effective QoS behavior | M | `internal/adapters/ecowitt/testdata/`, investigation notes in this spec if behavior differs | Mosquitto broker migration | A1, A2 |
| D2 | Add config, parser, and MQTT transport | Validated YAML and secret loading, exact-topic Paho subscription, bounded form parser, duplicate suppression, reconnect behavior | L | `internal/app/ecowitt/config.go`, `internal/adapters/ecowitt/mqtt.go`, `report.go` | D1 | A3 through A8 |
| D3 | Register static weather capabilities | Two stable slot Devices and the complete typed v1 Entity catalog with canonical conversion rules | L | `capability_catalog.go`, `registration.go`, `observation.go` | D2 | A9 through A14 |
| D4 | Project runtime evidence | Ordered Observations, health recovery, explicit availability, and deterministic freshness timers | L | `adapter.go`, `runtime.go`, `availability.go`, `observation.go` | D3 | A15 through A20 |
| D5 | Assemble and validate the executable | Runnable process, Mosquitto integration, operator docs, image and build wiring, real-station acceptance, and repository validation | L | `cmd/hearth-adapter-ecowitt/`, `internal/app/ecowitt/`, `README.md`, `mise.toml`, `.ko.yaml`, `docs/architecture.md` | D4 | A21 through A26 |

The real GW2000 MQTT capture is D1 because it carries the most risk. If the device does not emit the specified URL-encoded fields or native MQTT cannot interoperate with the migrated Mosquitto broker, revise this spec before implementation proceeds rather than substituting synthetic fixtures.

### Test strategy

Tests state the protected behavior and the plausible defect. Oracles come from Hearth contracts, official Ecowitt configuration documentation, the sanitized real payload, independent conversion constants, MQTT 3.1.1 behavior, Mosquitto integration, and native NATS SDK integration—not from the production capability table, so a defect there cannot rewrite its own oracle.

| Layer | Likely defects |
|---|---|
| Config | Unsupported security claims, ambiguous or wildcard topics, bad intervals, a missing secret file, and PASSKEY handling without leaks. |
| Report parser | Percent-encoding gaps, duplicate keys, malformed escapes, missing identity, limit bypass at 64 KiB or 256 fields, station-type bypass, and an implausible source time. |
| Conversion | Rounding drift, wrong Fahrenheit, inHg, mph, or inch factors, non-finite results, clamping instead of rejection, and lost fractions. |
| Registration | Table-derived oracles, unstable keys or external IDs, wrong Entity order, wrong names or types, and non-empty operations. |
| State and duplicates | Cross-Entity fanout, suppressed unchanged values, accepted duplicates, missing receive timestamps, and a cache that survives reconnect. |
| Runtime and health | Under a fake clock: premature healthy status, a deadline anchored at the wrong event, silent report loss, a blocked event loop, and generation races. |
| Availability | Under a fake clock: early or late stale deadlines, never-seen Entities, a missing recovery resend, batches over 256 entries, and availability inferred from State. |
| MQTT integration | Wrong protocol level, clean session, or QoS, retained replay acceptance, and failed disconnect recovery. |
| Process integration | Schema, subject, assembly, or lifecycle mismatch across the full path. |
| Fuzz | Panics, unbounded work, secret-bearing errors, invalid typed State, and sibling interference. |
| Real station | Actual payload incompatibility, unstable identity, missed cadence, and failed recovery; see the real-station acceptance list below. |

After adding tests, run Gremlins against `./internal/adapters/ecowitt` and investigate behavioral survivors rather than adding syntax-only assertions. Finish each implementation stage with `mise run validate`.

### Real-station acceptance

Use the household GW2000 only after confirming the target MQTT screen and firmware version. A physical gateway configuration change needs operator approval and must preserve its prior settings for restoration.

1. Record the GW2000 and WS90 model and firmware versions without recording the PASSKEY.
2. Configure a unique subject-safe topic on the trusted-private Mosquitto broker supplied by the prerequisite migration.
3. Capture one live publication with its topic, retain flag, effective QoS, cadence, and payload length.
4. Sanitize the PASSKEY, MAC-like values, household names, and network addresses while preserving field names and representative value shapes.
5. Verify Core shows one gateway Device with four Entities and one outdoor-array Device with fifteen Entities, each with canonical units and expected typed State.
6. Verify repeated equal weather values create fresh Observation evidence.
7. Stop gateway publication without stopping native NATS. After exactly three configured intervals, require unhealthy `station_silent` and effective unavailability.
8. Restore publication and require healthy before fresh per-Entity availability and Observations.
9. Restart only the Adapter and verify canonical Device and Entity IDs remain stable.
10. Restart or interrupt the MQTT listener and verify `hearth.external_system_unavailable`, reconnect, and recovery.
11. Verify normal logs contain no raw payload, PASSKEY, secret-file content, station MAC, or weather values.
12. Restore the gateway's previous Customized Server configuration if this test replaced an existing destination.

### Acceptance criteria

- [ ] **A1:** A sanitized real GW2000+WS90 MQTT fixture records firmware, topic shape, cadence, payload shape, retain flag, and effective QoS without household secrets.
- [ ] **A2:** A real publication crosses the migrated Mosquitto broker to a Paho MQTT 3.1.1 subscriber; only translated Hearth messages cross native NATS.
- [ ] **A3:** Configuration accepts only the specified plain MQTT URL, two-segment topic, Device names, PASSKEY file, and 8 through 600 second interval.
- [ ] **A4:** Logs and errors never expose the PASSKEY, raw payload, secret-file contents, configured topic, station MAC, or weather values.
- [ ] **A5:** Paho uses MQTT 3.1.1, clean session, the deterministic 23-character client ID, QoS 1, bounded token waits, and Adapter-owned reconnect.
- [ ] **A6:** Retained, wrong-topic, wrong-PASSKEY, wrong-station-type, oversized, duplicate-key, over-field-limit, and malformed reports produce no evidence and refresh no freshness deadline.
- [ ] **A7:** An exact duplicate adds no evidence, while the same values at a later date publish fresh Observations.
- [ ] **A8:** Relay overflow and MQTT disconnect fail visibly and never lose a report silently.
- [ ] **A9:** Startup registers exactly the `gateway` and `outdoor-array` slot Devices as kind `sensor` before MQTT health can become healthy.
- [ ] **A10:** Binding keys, external IDs, Entity keys, and registration order match the static identity and capability tables and survive restart and display-name changes.
- [ ] **A11:** The gateway Device registers four Entities and the outdoor-array Device fifteen, using the generated temperature, relative-humidity, pressure, speed, and numeric-sensor facades.
- [ ] **A12:** Fahrenheit, inHg, mph, inches, and inches/hour normalize with the stated constants, and invalid results are rejected rather than clamped.
- [ ] **A13:** One invalid or absent measurement never suppresses valid sibling Observations or affects station health.
- [ ] **A14:** Every Entity is read-only with empty operation support, and the defensive command handler rejects any impossible delivery as unavailable without publishing MQTT traffic or an Observation.
- [ ] **A15:** Health stays unknown until positive or negative evidence: MQTT failure reports unhealthy immediately, SUBACK starts the initial three-interval deadline, and a live compatible report reports healthy before availability and Observations.
- [ ] **A16:** MQTT failure uses `hearth.external_system_unavailable`, and three intervals without a valid non-duplicate report use `adapter.hearth-adapter-ecowitt.station_silent`.
- [ ] **A17:** Valid measurements become explicitly available, while absent, malformed, and never-seen measurements become unavailable at exactly three intervals with `adapter.hearth-adapter-ecowitt.measurement_stale`.
- [ ] **A18:** Recovery waits for the healthy acknowledgement before sending the availability that Core previously cleared.
- [ ] **A19:** Each report shares one Adapter receive time and uses a sane `dateutc` as optional source time.
- [ ] **A20:** Every valid present measurement publishes in catalog order on every non-duplicate report, including unchanged values.
- [ ] **A21:** The executable follows existing config, logging, signal, cancellation, and exit conventions.
- [ ] **A22:** Real Paho, Mosquitto, and native NATS process integration tests prove the complete MQTT-to-Core path, disconnect recovery, prompt Session termination through supervised command serving, and event-loop progress while SDK acknowledgements are pending.
- [ ] **A23:** Parser and conversion fuzzers satisfy their bounded-work, no-panic, no-leak, and valid-State invariants.
- [ ] **A24:** README and example YAML document gateway configuration, the trusted-network-only boundary, PASSKEY handling, identity, health, availability, and diagnostics.
- [ ] **A25:** `.ko.yaml` and `mise.toml` include the executable and focused fuzz tasks, and no deployment artifact or real secret is checked in.
- [ ] **A26:** Focused mutation testing and `mise run validate` pass, and the reviewed diff contains only intended generated or formatting changes.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Real GW2000 MQTT behavior (payload, cadence, retain flag, effective QoS) differs from these assumptions or cannot interoperate with Mosquitto | Medium | High | Capture first in D1 and revise this spec before implementation. |
| Ecowitt omits physical sensor identifiers | Certain | Medium | Document slot identity, use fixed Bindings, and require a new Adapter ID for a distinct station. |
| Missing fields can mean transient omission or absent hardware | Medium | Medium | Keep static registrations and use a three-interval availability threshold with no retirement. |
| Future attached sensors fall outside the closed catalog | High | Low for v1 | Add channel slots in a later reviewed spec. |
| Unit conversion silently corrupts weather data | Low | High | Centralize constants, verify with independent fixtures and property tests, reject out-of-envelope values, and never clamp. |
| PASSKEY leaks through diagnostics or fixtures | Medium | High | Load from a secret file, compare in constant time, sanitize fixtures, and log fixed codes only. |
| Mosquitto and native NATS fail independently | Medium | Medium | Exercise each failure separately and preserve explicit health and recovery ordering. |

## Trade-offs made

| Chose | Over | Because |
|---|---|---|
| Native MQTT through Mosquitto | Customized Server HTTP | It uses Hearth's broker prerequisite and Paho client pattern while avoiding an inbound Adapter HTTP server. |
| One exact topic and station | Wildcard multi-station discovery | Routing, health, identity, and secret validation stay deterministic. |
| Slot Devices | Claimed physical hardware identity | Ecowitt supplies no stable attached-sensor IDs. |
| Reusable physical-quantity types for humidity, pressure, and speed | A generic numeric sensor for every measurement | Fixed canonical units give common measurements semantic contracts without introducing weather-specific types; direction, solar, UV, and rain remain generic until broader reuse justifies more types. |

## Success metrics

- Every supported live report reaches Core with canonical units within one upload interval plus local processing time.
- Station silence and individual stale measurements become operator-visible after exactly three configured intervals, without false positives from transiently omitted fields.

## Open questions

None.
