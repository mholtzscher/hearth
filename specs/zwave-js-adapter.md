# Z-Wave JS Adapter implementation spec

**Status:** Draft for review
**Type:** Feature plan
**Effort:** XL, approximately 5 to 8 focused days at 60% confidence
**Date:** 2026-09-15
**Baseline:** branch `zwave` at `c8abc58`
**Depends on:** the existing Adapter SDK, Adapter-owned mapping inventory, multi-Entity registration, Adapter health, and Entity availability
**Target evidence:** an existing household Z-Wave network managed by Z-Wave JS UI; one binary switch and one multilevel dimmer

## Problem

Hearth cannot natively observe or control devices on the household's existing Z-Wave network. Re-pairing that network would be disruptive and would risk losing security-key continuity, so Hearth needs a first-party Adapter that uses the operator-managed Z-Wave JS UI service without taking ownership of the controller, network cache, security keys, inclusion, exclusion, or interviews.

The first useful slice is deliberately narrow: discover every ready Binary Switch and Multilevel Switch endpoint, preserve stable Hearth identity while the node remains in the same Z-Wave network, project power and native dimmer level, report health and availability, and execute observed Commands using a fresh post-dispatch poll. Z-Wave-specific node, endpoint, Command Class, and Value ID concepts remain private to the Adapter.

## Decision and scope

Add `hearth-adapter-zwavejs`, a stateless Go process that connects to the schema-versioned `@zwave-js/server` WebSocket exposed by Z-Wave JS UI:

```text
Hearth HTTP -> hearthd -> native Core NATS -> Go Adapter SDK Session
    -> hearth-adapter-zwavejs -> Z-Wave JS server WebSocket
    -> Z-Wave JS UI -> controller -> existing Z-Wave network -> device
```

Use API schema version 29. It supplies endpoint state, string interview stages, endpoint labels, `node.poll_value`, `node.get_state`, and structured `node.set_value` results while avoiding dependence on newer unrelated fields. A server is compatible when its version frame contains `minSchemaVersion <= 29 <= maxSchemaVersion` and its `homeId` agrees with the start-listening snapshot.

The WebSocket boundary is preferred over Z-Wave JS UI's MQTT gateway because it provides:

- an explicit API schema compatibility range;
- correlated request/result messages;
- one `start_listening` operation that returns a complete snapshot and then enables events without an asynchronous gap in the server implementation;
- direct `node.poll_value` reads for fresh Command evidence;
- no second broker contract, retained-message policy, topic-mode setting, or MQTT API emulation.

The trade-off is that the embedded server has no authentication or TLS. V1 accepts only plain `ws://` on loopback or a trusted private network. Z-Wave JS UI, its WebSocket listener, Hearth NATS, and the Adapter must not be exposed to an untrusted network.

### V1 includes

- automatic discovery of every ready, always-listening, non-controller node endpoint with a valid Binary Switch or Multilevel Switch value pair;
- one Hearth Device per Z-Wave node, with root and endpoint-scoped Entities;
- `hearth.power/v1` for Binary Switch and Multilevel Switch power behavior;
- `hearth.brightness/v1` with native `maximum: 99` and `step: 1` for Multilevel Switch;
- startup and topology reconciliation against Adapter-owned mappings;
- ordinary Observations from snapshots and live `value added` or `value updated` events;
- observed Commands accepted after a successful upstream set and satisfied only by a fresh correlated `node.poll_value` result;
- Adapter health and explicit per-Entity availability;
- scripted WebSocket integration tests, sanitized server transcripts, and read-only then explicitly approved real-device validation.

### V1 defers

- inclusion, exclusion, SmartStart, failed-node replacement, interviews, healing or route rebuilding, firmware updates, controller backup and restore, and security-key management;
- locks, covers, thermostats, fans, scenes, central scenes, notifications, meters, sensors, configuration parameters, associations, and controller Events;
- Z-Wave Long Range-specific behavior beyond accepting a node ID that fits the protocol payload;
- automatic canonical-identity transfer after exclusion/re-inclusion or controller replacement;
- MQTT gateway, Socket.IO, REST, TLS, authentication, reverse-proxy configuration, and untrusted-network exposure;
- sleeping or wake-up-queued actuators, transitions, dimmer values above 99, configurable level scaling, and optimistic Command satisfaction;
- a generic WebSocket platform package, Core changes, new Entity types, persistence changes, or a frontend change.

## Solution space and trade-offs

| Option | Strengths | Weaknesses | Decision |
|---|---|---|---|
| Z-Wave JS server WebSocket | Versioned schema, correlated RPC, atomic snapshot/listen shape, direct poll, existing Go WebSocket dependency | No auth/TLS; coupled to server schema | **Choose** |
| Z-Wave JS UI MQTT gateway | Familiar broker operations; retained values; Value ID topics | Unversioned topic contract, several required UI settings, awkward request correlation, retained-state races | Reject for v1 |
| Z-Wave JS UI Socket.IO | Full UI-facing API and authentication | Internal UI protocol, expiring JWT, weaker snapshot/subscription contract | Reject |
| One configured node | Smallest implementation | Avoids proving discovery and mapping reconciliation, then requires redesign | Reject |

