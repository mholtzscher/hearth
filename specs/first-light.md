# First Light — Implementation Spec

- **Status:** Implemented
- **Approved by:** Michael
- **Approved:** 2026-08-19
- **Completed:** 2026-08-23
- **Amended:** 2026-08-22 — unified Entity support, generated typed SDK facades, and added built-in brightness/v1 as generator/catalog validation; 2026-08-20 — generalized Entity values and Command parameters behind a closed Entity-type catalog, renamed the product Hearth, persisted Entity names, and defined permanent registration rejections and subscribe-first snapshot reconciliation
- **Type:** Feature plan
- **Effort:** XL (relative estimate only)

## Problem statement

Hearth needs a useful first vertical slice for a technical self-hoster migrating one household from Home Assistant. The household must remain operable while the project proves NATS through process isolation, durable Observations, recovery, and explicit Command semantics, without making Home Assistant permanent architecture.

## Proposed solution

Build three Go processes: the Hearth core daemon `hearthd`, a disposable Home Assistant migration adapter, and a fault-injecting simulator. A thin, stateless Go SDK hides NATS protocol mechanics; authoritative JSON Schemas support non-Go adapters.

Adapters register configured Devices and Entities over Core NATS request/reply, publish Observations durably to JetStream, and serve ephemeral Commands over Core NATS request/reply. Entity descriptors reference a core-owned, versioned Entity-type catalog; generic JSON values and Command parameters pass through the transport and persistence plumbing, while the catalog validates their semantics. The built-in catalog contains `hearth.power/v1` plus `hearth.brightness/v1` as generator/catalog validation; the first-light adapter and HTTP scenario continue to use only power. The core projects canonical State and records every dispatched Command attempt and outcome in SQLite, then exposes an Echo/Huma HTTP interface on its configured address. Command delivery and outcome waits remain synchronous and ephemeral; records never cause replay or redispatch. Commands for the same Entity may overlap and are correlated independently by Command ID.

## Scope

### In scope

- One configured Device of kind `light`
- One Entity of type `hearth.power/v1`
- Boolean State and operation `set`, validated by the closed first-light Entity-type catalog
- Core HTTP read and synchronous Command endpoints
- Disposable Home Assistant migration adapter
- Simulator implementing the same adapter contract
- Durable JetStream Observation intake
- SQLite registration, current State, Observation deduplication, and Command attempt history
- Runtime JSON Schema validation
- Causal IDs and W3C trace-context propagation
- Explicit liveness and readiness endpoints

### Non-goals

- Light behavior beyond boolean power `set`
- Discovery, approval, availability, heartbeats, automations, scheduling, or UI
- Adapter manifests, feature negotiation, runtime Entity-type registration or extension loading, checkpoints, or a vendor framework in the SDK
- Durable Command delivery/recovery or core-managed serialization, queuing, and supersession
- HTTP authentication
- Permanent Home Assistant support

## Key trade-offs

- JetStream durably recovers Observation evidence; SQLite owns transactional canonical State and non-replayable Command history.
- State follows core receive order while source times remain diagnostic metadata; Command success requires explicit linked evidence from the capability returned by acceptance rather than upstream acceptance alone.
- A thin SDK centralizes protocol mechanics while vendor behavior stays in adapters; the first slice remains one `devices` module.
- Generic JSON State values and Command parameters avoid boolean-specific transport and persistence, at the cost of moving semantic validation from structural wire schemas into a core-owned Entity-type catalog.
- The catalog is deliberately closed and concrete in this slice: it establishes the seam for later built-in or explicitly installed definitions without implementing runtime extension loading.
- Commands overlap independently by ID, accepting interleaved outcomes and immediately superseded successes.

## Runtime topology

```text
HTTP client -> hearthd -> Core NATS request/reply -> adapter -> external system
                    ^                              |
                    |---- JetStream Observation ---|
                    |
                  SQLite
```

The simulator replaces the Home Assistant adapter during fault scenarios. Local development runs NATS and all Go processes natively through devenv.

## Dependencies

The Go module path is `github.com/mholtzscher/hearth`.

| Concern | Dependency |
| --- | --- |
| NATS and JetStream | `github.com/nats-io/nats.go` |
| HTTP router | Echo v5 |
| Typed HTTP/OpenAPI | Huma v2 with Echo adapter |
| SQLite driver | `modernc.org/sqlite` through `database/sql` |
| Migrations | `github.com/pressly/goose/v3` |
| Query generation | sqlc |
| Home Assistant WebSocket | `github.com/coder/websocket` |
| JSON Schema | `github.com/santhosh-tekuri/jsonschema/v6` |
| YAML | `gopkg.in/yaml.v3` |
| UUIDv7 | `github.com/google/uuid` |
| Trace propagation | `go.opentelemetry.io/otel/propagation` |

Dependency versions are pinned by `go.mod`, `go.sum`, and `devenv.lock` during implementation.

## Types

### Domain types

Owner: `internal/modules/devices/model.go`

```go
package devices

import (
    "encoding/json"
    "time"
)

type DeviceID string
type EntityID string
type ObservationID string
type CommandID string
type CorrelationID string

type DeviceKind string
const DeviceKindLight DeviceKind = "light"

type EntityTypeID string
const (
    EntityTypePowerV1      EntityTypeID = "hearth.power/v1"
    EntityTypeBrightnessV1 EntityTypeID = "hearth.brightness/v1"
)

type OperationName string
const OperationNameSet OperationName = "set"

type EntitySupport json.RawMessage
type Value json.RawMessage
type CommandParameters json.RawMessage

type Device struct {
    ID   DeviceID
    Kind DeviceKind
    Name string
}

type Entity struct {
    ID        EntityID
    DeviceID  DeviceID
    AdapterID string
    Name      string
    TypeID    EntityTypeID
    Support   EntitySupport
}

type State struct {
    EntityID          EntityID
    Value             Value
    ObservationID     ObservationID
    AdapterReceivedAt time.Time
    SourceUpdatedAt   *time.Time
    ObservedAt        time.Time
    ReceiveOrder      int64
}

type EntityView struct {
    Entity Entity
    State  *State
}

type ObservationDisposition string
const (
    DispositionApplied   ObservationDisposition = "applied"
    DispositionUnchanged ObservationDisposition = "unchanged"
    DispositionRejected  ObservationDisposition = "rejected"
    DispositionDuplicate ObservationDisposition = "duplicate" // processing result; no second observation row
)

type ObservationRejection string
const (
    RejectionUnknownEntity ObservationRejection = "unknown_entity"
    RejectionWrongAdapter  ObservationRejection = "wrong_adapter"
    RejectionInvalidValue  ObservationRejection = "invalid_value"
)

type ProjectionResult struct {
    Disposition      ObservationDisposition
    State            *State
    Rejection        *ObservationRejection
    SatisfiedCommand *CommandResult
}

type CommandStatus string
const (
    CommandStatusRequested          CommandStatus = "requested"
    CommandStatusAccepted           CommandStatus = "accepted"
    CommandStatusSatisfied          CommandStatus = "satisfied"
    CommandStatusRejected           CommandStatus = "rejected"
    CommandStatusAdapterUnavailable CommandStatus = "adapter_unavailable"
    CommandStatusOutcomeTimeout     CommandStatus = "outcome_timeout"
    CommandStatusInternalFailure    CommandStatus = "internal_failure"
    CommandStatusInterrupted        CommandStatus = "interrupted"
)

type CommandFailureCode string
const (
    CommandFailureAdapterUnavailable CommandFailureCode = "adapter_unavailable"
    CommandFailureUpstreamRejected   CommandFailureCode = "upstream_rejected"
    CommandFailureOutcomeTimeout     CommandFailureCode = "outcome_timeout"
    CommandFailureInternalError      CommandFailureCode = "internal_error"
    CommandFailureCoreRestarted      CommandFailureCode = "core_restarted"
)

type CommandRecord struct {
    ID                   CommandID
    EntityID             EntityID
    AdapterID            string
    OperationName        OperationName
    Parameters           CommandParameters
    CorrelationID        CorrelationID
    Status               CommandStatus
    RequestedAt          time.Time
    DeadlineAt           time.Time
    AcceptedAt           *time.Time
    CompletedAt          *time.Time
    OutcomeObservationID *ObservationID
    FailureCode          *CommandFailureCode
}

type CommandCompletion struct {
    ID          CommandID
    Status      CommandStatus // rejected, adapter_unavailable, outcome_timeout, or internal_failure
    CompletedAt time.Time
    FailureCode CommandFailureCode
}

type CommandResult struct {
    CommandID     CommandID
    ObservationID ObservationID
    Value         Value
}
```

