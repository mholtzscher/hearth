# ESPHome Adapter implementation proposal

**Status:** Draft for review
**Type:** Feature plan
**Effort:** XL, approximately 6–12 focused days after the protocol spike, at 55% confidence
**Date:** 2026-09-14
**Baseline:** current `esphome` worktree
**Depends on:** existing Adapter SDK Session and built-in Entity types

## Assumptions

The user delegated product and technical choices, so this proposal assumes:

1. The first useful milestone connects configured ESPHome nodes on the same trusted LAN, observes their common Entities, and controls switches and basic lights.
2. Home Assistant must not be required. Hearth talks directly to each node.
3. V1 favors a reliable vertical slice over broad ESPHome platform coverage.
4. One `hearth-adapter-esphome` process supervises multiple node runtimes. Each node remains a distinct Adapter instance with its own stable `adapter_id`, SDK Session, health, availability, connection, and Command route because an ESPHome node is one configured external system under Hearth's current Adapter definition.
5. Addresses are explicitly configured. Protocol Entity enumeration is in scope; LAN-wide mDNS discovery and dynamic enrollment are not. This avoids introducing general discovery, which `docs/architecture.md` currently defers, and works across routed VLANs.
6. Noise encryption is required for production behavior. Plaintext may exist only in test fixtures.
7. No backward compatibility is required because Hearth has no deployments.

## Problem

Hearth cannot currently represent or control devices running ESPHome without routing them through Home Assistant or requiring each firmware image to publish a Hearth-specific MQTT contract. The Adapter must preserve Hearth's canonical Device, Entity, Binding, Observation, State, Command, availability, and health semantics while keeping all ESPHome protocol concepts private to the Adapter.

The highest-risk issue is not registration or entity mapping; existing Zigbee2MQTT and Home Assistant Adapters already establish those patterns. The highest risks are the native API's custom TCP/Noise framing, protocol-version drift, and proving that an observed Command—including a no-op command whose target already matches—can produce a fresh post-dispatch state report suitable for Hearth Command evidence.

## Research summary

### Repository fit

- `sdk/adapter/session.go` is the intended Core boundary: claim, registration, mapping inventory, health, availability, Observations, Entity Events, and Command serving already exist.
- `internal/adapters/zigbee2mqtt` is the closest discovery/planning/runtime precedent.
- `internal/adapters/homeassistant` is the closest snapshot-plus-live-state and Command-evidence precedent.
- Core and wire contracts need no ESPHome-specific change for the initial Entity set.
- Existing built-in types cover power, brightness, color, temperature, humidity, pressure, speed, generic numeric and binary sensors, settings, actions, and events. They do not yet cover covers, climate, fans as a compound domain, locks, text, media, alarms, dates, or times.

### External protocol evidence

ESPHome's native API is a protobuf protocol served directly by each node, normally on TCP port 6053. It supports typed Entity enumeration, initial/live State subscription, typed Commands, Device information, and optional Noise encryption with a 32-byte base64 key. ESPHome documents small connection limits and disconnecting slow consumers, so Hearth must use one continuously drained connection per Adapter instance.

ESPHome MQTT is viable but exposes Home Assistant-oriented retained discovery JSON and string/topic conventions rather than the native typed inventory. It adds the broker to the control failure domain and requires MQTT configuration in every firmware image. Its advantage is implementation speed because Paho and Mosquitto already exist in this repository.

No Go native-API package found during research has enough adoption and maintenance diversity to make it a low-risk foundational dependency. Candidate references include:

- `github.com/mycontroller-org/esphome_api` (Apache-2.0)
- `github.com/richard87/esphome-apiclient` (MIT)
- `github.com/flavio-fernandes/go-aioesphomeapi` (GPL-3.0-only; unsuitable unless Hearth deliberately accepts that license)
- `github.com/esphome/aioesphomeapi` (official Python behavioral reference)

Authoritative sources:

- Native API: <https://esphome.io/components/api/>
- Protocol framing: <https://developers.esphome.io/architecture/api/protocol_details/>
- API schema: <https://github.com/esphome/esphome/blob/dev/esphome/components/api/api.proto>
- MQTT: <https://esphome.io/components/mqtt/>
- mDNS: <https://esphome.io/components/mdns/>