| Chose | Over | Because |
|---|---|---|
| Native brightness 0–99 | Percent 0–100 | Hearth's observed outcome requires exact equality; 101 canonical values cannot round-trip through 100 native levels |
| All eligible nodes | An allowlist | Existing complete inventory and stable network/node identity make automatic discovery useful without adding configuration state |
| Poll results for Command evidence | Matching `value updated` events | Z-Wave JS server enables post-set value-update emission by default, so an event can be an upstream echo rather than a device read |
| One Device per node | One Device per endpoint | Endpoints are independently addressable Entities but still belong to one physical Z-Wave node |

## Operator and configuration contract

Owner: `internal/app/zwavejs/config.go`.

```go
type Config struct {
    AdapterID string        `yaml:"adapter_id"`
    NATSURL   string        `yaml:"nats_url"`
    ZWaveJS   ZWaveJSConfig `yaml:"zwave_js"`
}

type ZWaveJSConfig struct {
    URL string `yaml:"url"`
}
```

Validation requires:

- `adapter_id` satisfies Hearth's subject-safe slug rule;
- `nats_url` satisfies existing NATS URL validation;
- `zwave_js.url` is an absolute lowercase `ws://` URL with an explicit host and port;
- the URL has no user info, query, or fragment and has either an empty path or `/`;
- `wss://`, credentials, tokens, and certificate fields are rejected in v1.

Example `configs/zwavejs.example.yaml`:

```yaml
adapter_id: zwavejs
nats_url: nats://127.0.0.1:4222
zwave_js:
  url: ws://127.0.0.1:3000
```

Z-Wave JS UI must have its Z-Wave JS server enabled and reachable only through a trusted network boundary. The Adapter never reads or receives S0/S2 keys. Z-Wave JS UI must already possess the controller's existing network cache and matching security keys; preserving those remains an operator responsibility.

Disabling optimistic value updates in Z-Wave JS UI is recommended to reduce transient upstream echoes, but it is not a correctness dependency: Command satisfaction uses only a correlated `node.poll_value` result. The Adapter never interprets target-value events as State.

## Upstream protocol contract

All DTOs are private to `internal/adapters/zwavejs`. JSON decoding ignores unknown fields but strictly validates every consumed field. The WebSocket read limit is 16 MiB; an oversized frame terminates that connection generation and reports the Adapter unhealthy.

### Handshake and result envelope

On connection the server sends:

```go
type serverVersion struct {
    Type             string `json:"type"`
    DriverVersion    string `json:"driverVersion"`
    ServerVersion    string `json:"serverVersion"`
    HomeID           *uint32 `json:"homeId"`
    MinSchemaVersion int    `json:"minSchemaVersion"`
    MaxSchemaVersion int    `json:"maxSchemaVersion"`
}

type requestEnvelope struct {
    MessageID string `json:"messageId"`
    Command   string `json:"command"`
}

type resultEnvelope struct {
    Type              string          `json:"type"`
    MessageID         string          `json:"messageId"`
    Success           bool            `json:"success"`
    Result            json.RawMessage `json:"result,omitempty"`
    ErrorCode         string          `json:"errorCode,omitempty"`
    ZWaveErrorCode    *int            `json:"zwaveErrorCode,omitempty"`
    ZWaveErrorCodeName string         `json:"zwaveErrorCodeName,omitempty"`
    Message           string          `json:"message,omitempty"`
}
```

The client requires the first frame to be `type:"version"`, requires a non-nil Home ID, verifies schema 29 compatibility, sends `initialize` with schema 29 and `additionalUserAgentComponents:{"hearth":"0.1.0"}`, waits for success, then sends `start_listening` and waits for the complete snapshot result.

Every request has a monotonically increasing decimal `messageId` unique within the connection. One reader goroutine owns frame reads, routes results to bounded one-shot waiters, and sends validated Events to the runtime coordinator. Unknown result IDs, duplicate results, malformed frames, unexpected binary frames, and event-queue overflow terminate the connection rather than silently losing protocol state.

### Snapshot and node/value shapes