`EntitySupport`, `Value`, and `CommandParameters` each contain one complete valid JSON value; Command parameters are objects. They are normalized and copied defensively at module edges. The module never determines State equality or Command satisfaction by comparing encoded bytes; it delegates both to the Entity-type catalog so object key ordering and future type-specific normalization cannot change semantics.

`adapter_id` records the owner selected for dispatch even if ownership changes later. `requested_at`, `accepted_at`, and `completed_at` are core-clock UTC times for the corresponding committed lifecycle transitions; for the first `set` operation, `deadline_at` is exactly ten seconds after `requested_at` as defined by the catalog. The record stores normalized operation parameters and stable failure codes, not raw HTTP bodies, headers, client addresses, adapter error text, or credentials.

### Entity-type catalog

Owner: `internal/modules/devices/catalog.go`.

A concrete `TypeCatalog` is constructed by the application and passed to the `devices` module. JSON Schemas in public `entitytypes` packages are authoritative for support, State, and parameters. Generic factories retain compile-time Go types, then erase definitions behind catalog closures so transport, persistence, and orchestration continue to carry generic JSON.

```go
func DefineOperation[State, Support, OperationSupport, Parameters any](
    name OperationName,
    parameters *entitytypes.JSONCodec[Parameters],
    selectSupport func(Support) (OperationSupport, bool),
    validateParameters func(Support, OperationSupport, Parameters) error,
    deadline time.Duration,
    satisfies func(Parameters, State) bool,
) OperationDefinition[State, Support]

func DefineEntityType[State, Support any](
    id EntityTypeID,
    state *entitytypes.JSONCodec[State],
    support *entitytypes.JSONCodec[Support],
    validateSupportedState func(Support, State) error,
    equalState func(State, State) bool,
    operations ...OperationDefinition[State, Support],
) (EntityTypeDefinition, error)

func NewTypeCatalog([]EntityTypeDefinition) (*TypeCatalog, error)
func NewBuiltinTypeCatalog() (*TypeCatalog, error)
func (*TypeCatalog) NormalizeSupport(EntityTypeID, EntitySupport) (EntitySupport, error)
func (*TypeCatalog) NormalizeState(Entity, Value) (Value, error)
func (*TypeCatalog) EqualState(Entity, Value, Value) (bool, error)
func (*TypeCatalog) ResolveCommand(Entity, OperationName, CommandParameters) (ResolvedCommand, error)
func (*TypeCatalog) Satisfies(Entity, CommandRecord, Value) (bool, error)
```

Construction rejects empty or duplicate type IDs, duplicate or non-subject-safe operation names, missing codecs/functions, and non-positive deadlines. `NewBuiltinTypeCatalog` supplies power/v1 and brightness/v1 definitions; the generic factories also support focused typed tests without enabling runtime type loading.

The built-in `entitytypes/powerv1` package defines boolean State, exact `{"value": boolean}` set parameters, and this exact support document:

```json
{"state": {}, "operations": {"set": {}}}
```

An operation key's presence means the Entity supports it. `ResolveCommand` decodes current typed support, selects operation support, validates typed parameters against both schemas and cross-document rules, and returns normalized parameters plus the immutable ten-second deadline. `NormalizeState` applies State schema and current-support compatibility. Equality schema-decodes both values but applies current-support compatibility only to the incoming value, allowing comparison with a previously valid persisted State after support narrows.

The generated brightness/v1 binding uses integer State from 0–100 and support `{"state":{"maximum":N},"operations":{"set":{"step":S}}}`. Its `set.value` must not exceed `N` and must be aligned to `S`; supported State must not exceed `N`.

`Satisfies` deliberately excludes current support. It decodes the recorded normalized parameters and candidate State, then applies the immutable operation outcome callback; power/v1 uses `parameters.Value == bool(state)` and brightness/v1 uses exact integer equality. Combined with immutable type IDs/definitions and persisted absolute deadlines, this preserves active and historical Command meaning when re-registration replaces support. Entity `type_id` remains immutable; valid names, external IDs, and normalized support may change transactionally.

Runtime type registration, manifest loading, arbitrary matching code, and persistence of type definitions remain out of scope. Later built-in or explicitly installed definitions may populate the catalog without changing generic transport, persistence, or Command orchestration interfaces.

IDs use lowercase UUIDv7 strings with type prefixes:

```text
dev_<uuidv7>  ent_<uuidv7>  obs_<uuidv7>  cmd_<uuidv7>
reg_<uuidv7>  rep_<uuidv7>  cor_<uuidv7>
```

UUID text is canonical lowercase. JSON Schema and Go edge validation enforce the prefix, UUID version nibble `7`, and RFC 4122 variant. Adapter and binding slugs match `^[a-z0-9][a-z0-9_-]{0,62}$` and may not contain periods.

### Wire envelope

Each binary owns local transport DTOs; it does not import another binary's DTOs. JSON Schemas are the compatibility interface.

```go
type Envelope[T any] struct {
    ID            string  `json:"id"`
    Schema        string  `json:"schema"`
    EmittedAt     string  `json:"emitted_at"`
    CorrelationID string  `json:"correlation_id"`
    CausationID   *string `json:"causation_id,omitempty"`
    Data          T       `json:"data"`
}
```

Every protocol-owned JSON object uses `additionalProperties: false`. Registration support has the common closed outer shape `{"state": {...}, "operations": {...}}`; the structural schema accepts subject-safe operation keys with object values, while the resolved Entity-type schema closes their contents. Command `parameters` and Observation `value` remain generic until catalog validation. All timestamps are UTC RFC3339Nano strings ending in `Z`. `correlation_id` is required. `causation_id` is omitted for a root message.

Canonical schema files and IDs are:

| Path | Envelope `schema` and JSON Schema `$id` |
| --- | --- |
| `contracts/v1/registration-request.schema.json` | `urn:hearth:schema:registration-request:v1` |
| `contracts/v1/registration-response.schema.json` | `urn:hearth:schema:registration-response:v1` |
| `contracts/v1/observation.schema.json` | `urn:hearth:schema:observation:v1` |
| `contracts/v1/command-request.schema.json` | `urn:hearth:schema:command-request:v1` |
| `contracts/v1/command-response.schema.json` | `urn:hearth:schema:command-response:v1` |
| `contracts/v1/common.schema.json` | `urn:hearth:schema:common:v1` |

`contracts/v1/embed.go` exposes these same files through `embed.FS`. The core and SDK compile them with `jsonschema/v6` at startup; non-Go adapters consume the JSON files directly. `entitytypes/powerv1` and `entitytypes/brightnessv1` similarly embed authoritative support, State, and set-parameter schemas. Each versioned language-neutral `entitytype.json` associates those schemas with operation names and defines deadlines, cross-document validation, and outcome matching through a small schema-checked relation DSL; `examples.json` supplies conformance cases. `entitytypegen` derives all per-type Go, typed SDK facades, tests, and aggregate core catalog assembly.

### Registration payloads

Owner: local wire DTOs in `sdk/adapter/types.go` and `internal/modules/devices/nats/wire.go`; shared envelope, codec, subject, route, and trace mechanics in `internal/contracts/v1/natswire`.

```go
type Registration struct {
    BindingKey string             `json:"binding_key"`
    Device     DeviceDescriptor   `json:"device"`
    Entities   []EntityDescriptor `json:"entities"`
}

type DeviceDescriptor struct {
    ExternalID *string `json:"external_id,omitempty"`
    Name       string  `json:"name"`
    Kind       string  `json:"kind"` // "light" accepted by this slice
}

type EntityDescriptor struct {
    Key        string          `json:"key"`
    ExternalID string          `json:"external_id"`
    Name       string          `json:"name"`
    Type       string          `json:"type"` // "hearth.power/v1" in this slice
    Support    json.RawMessage `json:"support"`
}

type Binding struct {
    BindingKey string          `json:"binding_key"`
    DeviceID   string          `json:"device_id"`
    Entities   []EntityBinding `json:"entities"`
}

type EntityBinding struct {
    Key      string `json:"key"`
    EntityID string `json:"entity_id"`
}

type RegistrationResponse struct {
    Status  string             `json:"status"` // accepted | rejected
    Binding *Binding           `json:"binding,omitempty"`
    Error   *RegistrationError `json:"error,omitempty"`
}

type RegistrationError struct {
    Code    string `json:"code"` // invalid_descriptor | immutable_type_change | identity_conflict
    Message string `json:"message"`
}
```

Constraints:

- `accepted` requires `binding` and omits `error`; `rejected` requires `error` and omits `binding`. A rejection never exposes partially committed IDs.
- Registration rejection codes are permanent: `invalid_descriptor` covers module/catalog-invalid descriptors, `immutable_type_change` covers a changed Entity type, and `identity_conflict` covers binding or external-ID conflicts. Messages are safe for operator display and contain at most 512 characters.
- Transient core, SQLite, NATS, timeout, and no-responder failures do not use `rejected`; they remain request errors so the adapter can retry them.
- `binding_key` and Entity `key` use the slug pattern.
- Device and Entity names contain 1–128 Unicode characters.
- External IDs contain 1–256 characters when present.
- Device `kind` is a 1–128 character string; the structural wire schema does not enumerate kinds, but the first-slice module accepts only `light`.
- Entity `type` is a 1–128 character versioned catalog identifier; the structural wire schema does not enumerate catalog contents.
- `support` has required object fields `state` and `operations`; operation keys are subject-safe and each operation-support value is an object. The resolved catalog schema performs exact type-specific validation.
- `entities` contains exactly one Entity in this slice; the database permits only one Entity mapping per binding.
- Re-registration with the same adapter ID, binding key, and Entity key returns the same canonical IDs. Changing the Entity key for an existing binding is an identity conflict; changing its Entity type rejects the whole registration. Valid normalized support, name, and external-ID updates commit transactionally.
- A conflicting external mapping rejects the whole registration transaction.

### Observation wire payload

Each binary keeps this wire DTO private. The public SDK `Observation` type omits `RefreshForCommand`; only command evidence can add the private wire link.

```go
type wireObservation struct {
    EntityID          string          `json:"entity_id"`
    Value             json.RawMessage `json:"value"`
    AdapterReceivedAt string          `json:"adapter_received_at"`
    SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
    RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}
```

- `value` is one structurally valid JSON value. After resolving the Entity, the core validates it against the registered Entity type; the first-light type accepts only JSON booleans.
- `adapter_received_at` is when the adapter freshly acquired the value.
- `source_updated_at` preserves an optional upstream last-change claim.
- The core adds `observed_at` from the JetStream server timestamp when the message was durably received.
- A command-evidence publication carries both `refresh_for_command_id = cmd_...` and envelope `causation_id = cmd_...`, and reuses the accepted Command correlation ID.
- The SDK generates the `obs_...` envelope ID once per ordinary or evidence publication call.

### Command payloads

```go
type Command struct {
    ID            string
    CorrelationID string
    EntityID      string          `json:"entity_id"`
    OperationName string          `json:"operation"`
    Parameters    json.RawMessage `json:"parameters"`
    Deadline      string          `json:"deadline"`
}

type CommandResponse struct {
    CommandID string        `json:"command_id"`
    Status    string        `json:"status"` // accepted | rejected
    Error     *CommandError `json:"error,omitempty"`
}

type CommandError struct {
    Code    string `json:"code"` // const "upstream_rejected" in v1
    Message string `json:"message"`
}
```

The Command ID is the request envelope ID. `parameters` is a JSON object validated by the core against the resolved Entity type and operation before dispatch; the first-light `set` operation requires `{"value":true}` or `{"value":false}`. `deadline` comes from the resolved operation definition. `error` is required only for `rejected` and omitted for `accepted`. Error messages are safe for operator display and at most 512 characters.

### HTTP types

Owner: `internal/modules/devices/api/types.go`.

```go
type EntityBody struct {
    ID       string         `json:"id"`
    DeviceID string         `json:"device_id"`
    Name     string         `json:"name"`
    Type     string         `json:"type"`
    Support  map[string]any `json:"support"`
    State    *StateBody     `json:"state"`
}

type StateBody struct {
    Value             any     `json:"value"`
    ObservationID     string  `json:"observation_id"`
    AdapterReceivedAt string  `json:"adapter_received_at"`
    SourceUpdatedAt   *string `json:"source_updated_at,omitempty"`
    ObservedAt        string  `json:"observed_at"`
}

type CommandBody struct {
    OperationName string         `json:"operation"`
    Parameters    map[string]any `json:"parameters"`
}

type CommandResultBody struct {
    CommandID     string `json:"command_id"`
    Status        string `json:"status"` // const "satisfied"
    ObservationID string `json:"observation_id"`
    Value         any    `json:"value"`
}

type ErrorBody struct {
    Error APIError `json:"error"`
}

type APIError struct {
    Code      string  `json:"code"`
    Message   string  `json:"message"`
    CommandID *string `json:"command_id,omitempty"`
}
```

