# ESPHome Adapter implementation proposal

**Status:** Draft for review
**Type:** Feature plan
**Effort:** XL, approximately 6 to 12 focused days after the protocol spike, at 55% confidence
**Date:** 2026-09-14
**Baseline:** commit `7df6254`
**Depends on:** existing Adapter SDK Session and built-in Entity types

## Problem and assumptions

Hearth needs direct observation and control of ESPHome nodes without Home Assistant or an MQTT contract in each firmware image. V1 targets configured nodes on a trusted LAN and uses existing Hearth Device, Entity, Binding, Observation, State, Command, availability, and health semantics. ESPHome details remain private to the Adapter. Hearth has no deployments, so this work needs no compatibility path.

The protocol spike must resolve three risks before implementation starts: TCP/Noise framing, protocol-version drift, and fresh post-dispatch State for changing and already-matching Commands. Cached State cannot satisfy a Hearth Command.

## Decision

Build `hearth-adapter-esphome` on ESPHome's native protobuf API, normally served on TCP port 6053. Require Noise encryption with one 32-byte base64 key per node. Plaintext is allowed only in test fixtures. Pin the upstream `.proto` files to an ESPHome release, generate private Go bindings, and own the framing and session code in `internal/adapters/esphome/nativeapi`.

One process reads a static node list and runs one Adapter instance per node. Each instance has its own `adapter_id`, SDK Session, NATS connection, native API connection, health, availability, and Command routes. A failed node cannot change another node's health or Command handling. The current SDK owns its NATS connection, so V1 uses one NATS connection per instance.

Use configured addresses in V1. This supports routed networks and leaves mDNS discovery and dynamic enrollment for later work. MQTT remains a fallback for nodes that cannot use the native API. Home Assistant is not part of the runtime path.

The existing Adapter SDK supplies registration, mapping inventory, health, availability, Observations, and Command serving. `internal/adapters/zigbee2mqtt` provides the planning and runtime pattern. `internal/adapters/homeassistant` provides the snapshot reconciliation and Command-evidence pattern. V1 requires no Core, wire-contract, or Entity-type changes.

The Go native API clients found during research lack the adoption and maintenance diversity required for a production dependency. The spike may compare the Apache-2.0 `mycontroller-org/esphome_api` and MIT `richard87/esphome-apiclient` implementations. The official Python `esphome/aioesphomeapi` client is the behavioral reference. Do not use the GPL-3.0-only `flavio-fernandes/go-aioesphomeapi` package.

If the spike cannot obtain fresh State after both changing and already-matching switch or light Commands, stop D4. Continue with read-only Entities unless a repeatable refresh method or MQTT provides valid Hearth Command evidence.

## V1 scope

V1 supports between 1 and 256 configured nodes. Each node maps to one Hearth Device and supports switches, basic lights with power and brightness, binary sensors, temperature, relative humidity, and pressure. Each node connection supplies Device information, typed Entity enumeration, an initial State snapshot, live State, health, availability, reconnect, and resynchronization.

V1 also includes generated-protocol drift checks, bounded frame parsing, fake-server integration tests, pinned ESPHome host fixtures, and physical-device validation.

V1 excludes dynamic enrollment and configuration reload, mDNS, MQTT transport, ESPHome sub-devices, new Hearth Entity types, deployment supervision, and untrusted-network exposure. ESPHome platforms that need new Entity models remain out of scope, including covers, climate, fans, locks, valves, text, media, alarms, and cameras. Bluetooth proxy, voice assistant, OTA, logs, and custom API actions are also out of scope.

## Sources

- Native API: <https://esphome.io/components/api/>
- Protocol framing: <https://developers.esphome.io/architecture/api/protocol_details/>
- API schema: <https://github.com/esphome/esphome/blob/dev/esphome/components/api/api.proto>
- MQTT: <https://esphome.io/components/mqtt/>
- mDNS: <https://esphome.io/components/mdns/>

## Identity and mapping

Each Adapter instance registers one configured slot with these identifiers:

- Binding key: `node`
- Device external ID: the normalized MAC from `DeviceInfoResponse`
- Device name: the ESPHome friendly name, or the node name when no friendly name exists
- Device kind: `light` when the node has a supported light, otherwise `relay` when it has a supported switch, otherwise `sensor`
- Entity key: `<platform>-<object_id>-<capability>`, normalized as a Hearth slug and checked for collisions
- Entity external ID: `<platform>/<native-api-key>/<capability>`

The capability suffix gives sibling Hearth Entities unique identifiers. For example, one native light can produce `light-desk-lamp-power` and `light-desk-lamp-brightness`.

The `node` Binding is a physical slot. Address, name, and MAC may change without changing its canonical Hearth Device or matching capability keys. Use a new `adapter_id` when replacement hardware must become a new canonical Device. ESPHome has no immutable Entity identity independent of firmware configuration, so changing an `id` or `object_id` can create new Hearth Entities. The Adapter marks the old owned mappings unavailable.