## Decision

Build `hearth-adapter-esphome` on the **native API**. One process loads a static list of nodes and supervises one isolated Adapter instance per node. Pin and check in the upstream `.proto` inputs at a named ESPHome release, generate private Go bindings, and own the small framing/Noise/session layer in `internal/adapters/esphome/nativeapi`.

Do not collapse the fleet into one Adapter instance: aggregate health would let one unreachable node pre-fail Commands for healthy nodes. The process is only an operational supervisor; Core continues to see independent Adapter identities and failure domains. V1 may open one NATS connection per SDK Session because the current SDK owns its connection; sharing one process does not imply shared health or transport state.

Do not begin with MQTT and do not proxy through Home Assistant. MQTT remains a later fallback for MQTT-only firmware. Do not add mDNS in V1: configured addressing is simpler, works across subnets, and keeps general LAN discovery outside this slice.

This is a risk-first plan. A bounded protocol spike must pass before broad Adapter implementation. If the spike cannot reliably obtain fresh post-command State for both changing and already-matching switch/light Commands, stop and reconsider one of these options rather than weakening Hearth's Command semantics:

1. use a proven client behavior that requests a fresh state snapshot without accumulating subscriptions;
2. limit V1 to read-only Entities while the protocol issue is resolved; or
3. choose MQTT only if it can prove stronger command evidence with the target firmware.

## Alternatives

| Option | Advantages | Costs | Decision |
|---|---|---|---|
| Native API, owned client | Typed inventory and Commands; direct availability; no broker/HA runtime dependency | New protobuf, framing, Noise, and compatibility work | **Choose** |
| Native API, third-party Go client | Faster initial implementation | Small-maintainer dependency and protocol-drift risk | Use as reference and spike comparator, not foundational dependency |
| ESPHome MQTT | Reuses Paho/Mosquitto; retained State and LWT | Firmware/broker coupling; HA-shaped discovery; stringly mappings; broker failure domain | Defer as fallback |
| Home Assistant proxy | Fastest if HA already manages devices | Violates direct ownership goal and makes HA a runtime dependency | Reject |

## V1 scope

V1 includes:

- one process supervising 1–256 statically configured ESPHome node Adapter instances;
- one SDK Session, native API connection, health assessment, availability set, and Command route per node;
- explicit host and port per node, plus a distinct Noise key loaded from a separate file;
- one immediately scheduled child goroutine per node, jittered initial connection attempts, and independent reconnect loops so one blocked claim or offline node neither blocks nor restarts healthy siblings;
- Device info, typed Entity enumeration, initial State snapshot, live State, reconnect, resynchronization, Adapter health, and explicit Entity availability;
- one Hearth Device per ESPHome node;
- switches (`hearth.power/v1`), binary sensors, temperature, relative humidity, and pressure;
- basic lights: power and brightness first; color temperature and color only after their capability and evidence behavior passes focused tests;
- stable Adapter-local Binding key `node`; Entity keys derived from ESPHome platform, `object_id`, and Hearth capability; the native uint32 API key plus capability is mutable route metadata/external identity;
- fresh linked Observation evidence for accepted observed Commands;
- generated protocol drift checks, framing fuzz tests, fake-server integration tests, an ESPHome host fixture if practical, and real-device validation.

V1 defers:

- mDNS discovery, automatic enrollment, runtime configuration reload, and dynamic fleet membership;
- MQTT transport;
- ESPHome sub-device grouping;
- covers, climate, fans, locks, valves, text, media, alarms, camera, Bluetooth proxy, voice assistant, OTA, logs, and custom API actions;
- dynamic Entity types or Core runtime type registration;
- production supervision and untrusted-network exposure.

## Identity and mapping

The registration uses:

- Binding key: `node`
- Device external ID: normalized MAC from `DeviceInfoResponse`
- Device name: ESPHome friendly name, falling back to node name
- Device kind: `light` if a supported light exists, otherwise `relay` if a supported switch exists, otherwise `sensor`
- Entity key: `<platform>-<object_id>-<capability>` after Hearth slug normalization and collision checking; examples are `light-desk-lamp-power` and `light-desk-lamp-brightness`
- Entity external ID: `<platform>/<native-api-key>/<capability>` so sibling Hearth Entities projected from one native ESPHome Entity remain unique