The HTTP mapper decodes normalized domain JSON into these generic transport fields and encodes `parameters` back to one JSON object before calling the module. Huma/OpenAPI therefore describes structural JSON values and objects; Entity-type-specific schemas remain catalog semantics rather than being expanded into the first-slice OpenAPI document.

Stable API error codes are:

```text
invalid_request
entity_not_found
adapter_unavailable
upstream_rejected
outcome_timeout
internal_error
```

### Configuration types

Owner: `internal/platform/config` and each composition package.

```go
type CoreConfig struct {
    HTTPAddr  string `yaml:"http_addr"`
    NATSURL   string `yaml:"nats_url"`
    SQLitePath string `yaml:"sqlite_path"`
}

type HomeAssistantConfig struct {
    AdapterID string            `yaml:"adapter_id"`
    NATSURL   string            `yaml:"nats_url"`
    Binding   HABindingConfig   `yaml:"binding"`
    Upstream  HAUpstreamConfig  `yaml:"home_assistant"`
}

type HABindingConfig struct {
    Key              string  `yaml:"key"`
    DeviceExternalID *string `yaml:"device_external_id,omitempty"`
    DeviceName       string  `yaml:"device_name"`
    EntityExternalID string  `yaml:"entity_id"`
    EntityName       string  `yaml:"entity_name"`
}

type HAUpstreamConfig struct {
    URL       string `yaml:"url"`
    TokenFile string `yaml:"token_file"`
}

type SimulatorConfig struct {
    AdapterID string `yaml:"adapter_id"`
    NATSURL   string `yaml:"nats_url"`
    BindingKey string `yaml:"binding_key"`
    Scenario  string `yaml:"scenario"`
}
```

Protocol limits are not YAML fields: the built-in `hearth.power/v1` `set` definition supplies its ten-second Command deadline; fixed v1 transport/storage constants are the one-minute future-clock diagnostic threshold, seven-day/one-GiB stream, 30-second acknowledgement wait, and one pending acknowledgement. Observation retention is configurable through the single `observation_retention` core setting (default 30 days, minimum 8 days) with the current-State observation pinned: each hourly prune deletes non-current observations with `observed_at` older than the window. Command records are not pruned in this slice; retention policy awaits a history interface.

## Interfaces

### Adapter SDK

Owner: generic transport in `sdk/adapter`, typed routing in `sdk/adapter/typed`, and the built-in facade in `sdk/adapter/powerv1`.

```go
func Connect(context.Context, Config) (*Session, error)
func (*Session) Register(context.Context, Registration) (Binding, error)
func (*Session) PublishObservation(context.Context, Observation) (ObservationID, error)
func (*Session) ServeCommands(context.Context, CommandHandler) error
func (*Session) Close() error

type Observation struct {
    EntityID          string          `json:"entity_id"`
    Value             json.RawMessage `json:"value"`
    AdapterReceivedAt string          `json:"adapter_received_at"`
    SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
}

type CommandEvidence interface {
    PublishObservation(context.Context, Observation) (ObservationID, error)
}

type RegistrationRejectionCode string
const (
    RegistrationInvalidDescriptor   RegistrationRejectionCode = "invalid_descriptor"
    RegistrationImmutableTypeChange RegistrationRejectionCode = "immutable_type_change"
    RegistrationIdentityConflict    RegistrationRejectionCode = "identity_conflict"
)

type RegistrationRejectedError struct {
    Code    RegistrationRejectionCode
    Message string
}

func (*RegistrationRejectedError) Error() string

type CommandHandler func(context.Context, Command, Responder) error

type Responder interface {
    Accept() (CommandEvidence, error)
    Reject(message string) error
    RejectUnavailable(message string) error
}
```

Typed routing converts one `(entity ID, operation name)` route into the same `CommandHandler` interface:

```go
// sdk/adapter/typed
type Command[P any] struct {
    ID, CorrelationID, EntityID string
    Parameters P
    Deadline   time.Time
}

type Handler[P any] func(context.Context, Command[P], adapter.Responder) error

func Operation[P any](entityID, operationName string, decode ParameterDecoder[P], handler Handler[P]) (Route, error)
func NewCommandHandler(routes ...Route) (adapter.CommandHandler, error)

// sdk/adapter/powerv1
func NewEntityDescriptor(adapter.EntityMetadata, Support) (adapter.EntityDescriptor, error)
func NewCommandHandler(string, Support, Handlers) (adapter.CommandHandler, error)
func NewObservation(ObservationInput) (adapter.Observation, error)
```

The generated facades validate and normalize support, expose typed parameters, and require Observation support so support-dependent State rules run before encoding; they also format Observation timestamps as UTC. A Go adapter can therefore register, route Commands, and publish ordinary Observations without constructing `json.RawMessage` or switching on operation strings. The generic Session retains concurrency, one-shot response, acknowledgement/retry, and trace/correlation/causation behavior; successful acceptance adds the command-evidence publication capability.

Interface contract:

- `Connect` validates the adapter slug, compiles embedded schemas, connects to NATS, and installs W3C propagation. It does not provision core-owned streams.
- `Register` validates the request and retries one prepared request envelope across timeout, no-responder, and transient NATS failures until success or context cancellation. An accepted response returns its `Binding`; a rejected response returns `*RegistrationRejectedError`, suitable for `errors.As`. Local validation, malformed responses, and registration rejection are permanent failures.
- `PublishObservation` generates one ordinary Observation envelope and retries that same bytes/ID across transient NATS disconnects. It returns the generated ID after JetStream publish acknowledgement, or returns that ID with an error when the context expires. There is no local outbox.
- `ServeCommands` subscribes to the adapter-scoped wildcard, starts an independent handler invocation for each valid request, and blocks until context cancellation or terminal serving failure. Handler invocations may overlap, including for the same Entity, so adapter code must be concurrency-safe. An adapter may serialize internally when its vendor protocol requires it, but the SDK and core provide no ordering guarantee.
- A `Responder` is one-shot. `Accept` first sends the Core NATS acceptance reply and then returns evidence bound to that Command's ID, correlation, Entity, runtime, deadline, and trace context. A failed response returns nil evidence without consuming the responder, so the caller may retry; a response after success returns `ErrAlreadyResponded`. `Reject` emits the fixed v1 code `upstream_rejected`; `RejectUnavailable` emits `entity_unavailable`. Returning without a reply returns/logs `ErrMissingResponse` and lets the core request time out.
- Evidence may publish zero or more Observations for its bound Entity after the handler returns. It supplies the Command linkage and metadata, retries one stable envelope/ID through transient failure until acknowledgement or the effective deadline, and rejects a different Entity before publication. The caller context may shorten publication but cannot extend the Command deadline.
- `Close` is idempotent and drains subscriptions within the caller's shutdown budget.
- Vendor calls, credentials, polling, state refresh, mapping, and checkpoints remain outside the SDK.

### Devices module

Owner: `internal/modules/devices/service.go`.