```go
type networkSnapshot struct {
    State struct {
        Controller controllerState `json:"controller"`
        Nodes      []nodeState     `json:"nodes"`
    } `json:"state"`
}

type controllerState struct {
    HomeID *uint32 `json:"homeId"`
}

type nodeState struct {
    NodeID         int             `json:"nodeId"`
    Ready          bool            `json:"ready"`
    Status         int             `json:"status"`
    InterviewStage string          `json:"interviewStage"`
    IsController   bool            `json:"isControllerNode"`
    IsListening    bool            `json:"isListening"`
    Name           string          `json:"name"`
    Location       string          `json:"location"`
    Label          string          `json:"label"`
    ManufacturerID *int            `json:"manufacturerId"`
    ProductType    *int            `json:"productType"`
    ProductID      *int            `json:"productId"`
    Endpoints      []endpointState `json:"endpoints"`
    Values         []valueState    `json:"values"`
}

type endpointState struct {
    Index         int    `json:"index"`
    EndpointLabel string `json:"endpointLabel"`
}

type valueID struct {
    CommandClass int    `json:"commandClass"`
    Endpoint     int    `json:"endpoint,omitempty"`
    Property     string `json:"property"`
}

type valueMetadata struct {
    Type      string   `json:"type"`
    Readable  bool     `json:"readable"`
    Writeable bool     `json:"writeable"`
    Min       *float64 `json:"min,omitempty"`
    Max       *float64 `json:"max,omitempty"`
}

type valueState struct {
    valueID
    Metadata valueMetadata  `json:"metadata"`
    Value    json.RawMessage `json:"value,omitempty"`
}
```

`propertyKey`, numeric property names, unknown metadata types, malformed metadata, non-finite numbers, duplicate Value IDs, duplicate endpoint indexes, and ambiguous current/target pairs isolate the affected endpoint capability. Binary planning requires metadata type `boolean`; multilevel planning requires metadata type `number`, including when a target has no current value from which a type could be inferred. Invalid candidates do not discard valid sibling endpoints. Unknown Command Classes and properties are ignored.

### Consumed commands

```go
type initializeRequest struct {
    requestEnvelope
    SchemaVersion                 int               `json:"schemaVersion"`
    AdditionalUserAgentComponents map[string]string `json:"additionalUserAgentComponents"`
}

type nodeSetValueRequest struct {
    requestEnvelope
    NodeID  int             `json:"nodeId"`
    ValueID valueID         `json:"valueId"`
    Value   json.RawMessage `json:"value"`
}

type nodeSetValueResult struct {
    Result struct {
        Status string `json:"status"`
    } `json:"result"`
}

type nodePollValueRequest struct {
    requestEnvelope
    NodeID  int     `json:"nodeId"`
    ValueID valueID `json:"valueId"`
}

type nodePollValueResult struct {
    Value json.RawMessage `json:"value,omitempty"`
}

type nodeGetStateRequest struct {
    requestEnvelope
    NodeID int `json:"nodeId"`
}

type nodeGetStateResult struct {
    State nodeState `json:"state"`
}
```

Successful set statuses are the schema-29 encodings of `Working`, `Success`, and `SuccessUnsupervised`. Every other status or a successful envelope without a recognized status is an upstream rejection. Exact fixture values must be captured before implementation locks the string/number decoder; the decoder may accept the documented enum representation only, never infer success from a non-empty result.

`node.poll_value` returns the freshly read value in its correlated result. It is the only upstream report eligible for Command-linked evidence. `node.get_state` refreshes one complete node plan after topology or metadata events; it is not a physical-value refresh and does not satisfy a Command.

### Consumed events

```go
type serverEvent struct {
    Type  string `json:"type"`
    Event struct {
        Source string          `json:"source"`
        Event  string          `json:"event"`
        NodeID int             `json:"nodeId,omitempty"`
        Node   json.RawMessage `json:"node,omitempty"`
        State  json.RawMessage `json:"nodeState,omitempty"`
        Args   json.RawMessage `json:"args,omitempty"`
    } `json:"event"`
}

type valueEventArgs struct {
    valueID
    NewValue json.RawMessage `json:"newValue"`
}
```

V1 consumes controller `node added` and `node removed`, node `ready`, `interview completed`, `value added`, `value updated`, `value removed`, `metadata updated`, `wake up`, `sleep`, `alive`, and `dead`. Inventory-shape events trigger a correlated `node.get_state` and atomic re-plan. `sleep` immediately invalidates Command routes because v1 does not permit wake-up-queued writes; `wake up` refreshes node state before routes can return. Value updates are projected only after their Value ID resolves against the active immutable route snapshot. Unknown Events are ignored with bounded diagnostics.

## Discovery, identity, and reconciliation

### Network identity

The normalized network identity is the lowercase, zero-padded eight-digit hexadecimal Home ID. The version frame and snapshot controller Home ID must agree.

At each Hearth runtime the Adapter first pages through `Session.ListOwnedMappings`. Existing Z-Wave Binding keys owned by this Adapter must all carry one network identity. If that identity differs from the connected Home ID, the Adapter reports `adapter.hearth-adapter-zwavejs.network_identity_mismatch`, registers nothing, publishes no Observations, and waits for the operator to restore the intended Z-Wave JS UI target or configure a new `adapter_id` for the other network.

This prevents accidentally repointing one Adapter instance at another controller from silently reusing node-number-shaped identities.

### Binding and Entity identity

For Home ID `1a2b3c4d`, node 23, root endpoint 0, and endpoint 1:

| Resource | Root | Endpoint 1 |
|---|---|---|
| Binding key | `zwave-1a2b3c4d-node-23` | same Binding |
| Device external ID | `1a2b3c4d/node/23` | same Device |
| Power Entity key | `power` | `power-ep1` |
| Brightness Entity key | `brightness` | `brightness-ep1` |
| Power external ID | `1a2b3c4d/node/23/ep/0/power` | `1a2b3c4d/node/23/ep/1/power` |
| Brightness external ID | `1a2b3c4d/node/23/ep/0/brightness` | `1a2b3c4d/node/23/ep/1/brightness` |

Node names, locations, labels, manufacturer/product identifiers, driver versions, and controller hardware identifiers never enter Binding keys or external IDs. They are mutable or non-unique metadata.

Exclusion/re-inclusion normally assigns a different node ID. When it does, v1 creates a new Binding and canonical Device; the old Device remains unavailable. A controller may later reuse a removed node's ID, and Z-Wave exposes no universal immutable physical-device identifier with which this stateless Adapter can distinguish an offline same-model replacement. V1 therefore defines identity as the Home-ID/node-ID network slot: reuse of the same slot reuses canonical identity. Before removing a node, the operator must disable its Hearth Entities; reusing that slot safely requires explicit reconciliation support, which is deferred. Product fingerprints are retained only as diagnostics and are never used for automatic rebinding.

### Eligibility and planning

A node is eligible when it is not the controller, `isListening` is true, `ready` is true, `interviewStage` is `Complete`, and at least one endpoint produces a valid plan. V1 excludes sleeping and frequently-listening actuators so Z-Wave JS cannot place a Hearth write into a wake-up queue that may execute after the Command deadline. A `sleep` Event makes a previously planned node temporarily unroutable until a `wake up` Event and fresh `node.get_state` prove it eligible again.

Command Class constants are:

```go
const (
    commandClassBinarySwitch     = 37
    commandClassMultilevelSwitch = 38
)
```

For each endpoint:

1. A Binary Switch power plan requires one readable boolean `currentValue` and one writeable boolean `targetValue` for Command Class 37.
2. A Multilevel Switch brightness plan requires one readable numeric `currentValue` with bounds exactly 0–99 and one writeable numeric `targetValue` for Command Class 38. It registers `hearth.brightness/v1` support `{"state":{"maximum":99},"operations":{"set":{"step":1}}}`.
3. A Multilevel Switch also provides power when no valid Binary Switch pair exists on that endpoint. State is `currentValue > 0`; off writes numeric `0`; on writes numeric `255`, the Command Class restore-previous-level value. A fresh polled `currentValue` greater than zero satisfies on, while exactly zero satisfies off.
4. If both Command Classes are valid, Binary Switch owns power and Multilevel Switch owns brightness.
5. Basic Command Class, `targetValue` reports, duration, restore-previous values observed as current State, non-integral levels, and values outside 0–99 produce no State.

One registration contains all eligible endpoint Entities for the node. Registration order is endpoint number ascending, with power before brightness per endpoint. More than 64 planned Entities makes that node unsupported rather than splitting one physical node across Devices.

Device kind is `light` when any brightness Entity exists and `relay` otherwise. Device name is the trimmed node name, then trimmed device label, then `Z-Wave Node <nodeID>`. Root Entity names are `Power` and `Brightness`; endpoint Entities prefix a valid endpoint label, falling back to `Endpoint <N>`. Names longer than Hearth's bound are rejected, never truncated.

### Reconciliation

The start-listening snapshot is the complete inventory authority for one connection generation. The Adapter:

1. loads all owned mappings;
2. validates network identity;
3. plans and registers every eligible node;
4. builds one immutable route snapshot keyed by canonical Entity ID;
5. marks mappings for missing nodes, incomplete nodes, and removed capabilities unavailable;
6. reports healthy and waits for acknowledgement;
7. publishes explicit current availability;
8. publishes valid snapshot Observations in registration order;
9. activates live Events and Commands already buffered by the client/runtime coordinator.

The server's `start_listening` implementation sends the state result and enables Events synchronously without an intervening await. The Adapter still queues frames received while reconciliation is in progress. A bounded queue overflow fails the connection; no Event is silently dropped.

A later node-added, ready, interview-completed, value-added, value-removed, or metadata-updated Event causes one `node.get_state` refresh and replaces only that node's routes. If the refreshed node remains eligible, routes change only after successful registration. If it has no eligible plan, the Adapter immediately invalidates its prior routes and reports every owned mapping for that node unavailable without attempting an invalid zero-Entity registration. A node-removed or sleep Event also immediately invalidates routes and reports all known Entities unavailable. Omission never deletes a Hearth Device, Entity, State, history, or Binding.

## State projection

Snapshot `currentValue` fields and live `value added` or `value updated` events produce ordinary typed Observations. Each frame receives one Adapter-owned UTC receive time. Z-Wave JS server supplies no source timestamp used by this schema, so `SourceUpdatedAt` is nil.