ESPHome does not expose an immutable per-Entity identity independent of firmware configuration. Renaming an ESPHome `id`/`object_id` may therefore create new Hearth Entities while old owned mappings become unavailable. V1 documents this rather than pretending the identity is stable.

The Binding key `node` deliberately models a configured physical slot, matching the existing Ecowitt slot precedent. Address, name, and MAC are mutable evidence for that slot; replacing hardware at the configured endpoint preserves the canonical Hearth Device and any matching capability keys. Operators who need the replacement to become a distinct canonical Device must use a new `adapter_id`. Registration and reconnect tests must cover both MAC change with stable capability keys and multiple capability siblings from one native light.

## Configuration types

Owner: `internal/app/esphome/config.go`.

```go
type Config struct {
    NATSURL string       `yaml:"nats_url"`
    Nodes   []NodeConfig `yaml:"nodes"` // 1–256 entries
}

type NodeConfig struct {
    AdapterID           string `yaml:"adapter_id"`
    Address             string `yaml:"address"`             // host:port; explicit port required
    EncryptionKeyFile   string `yaml:"encryption_key_file"` // trimmed base64 for exactly 32 bytes
}
```

Validation requires 1–256 nodes, a unique Hearth slug for every `adapter_id`, no duplicate normalized configured address, the existing NATS URL rules, a TCP host and non-zero port without URL user info/path/query, a non-empty secret-file path, valid base64, and exactly 32 decoded key bytes. Static configuration errors fail the process before any child Session is claimed. Logs must never include a key, file contents, raw protocol frames, or untrusted device strings as event/error codes.

Example:

```yaml
nats_url: nats://127.0.0.1:4222
nodes:
  - adapter_id: office-panel
    address: office-panel.local:6053
    encryption_key_file: /run/secrets/office-panel-api-key
  - adapter_id: kitchen-light
    address: kitchen-light.local:6053
    encryption_key_file: /run/secrets/kitchen-light-api-key
```

## Adapter and protocol interfaces

Owner: `internal/app/esphome/supervisor.go`.

```go
type NodeRuntime interface {
    Run(context.Context) error
    Close() error
}

type NodeRuntimeFactory interface {
    Start(context.Context, NodeConfig, *slog.Logger) (NodeRuntime, error)
}

func runFleet(context.Context, Config, NodeRuntimeFactory, *slog.Logger) error
```

The production factory claims one SDK Session using the node's `adapter_id`, constructs one ESPHome Adapter, and supervises its `Run` and `Session.ServeCommands` together. `runFleet` starts every configured child, joins all children on shutdown, and restarts an unexpectedly terminated child with bounded jitter without canceling healthy siblings. Static configuration and secret-loading failures occur before children start and fail the process. Node connection, authentication, protocol, Session fencing, and runtime failures are node-local; the child reports bounded diagnostics and retries or recreates only that node runtime. A process-wide invariant failure may terminate the process.

Owner: `internal/adapters/esphome/adapter.go`.

```go
type Session interface {
    ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
    Register(context.Context, adapter.Registration) (adapter.Binding, error)
    SetHealth(context.Context, adapter.HealthReport) error
    ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
    PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Config struct {
    Address       string
    EncryptionKey [32]byte
}

type Adapter struct { /* Session, native API dialer, routes, coordinator, logger */ }

func New(session Session, config Config, logger *slog.Logger) (*Adapter, error)
func (a *Adapter) Run(context.Context) error
func (a *Adapter) HandleCommand(context.Context, adapter.Command, adapter.Responder) error
```

Owner: `internal/adapters/esphome/nativeapi/client.go`.

```go
type Dialer interface {
    Dial(context.Context, Endpoint, [32]byte) (Connection, error)
}

type Connection interface {
    Hello(context.Context, ClientHello) (ServerHello, error)
    DeviceInfo(context.Context) (DeviceInfo, error)
    ListEntities(context.Context) ([]EntityDescriptor, error)
    SubscribeStates(context.Context) (<-chan StateUpdate, error)
    SendCommand(context.Context, CommandRequest) error
    Close() error
}
```