```go
type Stores struct {
    Registration RegistrationRepository
    Runtimes     RuntimeRepository
    Adapters     AdapterRepository
    Availability AvailabilityRepository
    Reads        ReadRepository
    Enablement   EnablementRepository
    Commands     CommandLedger
    Observations ObservationRepository
}

type CommandSender interface {
    Send(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error)
}

func NewService(Stores, CommandSender, *TypeCatalog, Dependencies) *Service
func (*Service) Register(context.Context, string, RuntimeID, Registration) (Binding, error)
func (*Service) ProjectObservation(context.Context, string, RuntimeID, Observation, time.Time) (ProjectionResult, error)
func (*Service) GetEntity(context.Context, EntityID) (EntityWithState, error)
func (*Service) ExecuteCommand(context.Context, EntityID, OperationName, CommandParameters) (CommandResult, error)
```

`Dependencies` contains the injected clock and UUIDv7 generators used by deterministic module tests. `TypeCatalog` is a required concrete dependency selected by application assembly; it is not a runtime plugin interface. Capability interfaces are defined in the consuming `devices` package. One SQLite repository satisfies every production capability, while tests implement only the capabilities they exercise.

`ExecuteCommand` behavior:

1. Resolve the Entity and owner. Resolve the operation name against its current typed support and immutable type definition, validate the parameters, and obtain the outcome matcher and deadline. Unknown or unsupported operation names and invalid parameters fail before a Command record is created or dispatched.
2. Create Command/correlation IDs, an independent lifecycle context using the operation deadline, and a Command-ID-keyed Observation waiter.
3. Commit `requested` before dispatch. Failure prevents dispatch and returns an internal error. This commit is the point of no return: later HTTP cancellation does not cancel the lifecycle.
4. Send one Core NATS request/reply without retry or replay. Persist dispatch/response failures as `adapter_unavailable`, `rejected`, or `internal_failure`.
5. On acceptance, set `accepted_at`. If a linked Observation already made the record terminal, preserve its status while filling the timestamp.
6. Wait for `ProjectObservation` to atomically commit an `applied` or `unchanged` Observation that the catalog's outcome policy matches to this Command; otherwise persist `outcome_timeout` at the deadline.
7. Return `CommandResult` or the mapped stable error. After HTTP cancellation, abandon only the response and continue recording the lifecycle through outcome/deadline.
8. Remove the waiter at completion. On restart, lose in-memory waits, mark persisted `requested`/`accepted` records `interrupted`, and never redispatch them.

Lifecycle writes are monotonic and idempotent. `CompleteCommand` accepts only ordinary runtime failure outcomes; only startup-wide `InterruptActiveCommands` may record `interrupted/core_restarted`. Multiple calls for one Entity run concurrently without a guard, queue, or supersession; each has an independent ID, record, waiter, and deadline. An Observation satisfies only its `refresh_for_command_id`. Success may be immediately superseded, while canonical State follows core receive order.

### HTTP

Device operations are owned by `internal/modules/devices/api`; `internal/app/hearthd` owns the server and plain Echo health/readiness routes.

```http
GET /v1/entities/{entity_id}
POST /v1/entities/{entity_id}/commands
GET /healthz
GET /readyz
```

`GET /v1/entities/{entity_id}` returns HTTP 200 with `EntityBody`; `state` is `null` before the first accepted Observation. Unknown IDs return 404.

`POST /v1/entities/{entity_id}/commands` accepts:

```json
{"operation":"set","parameters":{"value":true}}
```

It waits synchronously for a linked outcome or deadline. Huma validates the structural body; the `devices` module validates `parameters` against the resolved Entity type and operation before creating a Command record. Status mapping is:

| Condition | HTTP | Error code |
| --- | ---: | --- |
| Invalid body, ID, operation | 400 | `invalid_request` |
| Unknown Entity | 404 | `entity_not_found` |
| No adapter responder | 503 | `adapter_unavailable` |
| Adapter rejects upstream call | 502 | `upstream_rejected` |
| No matching linked Observation by deadline | 504 | `outcome_timeout` |
| Internal failure | 500 | `internal_error` |

`devices/api` registers stable Huma operation IDs. Huma exposes runtime OpenAPI; no generated file is committed. Command history has no HTTP operation or UI.

### NATS protocol

```text
hearth.v1.adapter.<adapter>.register
hearth.v1.adapter.<adapter>.observation.<entity>
hearth.v1.adapter.<adapter>.command.<entity>.<operation>
```

- `<adapter>` is the configured adapter slug.
- `<entity>` is the canonical Entity ID.
- `<operation>` is the registered subject-safe operation name; it is `set` in this slice.
- Core validates that subject adapter/entity/operation tokens match the payload, the operation is present in current Entity support, and ownership is current.
- Registration and Commands use Core NATS request/reply.
- Persisting a Command record does not put the Command in a stream and never causes replay or redispatch.
- Observations use JetStream and require a publish acknowledgement.

Stream `HEARTH_OBSERVATIONS_V1`:

```text
Subjects:       hearth.v1.adapter.*.observation.>
Storage:        file
Retention:      limits
Max age:        168h
Max bytes:      1 GiB
Discard:        old
Duplicate ID:   envelope id / Nats-Msg-Id
```

Durable consumer `hearthd-state-v1`:

```text
Delivery:        all / instant replay
Acknowledgement: explicit
Ack wait:        30s
Max ack pending: 1
Max deliver:     unlimited
```

The core acknowledges applied, unchanged, duplicate, and durably recorded rejected Observations only after the SQLite transaction completes. Schema-invalid input is logged with subject, stream sequence, validation error, and safe metadata, then acknowledged. Transient SQLite/infrastructure errors remain unacknowledged.

## Persistence

Owner: `internal/platform/db` for migration/query/generated code; `internal/modules/devices/repository.go` for domain mapping and transaction behavior.

SQLite uses `modernc.org/sqlite`, foreign keys, WAL mode, a five-second busy timeout, and one writer connection. Goose migration `internal/platform/db/migrations/00001_initial.sql` creates:

```sql
-- +goose Up
PRAGMA foreign_keys = ON;

CREATE TABLE devices (
    id         TEXT PRIMARY KEY CHECK (id LIKE 'dev_%'),
    kind       TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 128),
    name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE entities (
    id           TEXT PRIMARY KEY CHECK (id LIKE 'ent_%'),
    device_id    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    type_id      TEXT NOT NULL CHECK (length(type_id) BETWEEN 1 AND 128),
    support_json TEXT NOT NULL CHECK (
        json_valid(support_json) AND json_type(support_json) = 'object'
    ),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE TABLE commands (
    id                     TEXT PRIMARY KEY CHECK (id LIKE 'cmd_%'),
    entity_id              TEXT NOT NULL REFERENCES entities(id) ON DELETE RESTRICT,
    adapter_id             TEXT NOT NULL CHECK (length(adapter_id) BETWEEN 1 AND 63),
    operation              TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 63),
    parameters_json        TEXT NOT NULL CHECK (
        json_valid(parameters_json) AND json_type(parameters_json) = 'object'
    ),
    correlation_id         TEXT NOT NULL CHECK (correlation_id LIKE 'cor_%'),
    status                 TEXT NOT NULL CHECK (
        status IN (
            'requested', 'accepted', 'satisfied', 'rejected',
            'adapter_unavailable', 'outcome_timeout',
            'internal_failure', 'interrupted'
        )
    ),
    requested_at           TEXT NOT NULL,
    deadline_at            TEXT NOT NULL,
    accepted_at            TEXT,
    completed_at           TEXT,
    outcome_observation_id TEXT UNIQUE CHECK (
        outcome_observation_id IS NULL OR outcome_observation_id LIKE 'obs_%'
    ),
    failure_code           TEXT CHECK (
        failure_code IS NULL OR failure_code IN (
            'adapter_unavailable', 'upstream_rejected', 'outcome_timeout',
            'internal_error', 'core_restarted'
        )
    ),
    CHECK (
        (status IN ('requested', 'accepted') AND completed_at IS NULL)
        OR (status NOT IN ('requested', 'accepted') AND completed_at IS NOT NULL)
    ),
    CHECK (
        (status = 'satisfied' AND outcome_observation_id IS NOT NULL)
        OR (status <> 'satisfied' AND outcome_observation_id IS NULL)
    ),
    CHECK (
        (status IN ('requested', 'accepted', 'satisfied') AND failure_code IS NULL)
        OR (status = 'rejected' AND failure_code = 'upstream_rejected')
        OR (status = 'adapter_unavailable' AND failure_code = 'adapter_unavailable')
        OR (status = 'outcome_timeout' AND failure_code = 'outcome_timeout')
        OR (status = 'internal_failure' AND failure_code = 'internal_error')
        OR (status = 'interrupted' AND failure_code = 'core_restarted')
    )
);

CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC);

CREATE TABLE adapter_bindings (
    adapter_id        TEXT NOT NULL,
    binding_key       TEXT NOT NULL,
    device_id         TEXT NOT NULL UNIQUE REFERENCES devices(id) ON DELETE CASCADE,
    external_device_id TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (adapter_id, binding_key),
    UNIQUE (adapter_id, external_device_id)
);

CREATE TABLE adapter_entity_mappings (
    adapter_id        TEXT NOT NULL,
    binding_key       TEXT NOT NULL,
    entity_key        TEXT NOT NULL,
    entity_id         TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
    external_entity_id TEXT NOT NULL,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (adapter_id, binding_key, entity_key),
    UNIQUE (adapter_id, external_entity_id),
    FOREIGN KEY (adapter_id, binding_key)
        REFERENCES adapter_bindings(adapter_id, binding_key)
        ON DELETE CASCADE
);

CREATE TABLE observations (
    receive_order       INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id      TEXT NOT NULL UNIQUE CHECK (observation_id LIKE 'obs_%'),
    adapter_id          TEXT NOT NULL,
    entity_id           TEXT NOT NULL,
    disposition         TEXT NOT NULL CHECK (
        disposition IN ('applied', 'unchanged', 'rejected')
    ),
    rejection_code      TEXT CHECK (
        rejection_code IS NULL OR rejection_code IN (
            'unknown_entity', 'wrong_adapter', 'invalid_value'
        )
    ),
    adapter_received_at TEXT NOT NULL,
    observed_at         TEXT NOT NULL,
    CHECK (
        (disposition = 'rejected' AND rejection_code IS NOT NULL)
        OR (disposition <> 'rejected' AND rejection_code IS NULL)
    )
);

CREATE INDEX observations_observed_at_idx
    ON observations(observed_at);

CREATE TABLE entity_states (
    entity_id           TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    observation_id      TEXT NOT NULL UNIQUE REFERENCES observations(observation_id),
    value_json          TEXT NOT NULL CHECK (json_valid(value_json)),
    adapter_received_at TEXT NOT NULL,
    source_updated_at   TEXT,
    observed_at         TEXT NOT NULL,
    receive_order       INTEGER NOT NULL UNIQUE REFERENCES observations(receive_order)
);

-- +goose Down
DROP TABLE entity_states;
DROP INDEX observations_observed_at_idx;
DROP TABLE observations;
DROP TABLE adapter_entity_mappings;
DROP TABLE adapter_bindings;
DROP INDEX commands_entity_requested_idx;
DROP TABLE commands;
DROP TABLE entities;
DROP TABLE devices;
```

Strict typed-ID, slug, catalog, operation, generic JSON, and UTC timestamp validation occurs at the module/transport edge. SQLite reinforces ID prefixes, JSON validity and object shape where applicable, lifecycle enums, ownership uniqueness, and foreign keys; it deliberately does not duplicate the catalog's Entity-type, support, State, parameter, or outcome semantics.

Required sqlc queries are split into four generation units with separate generated packages and focused `Querier` interfaces:

```text
registration:
  GetBinding
  GetBindingByExternalDeviceID
  CreateDevice
  UpdateDeviceDescriptor
  CreateEntity
  UpdateEntityDescriptor
  CreateBinding
  UpdateBindingExternalID
  GetEntityMapping
  GetEntityMappingByExternalID
  CreateEntityMapping
  UpdateEntityMappingExternalID

state:
  GetEntityView
  GetEntityState
  UpsertEntityState

commands:
  CreateCommand
  GetCommand
  MarkCommandAccepted
  CompleteCommand
  SatisfyCommandFromObservation
  InterruptActiveCommands

observations:
  GetObservation
  InsertObservation
  DeleteExpiredObservations
```

`sqlc.yaml` has one SQLite generation entry using the shared migrations and the concern-specific query files under `internal/modules/devices/dbqueries`. It emits one feature-owned package at `internal/modules/devices/dbsqlc`, so table models are generated once. The concrete `devices` SQLite repository owns one generated `Queries` value and binds it to transactions with `WithTx`. Generated types remain persistence details and never leak through the module's capability-based stores.

The pruning query excludes the observation referenced by current State:

```sql
-- name: DeleteExpiredObservations :exec
DELETE FROM observations
WHERE observed_at < ?
  AND NOT EXISTS (
      SELECT 1
      FROM entity_states
      WHERE entity_states.observation_id = observations.observation_id
  );
```

Checking `observation_id` protects both State foreign keys because they reference the same observation. The cutoff derives from Core now minus the configured retention on each hourly pass, so policy changes apply to already persisted rows without rewriting them. Superseded State observations become eligible at the next pruning pass.

`RegisterBinding`, `ProjectObservation`, and all Command transitions own their SQLite transactions behind the repository; generated sqlc types never cross that seam. The concrete `devices` repository receives the same required `TypeCatalog` instance as the service so projection can validate values, compare State, and evaluate a linked Command's outcome policy inside the transaction. Projection atomically inserts the observation, updates State, and satisfies a matching active Command. Registration stores the catalog-normalized `support_json` in the existing binding transaction; re-registration replaces that one document atomically.

`outcome_observation_id` intentionally has no observation foreign key because observations expire while Command history remains. Satisfaction writes this typed, immutable ID only after inserting its observation in the same transaction.

### Observation projection rules

For each schema-valid Observation, one transaction:

1. Read core-owned `observed_at` from the JetStream server timestamp; return `duplicate` when the Observation ID already has an observation.
2. When `adapter_received_at > observed_at + 1m`, log a structured clock-skew diagnostic containing Observation, adapter, Entity, `adapter_received_at`, and `observed_at`; continue normally.
3. Resolve Entity/owner; record `rejected/unknown_entity` or `rejected/wrong_adapter` for identity failures.
4. Validate the generic JSON value through the Entity-type catalog; record `rejected/invalid_value` when it violates the registered State schema. Unknown catalog IDs in persisted Entities are an internal configuration failure, not an adapter-input rejection.
5. Insert the observation with the next internal `receive_order`. For a known, correctly owned Entity with a valid value, advance State regardless of timestamps: no current State or a value the catalog considers different is `applied`; a value the catalog considers equivalent is `unchanged`.
6. An `applied`/`unchanged` Observation with `refresh_for_command_id` satisfies only an active `requested`/`accepted` Command for the same Entity/adapter when its catalog outcome policy matches the Command operation and parameters. Set `completed_at`/`outcome_observation_id` and notify its waiter. No rejected, nonmatching, wrong-identity, or terminal link satisfies a Command.
7. Atomically commit observation, State, and satisfaction before JetStream acknowledgement. No per-row expiry is stored.

On the hourly pass, delete non-current observations with `observed_at` older than the configured window, except the current-State observation; it becomes eligible after State advances. Startup performs no prune.

## Home Assistant adapter

Owner: `internal/adapters/homeassistant`.

The adapter uses Home Assistant's WebSocket API directly:

1. Read YAML/token, connect/authenticate, and call the SDK's context-bound `Register` once. The Session retries transient transport failures and stops on local validation or `RegistrationRejectedError`.
2. After startup/reconnect, subscribe to `state_changed` and wait for its acknowledgement before requesting `get_states`. Buffer configured-Entity events while the snapshot request is pending.
3. Publish the configured light's snapshot, then replay buffered events whose Home Assistant `last_updated` is later than the snapshot's `last_updated`, in WebSocket arrival order, before switching to live event publication. This closes the snapshot-to-subscription gap without letting pre-snapshot events overwrite the snapshot.
4. Map `on`/`off` to the JSON booleans accepted by `hearth.power/v1`; reject unsupported/unavailable values rather than inventing State.
5. Map `set` parameters `{"value":true}`/`{"value":false}` to `light.turn_on`/`light.turn_off`. Reject failed calls; accept after successful service-call completion.
6. Call `get_states` and durably publish a linked refresh through the evidence returned by successful acceptance.

Adapter acquisition time is `adapter_received_at`; Home Assistant `last_updated` is optional `source_updated_at` and is used only by the adapter to reconcile buffered startup/reconnect events. Core `observed_at` comes from the JetStream server timestamp. Home Assistant identifiers, service names, contexts, and payloads stay in this package. WebSocket request IDs, pending responses, event buffering, and refresh publications are concurrency-safe: overlapping handlers may issue calls concurrently, and each refresh uses its accepted Command evidence. The adapter serializes by Entity only if the protocol requires it and is deleted after migration.

## Configuration

Example `configs/hearthd.example.yaml`:

```yaml
http_addr: 127.0.0.1:8080
nats_url: nats://127.0.0.1:4222
sqlite_path: .data/hearthd.db
```

Example `configs/homeassistant.example.yaml`:

```yaml
adapter_id: homeassistant-migration
nats_url: nats://127.0.0.1:4222
binding:
  key: office-light
  device_external_id: ha-device-id
  device_name: Office Light
  entity_id: light.office
  entity_name: Power
home_assistant:
  url: http://homeassistant.local:8123
  token_file: .secrets/homeassistant-token
```

Example `configs/simulator.example.yaml`:

```yaml
adapter_id: simulator
nats_url: nats://127.0.0.1:4222
binding_key: simulated-light
scenario: happy
```

Actual local files and `.secrets/` are ignored by Git. No environment override layer is implemented.

## Startup and readiness

Core startup order:

1. Parse and validate YAML.
2. Open SQLite; enable foreign keys/WAL/busy timeout; apply Goose migrations.
3. Mark any `requested` or `accepted` Command records `interrupted` with failure code `core_restarted`; do not redispatch them.
4. Perform no observation prune; retained history waits for the next hourly pass.
5. Connect to NATS.
6. Idempotently provision/validate the stream and durable consumer.
7. Construct and validate the closed first-light Entity-type catalog.
8. Construct the concrete `devices` repository and service with the same catalog instance.
9. Start the Observation consumer wired to the module's projection handler.
10. Construct the Echo/Huma transport.
11. Listen on the configured HTTP address.

`GET /healthz` returns 200 whenever the HTTP process can serve. `GET /readyz` returns 200 only while SQLite responds, NATS is connected, JetStream resources match required configuration, and the Observation consumer is active; otherwise 503.

Adapters do not depend on process startup order. They reconnect to NATS and retry transient registration failures until the core responds, but stop on local validation or a schema-defined permanent registration rejection.

## Project layout

```text
cmd/
├── hearthd/                         # new — thin core executable entry point
├── hearth-adapter-homeassistant/    # new — thin disposable-adapter entry point
└── hearth-simulator/                # new — thin simulator entry point
internal/
├── cmd/entitytypegen/               # new — complete build-time Entity-type generator
├── app/
│   ├── hearthd/                     # new — core composition, HTTP, lifecycle
│   ├── homeassistant/               # new — migration-adapter composition
│   └── simulator/                   # new — simulator composition
├── modules/
│   └── devices/
│       ├── api/                     # new — Huma operations and transport mapping
│       ├── model.go                 # new — Device, Entity, generic State value, and Command types
│       ├── catalog.go               # new — closed first-light Entity-type definitions and semantic policies
│       ├── service.go               # new — registration, projection, Command behavior
│       ├── repository.go            # new — identity, State, observation, and Command-record persistence seam
│       ├── command.go               # new — durable lifecycle transitions plus concurrent in-memory outcome waits
│       └── errors.go                # new — domain errors
├── adapters/
│   ├── homeassistant/               # new — WebSocket client and HA mapping
│   └── simulator/                   # new — simulated external behavior and faults
└── platform/
    ├── config/                      # new — strict per-process YAML loading
    ├── db/
    │   ├── migrations/              # new — Goose SQLite migrations
    │   ├── queries/                 # new — registration/state/commands/observations query directories
    │   └── sqlc/                    # new/generated — matching focused query packages
    └── nats/                        # new — core NATS/JetStream transport
entitytypes/
├── codec.go                         # new — schema-backed typed JSON codec
├── powerv1/                         # new — declarative power inputs and generated outputs
└── brightnessv1/                    # new — declarative brightness inputs and generated outputs
sdk/
└── adapter/                         # new — public, stateless Go session facade
    ├── typed/                       # new — typed Command routing
    ├── powerv1/                     # new/generated — typed power facade
    └── brightnessv1/                # new/generated — typed brightness facade
contracts/
└── v1/
    ├── embed.go                     # new — canonical embedded schema FS
    └── *.schema.json                # new — authoritative v1 wire schemas
configs/
├── hearthd.example.yaml             # new — core non-secret example
├── homeassistant.example.yaml       # new — migration-adapter example
└── simulator.example.yaml           # new — simulator example
devenv.nix                            # modify — Go/NATS/sqlc/Goose and native processes
devenv.yaml                           # modify — project process/task definitions if needed
.gitignore                            # modify — local YAML, .data, and .secrets
go.mod                                # new — github.com/mholtzscher/hearth
go.sum                                # new — pinned Go dependency graph
sqlc.yaml                             # new — root SQLite generation config
```