- Binary Switch `currentValue` JSON `true`/`false` maps directly to Hearth power.
- Multilevel Switch integral `currentValue` from 0 through 99 maps directly to Hearth brightness.
- When Multilevel Switch owns power, the same current-value report maps zero to false and 1–99 to true.
- A frame that maps to both power and brightness publishes power first.
- Numeric strings, fractions, non-finite values, 255 as current State, null, and values outside the planned support are diagnosed and skipped per Entity.
- `targetValue` is never State and never satisfies a Command.
- The Adapter keeps no cross-frame State assembly cache and never infers brightness from Binary Switch power.

An upstream post-set `value updated` event may be optimistic because Z-Wave JS server enables value-update emission after set by default. Such an event may be an ordinary Observation, consistent with Hearth's rule that an Observation is a report rather than physical proof, but it is never Command-linked. The subsequent correlated poll result is the Command evidence.

## Adapter health and Entity availability

The Adapter remains `unknown` until it has connected, negotiated schema 29, validated one Home ID, received and reconciled the complete snapshot, and installed routes. An empty network with a valid complete snapshot is healthy.

Adapter unhealthy reasons:

```text
hearth.external_system_unavailable
adapter.hearth-adapter-zwavejs.incompatible_protocol
adapter.hearth-adapter-zwavejs.invalid_snapshot
adapter.hearth-adapter-zwavejs.network_identity_mismatch
```

Connection refusal, WebSocket close including server code 1013 while the driver is not ready, read/write failure, and request timeout use `hearth.external_system_unavailable`. A schema range excluding 29 uses `incompatible_protocol`. Malformed version/snapshot data uses `invalid_snapshot`. An unhealthy transition invalidates routes before reporting health and clears active connection work. Static configuration failures and terminal SDK fencing terminate the process; upstream failures reconnect with bounded exponential backoff and jitter.

Node status values follow Z-Wave JS `Unknown`, `Asleep`, `Awake`, `Dead`, and `Alive` semantics. For each current Entity:

- ready plus `Awake` or `Alive` reports available;
- `Dead` reports unavailable with `adapter.hearth-adapter-zwavejs.node_dead`;
- `Asleep` invalidates v1 Command routes and reports unavailable with `adapter.hearth-adapter-zwavejs.node_asleep`;
- not ready or an incomplete interview reports unavailable with `adapter.hearth-adapter-zwavejs.node_not_ready`;
- a previously known node absent from the snapshot reports unavailable with `adapter.hearth-adapter-zwavejs.node_missing`;
- a previously known Entity absent from the current plan reports unavailable with `adapter.hearth-adapter-zwavejs.capability_missing`;
- a new node with `Unknown` status remains unknown; a previously assessed node returning to `Unknown` reports unavailable with `adapter.hearth-adapter-zwavejs.node_unknown`.

Availability itself is advisory and never gates Command dispatch. Independently, v1 route eligibility excludes a node while it is sleeping because the upstream may defer a write beyond Hearth's deadline; recovery requires `wake up` plus a fresh eligible node state. Recovery reports healthy first, then sends fresh availability batches of at most 256 in owned-mapping order.

## Command runtime and correlation

A private serial runtime coordinator owns connection generations, immutable routes, per-node FIFO queues, active attempts, protocol request completions, buffered Events, and deadline timers. Blocking SDK calls and WebSocket requests run outside the coordinator loop in tracked goroutines. Queue time consumes the existing absolute Hearth deadline. Different nodes may execute concurrently; one node executes one Command at a time. Dispatch occurs only through an eligible always-listening route and only while the Command deadline remains open. Once a SetValue request has been written, Z-Wave JS exposes no cancellation primitive for the remote radio transaction: the deadline bounds Hearth acceptance and outcome, not proof that a timed-out physical effect can never occur later. A timed-out SetValue request closes that connection generation and is diagnosed as an ambiguous upstream attempt; this residual limitation is explicit rather than treated as successful dispatch.

For power or brightness `set`:

1. Resolve the canonical Entity against the current connection generation and route revision.
2. Decode parameters with the generated `powerv1` or `brightnessv1` facade.
3. Send correlated `node.set_value` to the planned target Value ID. Binary power sends a boolean; multilevel brightness sends 0–99; multilevel power sends 0 for off or 255 for on.
4. A transport failure or missing node invalidates the generation and rejects the unaccepted Command as unavailable. A deterministic upstream value rejection uses `Responder.Reject`.
5. Require a recognized successful SetValue status, then call `Responder.Accept` and retain its `CommandEvidence` beyond handler return.
6. Immediately send correlated `node.poll_value` for the planned current Value ID.
7. Decode every successful poll result through the same State translator and publish it through `CommandEvidence.PublishObservation`, even when it does not yet match. A matching polled value satisfies the Command in Core.
8. If the first poll does not match, a later relevant `value updated` event is only a wake hint: coalesce hints and issue another poll no more often than once per 250 ms. The Event itself remains ordinary evidence. Stop after a matching linked Observation, route/generation invalidation, or the absolute deadline.