The concrete Connection owns exactly one reader loop. It validates bounded frame lengths before allocation, decodes known message IDs, safely ignores forward-compatible unknown fields/messages where protocol rules permit, dispatches correlated request completion and State updates, sends keepalives, and turns malformed framing, authentication failure, major-version mismatch, slow-consumer overflow, or disconnect into bounded typed errors. The Adapter—not the client—owns Hearth registration and semantic mapping.

## Runtime behavior

### Process supervisor

1. Load and validate the complete static node list and every key file before claiming any Session.
2. Start one child goroutine per node immediately. Apply a bounded random initial delay so the fleet does not stampede NATS, but never hold a shared permit while `adapter.Connect` may retry an active claim; one blocked claim therefore cannot starve another identity.
3. Each child claims its own SDK Session under its configured `adapter_id`, then supervises that node Adapter and its runtime-scoped Command server. Native API dial/Noise attempts may use an eight-slot shared limiter only for one finite attempt; each attempt releases its slot before node-local backoff.
4. A node-local exit closes only that Session and restarts only that child with jittered backoff. Healthy siblings continue serving State and Commands.
5. Process cancellation first cancels every child and closes every native connection, then invokes potentially blocking `Session.Close` calls concurrently. The supervisor waits up to one fleet-wide five-second deadline. Success means every child joined; timeout returns a shutdown error and explicitly does not claim that blocked handlers or Sessions exited before external process termination.

### Node runtime

1. Connect to the node and complete Noise, Hello/version, and DeviceInfo exchange.
2. Start State subscription before or atomically with the initial snapshot so no transition is lost.
3. Enumerate all Entities to a complete inventory, plan supported Hearth Entities, page existing owned mappings, and register one Device.
4. Atomically install routes only after registration succeeds.
5. Report that node's Adapter instance healthy, then report its supported Entities available, then publish the initial snapshot as ordinary Observations.
6. Continuously drain State updates and publish every valid report, including same-value reports.
7. On disconnect, report only that Adapter instance unhealthy, clear routes for the failed connection generation, reconnect with jittered exponential backoff, and repeat inventory plus snapshot reconciliation.
8. For a Command, serialize per native Entity key, send the typed upstream request, call `Responder.Accept` only after successful protocol write/acceptance, then publish the first fresh matching post-dispatch State update through the returned evidence capability. Do not satisfy from cached or pre-command State.

## Entity mapping rules

| ESPHome platform | Hearth type | V1 rule |
|---|---|---|
| `switch` | `hearth.power/v1` | State and `set`; exact boolean outcome |
| `binary_sensor` | `hearth.binarysensor/v1` | Read-only boolean |
| `sensor`, temperature device class/unit | `hearth.temperature/v1` | Convert finite Celsius/Fahrenheit values to milli-Celsius |
| `sensor`, humidity device class/unit | `hearth.relativehumidity/v1` | Finite 0–100 percent |
| `sensor`, pressure device class/unit | `hearth.pressure/v1` | Convert supported pressure units to hPa |
| `light` | power + optional brightness | Require matching advertised color modes/capabilities; brightness normalized to integer 0–100 |

Unknown platforms, unsupported units, malformed bounds, key collisions, and unsupported light capability combinations omit only the affected Entity and emit bounded diagnostics. They do not make otherwise valid siblings unusable. All vendor DTOs remain private to the Adapter.

## Project layout