## Configuration types

Owner: `internal/app/esphome/config.go`.

```go
type Config struct {
    NATSURL string       `yaml:"nats_url"`
    Nodes   []NodeConfig `yaml:"nodes"` // Between 1 and 256 entries.
}

type NodeConfig struct {
    AdapterID           string `yaml:"adapter_id"`
    Address             string `yaml:"address"`             // host:port; explicit port required
    EncryptionKeyFile   string `yaml:"encryption_key_file"` // trimmed base64 for exactly 32 bytes
}
```

The loader validates the complete configuration before it claims a Session. It requires between 1 and 256 nodes, unique Hearth slugs for `adapter_id`, unique normalized addresses, and the existing NATS URL rules. Each address must contain a TCP host and non-zero port, with no URL user information, path, or query. Each key file must contain valid base64 that decodes to exactly 32 bytes.

Logs must not contain keys, secret-file contents, raw protocol frames, or untrusted device strings in event names or error codes.

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

The production factory claims a Session with the node's `adapter_id` and constructs a NodeRuntime. Its `Run` method supervises `Adapter.Run` with `Session.ServeCommands`. Its `Close` method cancels the Adapter, closes the native connection, and invokes `Session.Close`. `runFleet` starts and restarts node runtimes independently, then joins them during shutdown. Only invalid static configuration or a process-wide invariant may terminate the process.

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

The concrete Connection owns one reader loop. It checks frame lengths before allocation, decodes known message IDs, ignores unknown fields or messages only when the protocol permits it, completes correlated requests, emits State updates, and sends keepalives. It returns typed errors for malformed frames, authentication failure, major-version mismatch, slow-consumer overflow, and disconnect. The Adapter owns Hearth registration and mapping.

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
| `sensor`, humidity device class/unit | `hearth.relativehumidity/v1` | Finite value between 0 and 100 percent |
| `sensor`, pressure device class/unit | `hearth.pressure/v1` | Convert supported pressure units to hPa |
| `light` | power and optional brightness | Require advertised capabilities; normalize brightness to an integer between 0 and 100 |

Unknown platforms, unsupported units, malformed bounds, key collisions, and unsupported light capability combinations omit only the affected Entity and emit bounded diagnostics. They do not make otherwise valid siblings unusable. All vendor DTOs remain private to the Adapter.

## Project layout

```text
cmd/
└── hearth-adapter-esphome/
    ├── main.go                         # new: process entry point
    └── main_test.go                    # new: process behavior and safe errors
internal/
├── app/esphome/
│   ├── config.go                       # new: YAML and secret validation
│   ├── config_test.go                  # new: configuration boundaries
│   ├── run.go                          # new: composition root
│   ├── supervisor.go                   # new: node lifecycle and failure isolation
│   ├── supervisor_test.go              # new: restart, isolation, and shutdown tests
│   └── run_test.go                     # new: startup, cancellation, and secrecy
└── adapters/esphome/
    ├── adapter.go                      # new: Hearth lifecycle and Session boundary
    ├── connection.go                   # new: reconnect and inventory synchronization
    ├── coordinator.go                  # new: routes, generations, State, and Commands
    ├── planner.go                      # new: inventory to Device registration
    ├── entity_switch.go                # new: power mapping and Commands
    ├── entity_sensor.go                # new: sensor mappings
    ├── entity_light.go                 # new: power and brightness mapping
    ├── logging.go                      # new: bounded event and reason codes
    ├── nativeapi/
    │   ├── client.go                   # new: typed client and session
    │   ├── frame.go                    # new: bounded plaintext and Noise framing
    │   ├── noise.go                    # new: NNpsk0 transport setup
    │   ├── registry.go                 # new: generated message-ID dispatch
    │   ├── proto/                      # new: pinned upstream inputs and provenance
    │   └── gen/                        # generated: private Go bindings
    └── testdata/                       # new: sanitized protocol and inventory fixtures
configs/
└── esphome.example.yaml                # new: trusted-LAN example
README.md                               # modify: operator documentation
.ko.yaml                                # modify: image build
.github/workflows/release.yml           # modify: image publication
mise.toml                               # modify: generation checks and image tasks
```

Core packages, `contracts/v1`, and existing Entity-type schemas remain unchanged in V1.

## Deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---:|---|---|---|
| D1 | Prove Noise, version negotiation, inventory, State, Command evidence, reconnect, and connection coexistence | L | `internal/adapters/esphome/nativeapi`, spike tests and fixtures | - | A1 through A3 |
| D2 | Build the private native API client, pinned generated protocol, framing, typed errors, and keepalive | L | `internal/adapters/esphome/nativeapi/**`, generation task | D1 | A4 and A5 |
| D3 | Add multi-node supervision and the read-only Entity slice with isolated health and availability | XL | `internal/adapters/esphome`, `internal/app/esphome`, `cmd/hearth-adapter-esphome` | D2 | A6 through A8 |
| D4 | Add switch and basic-light Commands with linked outcome evidence | L | `internal/adapters/esphome/entity_{switch,light}.go`, coordinator tests | D3 | A9 and A10 |
| D5 | Add packaging, operator documentation, and physical validation | M | config, documentation, build, release, and validation files | D4 | A11 and A12 |

## Acceptance criteria

- [ ] **A1:** Against a pinned ESPHome host fixture and one real encrypted device, the spike completes Noise, Hello, DeviceInfo, ListEntities, initial State, and one live State update.
- [ ] **A2:** A changing switch/light Command and an already-matching Command each yield a demonstrably fresh post-dispatch State response; no cached State is reused as evidence.
- [ ] **A3:** Wrong keys, disconnect/reboot, a full connection-slot condition, unknown messages, and minor-version differences fail or recover predictably without panic, secret leakage, or blocked reads.
- [ ] **A4:** Proto inputs record their upstream repository/tag/license/checksum, generated output is reproducible, and CI detects drift.
- [ ] **A5:** Frame parsing enforces size bounds and survives fuzzing malformed plaintext and Noise input without panic or unbounded allocation.
- [ ] **A6:** More than eight children are scheduled independently when the first eight claims remain blocked, and other Adapter identities can claim and register. Each successful node registers one Device with deterministic, collision-free Entity keys. Registration and reconnect preserve capability keys across a MAC change and give multiple capabilities from one native light distinct keys. Unsupported or malformed Entities do not suppress valid siblings. Duplicate Adapter IDs and normalized addresses fail before any Session claim.
- [ ] **A7:** Each node subscribes before snapshot reconciliation. Startup and reconnect publish initial State and explicit availability for all supported Entities without allowing an older snapshot to overwrite a newer update.
- [ ] **A8:** An offline node or runtime authentication failure changes only that Adapter instance's health, availability, reconnect loop, and Command outcomes. Healthy siblings continue publishing State and serving Commands. Normal shutdown joins all children within five seconds. A blocked Command handler or unavailable Core returns a bounded shutdown error without claiming a successful join.
- [ ] **A9:** Switch and brightness Commands are serialized per native Entity, honor Hearth deadlines, and map upstream failures to bounded rejections.
- [ ] **A10:** Accepted observed Commands can be satisfied only by their own fresh linked matching Observation.
- [ ] **A11:** The executable, example config, release image, and README document trusted-LAN scope, key handling, supported platforms, identity limitations, and troubleshooting codes.
- [ ] **A12:** `mise run validate` passes. An ESPHome-specific procedure runs at least two encrypted nodes for 24 hours and records reconnect and sibling-isolation evidence without a blocked reader or secret leak. The procedure uses an operator-approved host, does not start or change shared services or a competing Core, keeps environment secrets out of the repository, and requires explicit approval before actuation, reboot, or connection-slot exhaustion. The current Zigbee-only real-device skill cannot run this proof unchanged.

## Test strategy

| Layer | Acceptance | Method |
|---|---|---|
| Unit | A6, A9 | Table and property tests with private DTOs and a fake Session |
| Supervisor | A6, A8 | Independently controlled fake runtimes, blocked claims, and shutdown tests |
| Fuzz | A3, A5 | Frame boundaries, lengths, and message dispatch |
| Protocol | A1 through A5 | Fake server, golden frames, and two pinned ESPHome host releases |
| End to end | A6 through A10 | `hearthd`, the Adapter, NATS, and firmware fixtures |
| Physical device | A1 through A3, A8, A10, A12 | ESPHome-specific approved-actuation checklist |

## Risks and revisit triggers

| Risk | Mitigation |
|---|---|
| Native protocol or Noise errors | Use a standard Noise library, pin and regenerate from an upstream release, check the Hello version, compare with the official client, and fuzz frame parsing. |
| ESPHome connection limits | Keep one drained connection per Adapter instance and test beside Home Assistant and the ESPHome dashboard. |
| Firmware renames change Entity identity | Use deterministic keys and mark missing owned mappings unavailable. Use a new `adapter_id` when hardware needs a new Device identity. |
| One process can affect the fleet | Isolate node errors, exit only for invalid static configuration or process-wide invariants, and rely on deployment supervision. |
| Startup or shutdown blocks | Never limit indefinite Session claims with a shared permit. Limit finite native connection attempts, close connections before Sessions, and enforce the five-second fleet shutdown deadline. |

Revisit the design when measured NATS connection or memory cost justifies a shared SDK connection, static configuration justifies reload or mDNS enrollment, a maintained Go client can replace the private client, or MQTT-only nodes become common.