The Adapter never claims that SetValue acceptance, supervision success, a target value, or an emitted post-set update proves the physical result. A SetValue status `Working` may therefore remain active until polling reaches the requested value or Core's ten-second Entity-type deadline expires.

A route change, WebSocket loss, or shutdown rejects only attempts not yet accepted. Accepted Commands receive no second response and naturally time out if no linked poll result can be published. A poll publication with an ambiguous JetStream acknowledgement is retried by the SDK using its original Observation identity.

## Module interfaces and implementation shape

### Adapter module

Owner: `internal/adapters/zwavejs/adapter.go`.

```go
type Session interface {
    ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
    Register(context.Context, adapter.Registration) (adapter.Binding, error)
    SetHealth(context.Context, adapter.HealthReport) error
    ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
    PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Config struct {
    URL string
}

func New(session Session, config Config, logger *slog.Logger) (*Adapter, error)
func (zwave *Adapter) Run(context.Context) error
func (zwave *Adapter) HandleCommand(context.Context, adapter.Command, adapter.Responder) error
```

No Entity Event method is included because v1 plans no event-source Entity. The consuming package defines this minimal SDK seam.

### Private WebSocket seam

Owner: `internal/adapters/zwavejs/client.go`.

```go
type zwaveDialer interface {
    Dial(context.Context, string, int, map[string]string) (zwaveConnection, error)
}

type zwaveConnection interface {
    StartListening(context.Context) (serverVersion, networkSnapshot, error)
    SetValue(context.Context, int, valueID, json.RawMessage) (setValueStatus, error)
    PollValue(context.Context, int, valueID) (json.RawMessage, time.Time, error)
    GetNodeState(context.Context, int) (nodeState, error)
    Events() <-chan receivedEvent
    Lost() <-chan error
    Close()
}

type receivedEvent struct {
    Event      serverEvent
    ReceivedAt time.Time
}
```

The concrete implementation uses the existing `github.com/coder/websocket` dependency and `wsjson`; no Socket.IO or vendor Go client dependency is added.

### Typed Entity plans

Owner: `internal/adapters/zwavejs/entity_plan.go`.

```go
type entityPlan struct {
    Key            string
    ExternalID     string
    Name           string
    Endpoint       int
    CurrentValueID valueID
    TargetValueID  valueID
    Descriptor     adapter.EntityDescriptor
    DecodeState    func(json.RawMessage) (json.RawMessage, error)
    EncodeSet      func(adapter.Command) (json.RawMessage, error)
    Matches        func(parameters json.RawMessage, state json.RawMessage) bool
}

type routeSnapshot struct {
    Generation uint64
    Revision   uint64
    ByEntityID map[string]entityRoute
    ByValueID  map[upstreamValueKey][]entityRoute
}
```

Concrete plan construction uses generated `sdk/adapter/powerv1` and `sdk/adapter/brightnessv1` descriptors, observations, and command handlers. Vendor JSON does not cross this package boundary.

## Project layout

```text
cmd/
└── hearth-adapter-zwavejs/
    ├── main.go                         # new — process flags, logging, signals, config load, app run
    └── main_test.go                    # new — executable smoke and invalid-log-flag tests
configs/
└── zwavejs.example.yaml                # new — loopback-only example
internal/
├── adapters/
│   └── zwavejs/
│       ├── adapter.go                  # new — Adapter lifecycle, SDK seam, reconnect policy
│       ├── availability.go             # new — node status and reconciliation reports
│       ├── client.go                   # new — correlated schema-29 WebSocket client
│       ├── client_test.go              # new — handshake, routing, limits, malformed frames, disconnects
│       ├── command.go                  # new — typed command translation and poll evidence
│       ├── command_test.go             # new — set status, FIFO, poll matching, deadlines, route invalidation
│       ├── discovery.go                # new — snapshot/node planning, identity, mapping reconciliation
│       ├── discovery_test.go           # new — nodes, endpoints, ambiguity, removals, names, network identity
│       ├── entity_plan.go              # new — private plans and immutable route snapshots
│       ├── logging.go                  # new — safe event/error classifications
│       ├── observation.go              # new — current-value translation and typed Observations
│       ├── observation_test.go         # new — binary, 0–99, invalid values, derived power
│       ├── runtime.go                  # new — generations, routes, per-node queues, event/poll coordination
│       ├── runtime_test.go             # new — reconnect and concurrency state-machine tests
│       ├── testhelp_test.go            # new — fake Session, connection, and clock
│       └── testdata/
│           ├── switch-session.jsonl    # new — sanitized server transcript
│           └── dimmer-session.jsonl    # new — sanitized server transcript
└── app/
    └── zwavejs/
        ├── config.go                   # new — strict YAML and trusted ws:// validation
        ├── config_test.go              # new — config validation table
        ├── run.go                      # new — SDK and Adapter assembly/supervision
        └── run_integration_test.go     # new — scripted WS + real NATS/Core public-boundary test
docs/
└── architecture.md                     # modify after approval — accepted Z-Wave JS Adapter constraint
specs/
└── zwave-js-adapter.md                 # new — implementation contract
README.md                               # modify — setup, safety boundary, supported capabilities, diagnostics
.ko.yaml                                # modify — release image build
.github/workflows/release.yml           # modify — publish adapter image
mise.toml                               # modify — local ko build and bounded Z-Wave parser fuzz tasks
```