```text
cmd/
└── hearth-adapter-esphome/
    ├── main.go                         # new — process entry point
    └── main_test.go                    # new — process behavior and safe errors
internal/
├── app/esphome/
│   ├── config.go                       # new — YAML and secret validation
│   ├── config_test.go                  # new — configuration boundaries
│   ├── run.go                          # new — composition root
│   ├── supervisor.go                   # new — multi-node child lifecycle and failure isolation
│   ├── supervisor_test.go              # new — child restart, isolation, and bounded shutdown
│   └── run_test.go                     # new — startup, cancellation, and secrecy
└── adapters/esphome/
    ├── adapter.go                      # new — Hearth-facing lifecycle and Session seam
    ├── connection.go                   # new — reconnect and inventory synchronization
    ├── coordinator.go                  # new — routes, generations, State, Commands
    ├── planner.go                      # new — inventory to one Device registration
    ├── entity_switch.go                # new — power mapping and Commands
    ├── entity_sensor.go                # new — binary/temperature/humidity/pressure mappings
    ├── entity_light.go                 # new — power/brightness mapping
    ├── logging.go                      # new — bounded event and reason codes
    ├── nativeapi/
    │   ├── client.go                   # new — typed client boundary and session
    │   ├── frame.go                    # new — bounded plaintext/Noise framing
    │   ├── noise.go                    # new — NNpsk0 transport setup
    │   ├── registry.go                 # new — generated message-ID dispatch glue
    │   ├── proto/                      # new — pinned upstream proto inputs + provenance
    │   └── gen/                        # new/generated — private Go protobuf bindings
    └── testdata/                       # new — sanitized protocol and inventory fixtures
configs/
└── esphome.example.yaml                # new — loopback/trusted-LAN example
README.md                               # modify — build/run/security/support docs
.ko.yaml                                # modify — image build
.github/workflows/release.yml           # modify — image publication
mise.toml                               # modify — generation/check and image tasks
```

Core packages, `contracts/v1`, and existing Entity-type schemas remain unchanged in V1.

## Deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---:|---|---|---|
| D1 | Risk spike proves Noise, version negotiation, Entity inventory, initial/live State, changing and no-op Command evidence, reconnect, and connection coexistence | L | `internal/adapters/esphome/nativeapi`, spike tests/fixtures | - | A1–A3 |
| D2 | Production private native API client with pinned generated protocol, bounded framing, typed errors, keepalive, and fuzz/golden tests | L | `internal/adapters/esphome/nativeapi/**`, generation task | D1 | A4–A5 |
| D3 | Multi-node process supervisor plus read-only vertical slice registers each node independently and publishes binary/temperature/humidity/pressure State with isolated health and availability | XL | `internal/adapters/esphome`, `internal/app/esphome`, `cmd/hearth-adapter-esphome`, `CONTEXT.md`, `docs/architecture.md` | D2 | A6–A8 |
| D4 | Switch and basic light Commands publish fresh linked outcome evidence | L | `internal/adapters/esphome/entity_{switch,light}.go`, coordinator tests | D3 | A9–A10 |
| D5 | Operator packaging, documentation, an ESPHome-specific physical validation procedure, and broad repository validation | M | config/docs/build/release files, validation procedure, and evidence | D4 | A11–A12 |

## Acceptance criteria

- [ ] **A1:** Against a pinned ESPHome host fixture and one real encrypted device, the spike completes Noise, Hello, DeviceInfo, ListEntities, initial State, and one live State update.
- [ ] **A2:** A changing switch/light Command and an already-matching Command each yield a demonstrably fresh post-dispatch State response; no cached State is reused as evidence.
- [ ] **A3:** Wrong keys, disconnect/reboot, a full connection-slot condition, unknown messages, and minor-version differences fail or recover predictably without panic, secret leakage, or blocked reads.
- [ ] **A4:** Proto inputs record their upstream repository/tag/license/checksum, generated output is reproducible, and CI detects drift.
- [ ] **A5:** Frame parsing enforces size bounds and survives fuzzing malformed plaintext and Noise input without panic or unbounded allocation.
- [ ] **A6:** More than eight configured children are scheduled independently even when the first eight claims remain blocked; other available identities can claim and register. Each successful node registers exactly one canonical Device with deterministic, collision-free Entity keys, while duplicate Adapter IDs and duplicate normalized addresses fail before any Session claim.
- [ ] **A7:** Each node starts its subscription before snapshot reconciliation; startup/reconnect cannot let older snapshot data overwrite a newer live update.
- [ ] **A8:** Taking one node offline changes only its Adapter health, Entity availability, reconnect loop, and Command outcomes; a healthy sibling continues publishing State and serving Commands. Normal shutdown joins all children within five seconds; a deliberately blocked Command handler or unavailable Core produces a bounded shutdown error without falsely reporting a successful join.
- [ ] **A9:** Switch and brightness Commands are serialized per native Entity, honor Hearth deadlines, and map upstream failures to bounded rejections.
- [ ] **A10:** Accepted observed Commands can be satisfied only by their own fresh linked matching Observation.
- [ ] **A11:** The executable, example config, release image, and README document trusted-LAN scope, key handling, supported platforms, identity limitations, and troubleshooting codes.
- [ ] **A12:** `mise run validate` passes, and physical-device evidence is collected through a documented ESPHome-specific procedure with encrypted-node prerequisites and explicit approval before actuation, reboot, or connection-slot exhaustion. The current Zigbee-only real-device skill must not be used unchanged for this proof.