## Deliverables

| ID | Deliverable | Effort | Depends on |
| --- | --- | --- | --- |
| D1 | Go/devenv foundation, dependency lock, typed IDs, closed first-light Entity-type catalog, embedded JSON Schemas, YAML loaders | L | - |
| D2 | Stateless adapter SDK, NATS subjects/envelopes, schema validation, registration/Observation/Command contract tests | L | D1 |
| D3 | SQLite migration/sqlc layer, Command ledger, and idempotent binding registration in `devices` | L | D1, D2 |
| D4 | JetStream provisioning, durable Observation projection, observation pruning, GET Entity, readiness | XL | D2, D3 |
| D5 | Audited ephemeral Command orchestration, POST endpoint, simulator happy path and complete failure matrix | XL | D4 |
| D6 | Disposable Home Assistant WebSocket adapter and real-light verification | L | D2, D5 |
| D7 | Broad checks, runtime OpenAPI assertions, recovery tests, docs reconciliation | L | D1–D6 |

Total relative effort is **XL**. No calendar estimate is asserted.

## Acceptance and success criteria

- [x] From a clean checkout, `devenv test` (or its documented replacement) checks generation, tests, vet, runtime OpenAPI, compilation of every authoritative JSON Schema, and validation of cross-binary fixtures.
- [x] Re-registration returns identical IDs, replaces normalized Entity support, and updates the Entity name returned by HTTP; both persist across restart. Conflicting binding/external mappings, an Entity type change, or catalog-invalid support return their schema-defined permanent rejection codes atomically without partial rows, and adapters do not retry them.
- [x] A registered, unobserved Entity returns HTTP 200 with its configured metadata and `state: null`.
- [x] A JetStream-acknowledged Observation survives restart; exact redelivery changes neither State nor observation count.
- [x] Hourly pruning deletes non-current observations older than the configured window, pins the current-State observation without foreign-key errors, then deletes it after State advances.
- [x] The generic wire and persistence paths round-trip the first-light JSON boolean without boolean-specific columns or DTO fields; the catalog rejects a schema-valid non-boolean Observation as `invalid_value` and rejects invalid `set` parameters before Command creation or dispatch.
- [x] A later-received Observation advances State even when its `adapter_received_at` is older; a catalog-equivalent value advances State evidence/timestamps as `unchanged`; adapter clock skew beyond the threshold is logged but accepted; wrong-owner, unknown-Entity, invalid-value, and malformed input produce the specified rejection/diagnostic/acknowledgement behavior.
- [x] Every dispatched Command first commits `requested`; record-creation failure prevents dispatch.
- [x] Two simultaneous Commands for one Entity create distinct durable records, both dispatch, and invoke independent SDK handlers without SDK-imposed ordering; each is satisfiable only by its own linked Observation. Opposite-value interleavings allow both to satisfy at different receive orders or one to time out on a mismatched link; final State follows core receive order.
- [x] Missing adapter, upstream rejection, deadline, and unexpected post-creation failures persist the specified terminal status/failure code and map respectively to HTTP 503, 502, 504, and 500 (`internal_failure` when SQLite remains writable).
- [x] An already-matched target is dispatched, accepted, explicitly refreshed, and satisfied only by its linked Observation; State and Command satisfaction reference that Observation in one transaction.
- [x] A linked Observation may win the acceptance race; later acceptance fills `accepted_at` without status regression.
- [x] HTTP disconnect after `requested` commit leaves the lifecycle active through outcome/deadline and records its terminal status.
- [x] Restart marks `requested`/`accepted` Commands `interrupted`, never redispatches them, and prevents later linked Observations from changing terminal status while still allowing State advancement.
- [x] Restart after atomic State/Command commit but before JetStream acknowledgement redelivers without changing either record.
- [x] The simulator passes duplicate, delayed-source-time, future-clock-skew, malformed, unavailable-adapter, upstream-rejection, no-op-refresh, overlapping-opposite-command, outcome-timeout, interrupted-command, and restart-before-ack scenarios deterministically.
- [x] A configured Home Assistant light reads and sets on/off through Hearth; every HTTP 200 is tied to its linked Observation. A controlled state transition after snapshot acquisition but before live event processing is published after reconciliation and becomes canonical State.
- [x] Core/wire fixtures contain no Home Assistant service names or payload shapes, and removing the migration adapter preserves canonical Device/Entity IDs and the wire contract.
- [x] HTTP listens on its configured address, with a loopback-only example configuration; `/readyz` fails for unavailable SQLite, NATS, required JetStream configuration, or consumer.

## Test strategy

| Layer | What | How |
| --- | --- | --- |
| Pure module | ID validation, typed catalog support/State/parameter validation and erasure, type-aware equality and immutable outcome matching, registration conflicts, receive-order projection, source-time diagnostics, same-value advancement, Command transition monotonicity, concurrent waiter isolation, interleaved outcomes, deadline/result mapping | Inject in-memory Repository, CommandSender, closed catalog, clock, and ID generator |
| Repository | Transactions, constraints, idempotency, receive ordering, observation pruning, current-State retention, Command lifecycle persistence, atomic outcome satisfaction, startup interruption | Temporary real SQLite; apply Goose; use generated sqlc queries |
| SDK/NATS | Subjects, envelopes, schema validation, typed power registration/Command/Observation facade, accepted/rejected registration responses and retry classification, publish acknowledgement/retry, concurrent Command handler invocation, request/reply, responder invariants, W3C headers | Typed facade tests plus in-process NATS Server with JetStream |
| HTTP | Huma validation, operation IDs, nullable State, bodies, error/status mapping, runtime OpenAPI | Echo/Huma test server with fake module dependencies |
| Home Assistant adapter | Subscribe-first snapshot/event reconciliation, concurrent request/response correlation, service calls, command-linked no-op refresh, reconnect | Scripted WebSocket server using captured minimal fixtures and controlled snapshot/event interleavings; one manual/live verification |
| Process | Full failure matrix and restart behavior | Native devenv processes plus simulator and disposable SQLite/NATS state |

CI regenerates sqlc output and fails on diff. OpenAPI is inspected at runtime in tests but is not committed.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Lost JetStream publish acknowledgement leaves an uncertain result | Medium | Medium | Retry identical bytes/ID within context; deduplicate by Observation ID |
| Home Assistant unchanged State has old `last_updated` | High | Medium | Separate `adapter_received_at` from optional `source_updated_at`; explicitly refresh Commands |
| Adapter clock skew misleads operators or history consumers | Medium | Low | Keep `adapter_received_at`/`source_updated_at` diagnostic-only, expose core `observed_at`, and log future-clock skew |
| Three-process tests become slow or flaky | Medium | High | Keep most cases at module/repository/SDK seams; reserve process tests for integration behavior |
| Concurrent handlers expose unsafe adapter/vendor state | Medium | High | Require concurrency safety, correlate vendor calls by request ID, test interleavings, and permit protocol-required adapter serialization |
| Generic JSON passes structural validation but violates Entity semantics | Medium | High | Resolve the registered Entity first, validate through the closed catalog before State projection or Command creation, and test invalid values and parameters at module and process seams |

## Open items

No open items remain. Live verification against the configured Home Assistant light succeeded using ignored YAML/secret files.