No changes are expected in `contracts/v1`, `entitytypes`, `sdk/adapter`, `internal/modules/devices`, database migrations, `docker-compose.yml`, or `web`.

## Deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
|---|---|---:|---|---|---|
| D1 | Schema-29 client and strict config can connect, negotiate, atomically receive a snapshot, route concurrent results, and fail safely | L | `internal/adapters/zwavejs/client.go`, `internal/app/zwavejs/config.go`, tests | — | A1, A2 |
| D2 | Complete snapshot planning preserves network/node/endpoint identity and registers all valid power/brightness Entities | L | `discovery.go`, `entity_plan.go`, `observation.go`, tests/fixtures | D1 | A3, A4, A5 |
| D3 | Runtime reconciliation, health, availability, reconnect, and topology changes preserve owned mappings without stale routes | L | `adapter.go`, `availability.go`, `runtime.go`, tests | D1, D2 | A6, A7, A8 |
| D4 | Per-node serialized power/brightness Commands use correlated SetValue plus fresh poll evidence under deadline and disconnect races | XL | `command.go`, `runtime.go`, tests | D2, D3 | A9, A10, A11 |
| D5 | Process assembly, packaging, operator documentation, and repository tasks ship the Adapter consistently | M | `cmd/hearth-adapter-zwavejs`, `internal/app/zwavejs`, config, README, `.ko.yaml`, release workflow, `mise.toml` | D1–D4 | A12, A13 |
| D6 | Sanitized real network evidence verifies snapshot/event shapes and one approved switch and dimmer round trip without modifying network membership | M | `internal/adapters/zwavejs/testdata`, spec evidence section | D1–D5 | A14, A15 |

## Acceptance criteria

- [ ] **A1:** A compatible version frame negotiates schema 29, `initialize` and `start_listening` results correlate under concurrent requests, and malformed, duplicate, unknown, oversized, or binary frames cannot silently corrupt routing.
- [ ] **A2:** Config accepts only an explicit trusted `ws://host:port` endpoint and rejects credentials, TLS, path variants, queries, fragments, missing ports, and malformed NATS/Adapter values.
- [ ] **A3:** A complete mixed-node snapshot registers every and only valid always-listening Binary Switch and Multilevel Switch endpoint, with deterministic Device kind, ordering, names, keys, external IDs, metadata-type validation, and typed support; sleeping candidates are excluded.
- [ ] **A4:** Binary power, native 0–99 brightness, and Multilevel-derived power Observations decode exactly; malformed one-Entity values never suppress valid siblings.
- [ ] **A5:** Restart and rename preserve canonical IDs; a different Home ID under an existing Adapter ID is rejected as a network identity mismatch; a changed node ID creates a new Binding, while same-slot node-ID reuse follows the documented disable-and-reconcile operator safety limitation.
- [ ] **A6:** Startup lists all owned mappings, marks missing/incomplete/capability-removed resources unavailable, reports healthy before fresh availability, and publishes snapshot State only after registration.
- [ ] **A7:** Node add/remove/ready/interview/value/metadata/status Events atomically replace affected routes, invalidate zero-Entity and sleeping plans without registration, recover on the exact `wake up` event, isolate malformed nodes, and never dispatch through a stale generation or revision.
- [ ] **A8:** Connection loss invalidates routes, reports unhealthy, clears in-flight unaccepted work, reconnects with bounded backoff, and requires full fresh reconciliation before recovery reports.
- [ ] **A9:** Binary and multilevel power and brightness Commands publish the exact planned Value ID/value, accept only a documented successful SetValue status, and never use target or value-update events as linked evidence.
- [ ] **A10:** A fresh correlated poll result publishes through the accepted Command's evidence capability; matching State satisfies, mismatching State does not, and later event-triggered polls may satisfy before the absolute deadline.
- [ ] **A11:** Commands are FIFO per node, concurrent across nodes, consume deadline while queued, never dispatch to a sleeping route, and handle set/poll/result/disconnect/route-change/deadline races without duplicate responses, leaked goroutines, or ordinary/linked double publication; a timed-out in-flight SetValue is recorded as ambiguous rather than accepted.
- [ ] **A12:** A process integration test crosses scripted WebSocket, Adapter, SDK NATS, Core registration/Observation/Command paths, and SQLite-backed reads for one switch and one dimmer.
- [ ] **A13:** The executable, example config, release image lists, README, format, generation, lint, tidy, race tests, and vet pass through `mise run validate`.
- [ ] **A14:** A sanitized read-only capture from the existing network confirms Z-Wave JS UI/server versions, schema range, Home ID shape, node/endpoint/value DTOs, SetValue status encoding, poll result encoding, and event argument shapes without recording DSKs, security keys, household names, or real Home ID.
- [ ] **A15:** After explicit operator approval for each physical actuation, one real binary switch and one real dimmer complete off/on and 0/mid/99 Commands using poll-linked evidence; no inclusion, exclusion, interview, association, configuration, healing, or security operation is sent.