## Test strategy

| Layer | Risk | Method |
|---|---|---|
| Unit | Entity eligibility, identity, units, collisions, Command matching | Table/property tests with private DTOs and fake Session |
| Supervisor | Multi-node startup, child restart, failure isolation, shutdown | Fake runtime factory with independently controlled children and duplicate-config tests |
| Fuzz | Hostile/truncated frames and protobuf dispatch | Fuzz frame boundaries, length fields, and message registry |
| Protocol integration | Noise/framing/session/reconnect | Deterministic fake ESPHome server plus golden captured frames |
| Firmware integration | Real server behavior and version drift | Pinned ESPHome host builds for at least two supported releases |
| Real device | Wi-Fi timing, no-op Commands, connection slots, reboot | ESPHome-specific checklist; require operator approval before actuation/reboot and preserve the safety rules of the existing homelab workflow |
| End to end | Core registration, State, health, availability, Commands | `hearthd` + Adapter + NATS + firmware fixture |

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| No fresh State for no-op Commands | Medium | Critical | Make it a D1 go/no-go criterion; do not weaken Command semantics |
| Native protocol or generated message IDs drift | Medium | High | Pin upstream tag, reproducible generation, Hello version gate, multi-version fixture |
| Noise implementation flaw | Medium | High | Use standard Noise primitive, compare with official Python client/reference captures, fuzz and negative tests |
| ESP nodes have few connection slots | Medium | Medium | Exactly one connection per Adapter; keepalive; promptly drain; test alongside HA/dashboard |
| Entity identity changes after firmware rename | High | Medium | Document limitation, deterministic keys, mark missing owned mappings unavailable, require explicit reconciliation |
| Scope explodes across ESPHome's many platforms | High | High | Fixed V1 mapping table; omit unsupported Entities; add types only in later specs |
| One process crash affects the whole configured fleet | Low | High | Keep node runtime errors isolated; reserve process exit for invalid static config or process-wide invariants; rely on external process supervision |
| One SDK-owned NATS connection per node becomes expensive | Medium | Medium | Measure at household scale; add an SDK shared-connection composition seam only when evidence justifies it |
| Concurrent startup creates a LAN/NATS connection storm | Medium | Medium | Jitter child start; limit only finite native connection attempts, never long-lived SDK claims or retry backoff |
| A Command handler ignores cancellation during shutdown | Medium | Medium | Close native connections first, close Sessions concurrently, enforce one fleet deadline, and return an explicit timeout rather than claiming a join |

## Revisit triggers

Reconsider the chosen shape if any of these become true:

- D1 cannot prove fresh Command evidence on representative firmware.
- Per-node NATS connections become a measured broker or memory burden, justifying an SDK shared-connection seam.
- Static configuration becomes operationally burdensome, justifying runtime reload or mDNS-assisted enrollment.
- A Go native API client gains durable multi-maintainer stewardship and tracks ESPHome releases reliably.
- The product accepts general discovery, at which point mDNS `_esphomelib._tcp` can supply candidate addresses but not credentials or trusted identity.
- A meaningful share of household nodes are MQTT-only and cannot enable the native API.

## Success metrics

- One process runs at least two encrypted ESPHome nodes for 24 hours with reconnect recovery, no lost reader progress, and no secret leakage.
- Disconnecting or misconfiguring one node does not interrupt State or Commands for a healthy sibling.
- All supported Entities acquire initial State and explicit availability after every connection generation.
- Changing and no-op power/brightness Commands satisfy only through fresh linked Observations within their Hearth deadlines.
- Unsupported ESPHome platforms remain isolated and visible through bounded diagnostics without destabilizing supported Entities.