## Test strategy

| Layer | What | How |
|---|---|---|
| Unit | Config, IDs, plans, metadata, State decoders, set encoders, status mapping | Table tests plus property tests for node/endpoint ordering and level round trips |
| Protocol | Handshake, schema negotiation, request correlation, event routing, read limits, closes | Scripted `coder/websocket` server and JSONL transcripts |
| Runtime | Generations, route revisions, per-node FIFO, deadline, reconnect, poll hints, health/availability order | Deterministic fake connection, fake Session, fake clock; race detector |
| Integration | Public registration, Observation, Command, and read behavior | Scripted WebSocket server + embedded NATS/Core + SQLite repository |
| Fuzz | Snapshot/event decoding and result-envelope classification | Bounded `FuzzZWaveJSSnapshot` and `FuzzZWaveJSEvent` campaigns in `mise.toml` |
| Real device | Existing-network preservation and physical round trip | Read-only capture first; announce exact Commands and obtain explicit approval before actuation |

Go tests should target fault detection: mutate Command Class/property matching, endpoint identity, Home ID comparison, 0/99/255 boundaries, SetValue status acceptance, poll freshness, route generation checks, and health-before-availability ordering. Green helper tests alone do not establish the public boundary; A12 must assert through Core-visible Device/Entity/State/Command reads.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Actual server event or SetValue enum encoding differs from inferred DTOs | Medium | High | D6 captures sanitized real frames before decoder lock-in; scripted tests use captured shapes |
| `emitValueUpdateAfterSetValue` creates optimistic reports | High | Medium | Never link Events to Commands; poll current Value ID and use the correlated result |
| Full snapshot exceeds the fixed frame limit on a large network | Low | High | Measure target snapshot, keep a documented 16 MiB bound, fail/reconnect rather than truncate |
| Node ID changes or is reused after re-inclusion | Medium | High | New Binding when the ID changes; define same-ID identity as a network slot; require disable-before-removal and defer explicit reconciliation |
| Same Adapter ID points at another controller | Low | High | Home ID in every Binding plus owned-mapping mismatch gate |
| Polling adds Z-Wave mesh traffic or misses slow transitions | Medium | Medium | Serialize per node, poll immediately and only on coalesced hints, cap at one per 250 ms, obey ten-second deadline |
| A SetValue already handed to the server may complete after local timeout | Low for always-listening nodes | High | Exclude sleeping nodes, close the failed generation, classify the attempt as ambiguous, and never claim the deadline proves cancellation of radio work |
| WebSocket has no auth/TLS | High | High on untrusted network | Accept plain `ws://` only and document loopback/trusted-private deployment as mandatory |
| Node/endpoint metadata is incomplete or vendor-specific | Medium | Low | Require only exact standard CC/value pairs; isolate malformed capabilities; use deterministic fallback names |

## Success metrics

- Every eligible switch/dimmer on the existing network appears once with stable canonical identity across Adapter and Z-Wave JS UI restarts.
- A switch and dimmer can be observed and controlled with Command outcomes based only on fresh poll evidence.
- No Z-Wave identity, DTO, Command Class, or lifecycle concept is added to Core, wire contracts, Entity types, or persistence.
- The Adapter performs no network-management operation and does not require re-inclusion.

## External references

- Z-Wave JS server API and schema negotiation: <https://github.com/zwave-js/zwave-js-server/blob/3.10.1/README.md>
- Z-Wave JS server API schema history: <https://github.com/zwave-js/zwave-js-server/blob/3.10.1/API_SCHEMA.md>
- Z-Wave JS server snapshot shapes: <https://github.com/zwave-js/zwave-js-server/blob/3.10.1/src/lib/state.ts>
- Z-Wave JS server request handling: <https://github.com/zwave-js/zwave-js-server/blob/3.10.1/src/lib/server.ts>
- Binary Switch Command Class: <https://zwave-js.github.io/node-zwave-js/#/api/CCs/BinarySwitch>
- Multilevel Switch Command Class: <https://zwave-js.github.io/node-zwave-js/#/api/CCs/MultilevelSwitch>
- Z-Wave JS UI setup: <https://zwave-js.github.io/zwave-js-ui/#/usage/setup>

## Open questions before implementation

- The target network's exact Z-Wave JS UI version, bundled server version, switch model, dimmer model, endpoint layout, and frame size must be captured in D6. This does not change the architecture unless schema 29 is unavailable.
- The exact JSON representation of schema-29 SetValue statuses must be fixed from authoritative generated output or capture before production decoding; this is owned by D1, not left to runtime guessing.
- Whether ordinary post-set Events expose a reliable source discriminator should be investigated from captured data. V1 remains correct without one because only poll results are linked.
