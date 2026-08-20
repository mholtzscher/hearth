# First Light — Implementation Spec

**Status:** Ready for task breakdown

**Approved by:** Michael

**Approved:** 2026-08-19

**Type:** Feature plan

**Effort:** XL (relative estimate only)

## Problem statement

- **Who:** A technical self-hoster migrating one household away from Home Assistant.
- **What:** Hearthd needs its first useful vertical slice without making Home Assistant part of the permanent architecture.
- **Why it matters:** The household must remain operable during migration, while the project proves that NATS earns its production role through process isolation, durable observations, recovery, and explicit command semantics.
- **Evidence:** The current household runs through Home Assistant, and the project goal is to eliminate Home Assistant after native adapters take ownership.

## Proposed solution

Build three Go processes: the Hearthd core, a disposable Home Assistant migration adapter, and a fault-injecting simulator. A thin, stateless Go SDK hides NATS protocol mechanics for adapters. Authoritative JSON Schemas remain usable by non-Go adapters.

Adapters register configured Devices and Entities over Core NATS request/reply, publish Observations durably to JetStream, and serve ephemeral Commands over Core NATS request/reply. The core projects canonical State into SQLite, records each Command attempt and outcome there for diagnosis and future history, and exposes a loopback Echo/Huma HTTP interface. Command delivery and outcome waits remain synchronous and ephemeral; the audit record never causes replay or redispatch. Commands for the same Entity may overlap and are correlated independently by Command ID.

## Scope

### In scope

- One configured Device of kind `light`
- One writable Entity of kind `power`
- Boolean State and operation `set`
- Core HTTP read and synchronous Command endpoints
- Disposable Home Assistant migration adapter
- Simulator implementing the same adapter contract
- Durable JetStream Observation intake
- SQLite registration, current State, Observation deduplication, and Command attempt history
- Runtime JSON Schema validation
- Causal IDs and W3C trace-context propagation
- Explicit liveness and readiness endpoints

### Non-goals

- Brightness, color, transitions, or effects
- General discovery or approval
- Adapter manifests, feature negotiation, checkpoints, or a full adapter framework
- Automations, scheduling, browser UI, or history UI
- Adapter or Entity availability and heartbeats
- Durable Command delivery, replay, or recovery
- Per-Entity Command serialization, queuing, or supersession
- Authentication or non-loopback HTTP exposure
- NATS KV
- Container deployment
- Committed/generated OpenAPI artifacts
- Permanent support for the Home Assistant adapter

## Key trade-offs

| Chose | Over | Because |
| --- | --- | --- |
| Durable Observation delivery plus SQLite Command history | Durable Command stream or no Command record | JetStream recovers evidence, while a non-replayable SQLite ledger supports diagnosis and future history without executing stale intent |
| SQLite canonical projection | NATS KV or replay-only State | Only the core needs transactional query ownership |
| Thin session SDK | Raw NATS in every adapter or full runtime framework | Repeated protocol mechanics earn reuse; vendor behavior does not |
| One `devices` module | Registry/State/Commands modules | One slice does not yet justify three seams |
| Source acquisition ordering | JetStream arrival ordering | Delayed delivery is not source chronology |
| Explicit linked refresh | Acceptance-only success | Upstream acceptance does not prove the requested outcome is present |
| Concurrent Commands per Entity | Guard, queue, or supersession policy | This matches common ecosystem behavior and keeps each Command independent; interleaved outcomes and immediately superseded successes are accepted |
| Runtime-only OpenAPI | Committed generated artifact | There is no external HTTP consumer requiring compatibility review yet |

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

The Go module path is `github.com/mholtzscher/hearthd`.

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

import "time"

type DeviceID string
type EntityID string
type ObservationID string
type CommandID string
type CorrelationID string

type DeviceKind string
const DeviceKindLight DeviceKind = "light"

type EntityKind string
const EntityKindPower EntityKind = "power"

type ValueType string
const ValueTypeBoolean ValueType = "boolean"

type Operation string
const OperationSet Operation = "set"

type Device struct {
    ID   DeviceID
    Kind DeviceKind
    Name string
}

type Entity struct {
    ID         EntityID
    DeviceID   DeviceID
    AdapterID  string
    Kind       EntityKind
    ValueType  ValueType
    Writable   bool
    Operations []Operation
}

type State struct {
    EntityID        EntityID
    Value           bool
    ObservationID   ObservationID
    ObservedAt      time.Time
    SourceUpdatedAt *time.Time
    ReceivedAt      time.Time
    ReceiveOrder    int64
}

type EntityView struct {
    Entity Entity
    State  *State
}

type ObservationDisposition string
const (
    DispositionApplied   ObservationDisposition = "applied"
    DispositionUnchanged ObservationDisposition = "unchanged"
    DispositionStale     ObservationDisposition = "stale"
    DispositionRejected  ObservationDisposition = "rejected"
    DispositionDuplicate ObservationDisposition = "duplicate" // processing result; no second receipt row
)

type ObservationRejection string
const (
    RejectionFutureObservedAt ObservationRejection = "future_observed_at"
    RejectionUnknownEntity    ObservationRejection = "unknown_entity"
    RejectionWrongAdapter     ObservationRejection = "wrong_adapter"
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
    Operation            Operation
    Value                bool
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
    Value         bool
}
```

`adapter_id` records the owner selected for dispatch even if ownership changes later. `requested_at`, `accepted_at`, and `completed_at` are core-clock UTC times for the corresponding committed lifecycle transitions; `deadline_at` is exactly ten seconds after `requested_at`. The record stores normalized domain intent and stable failure codes, not raw HTTP bodies, headers, client addresses, adapter error text, or credentials.

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

Every schema uses `additionalProperties: false`. All timestamps are UTC RFC3339Nano strings ending in `Z`. `correlation_id` is required. `causation_id` is omitted for a root message.

Canonical schema files and IDs are:

| Path | Envelope `schema` and JSON Schema `$id` |
| --- | --- |
| `contracts/v1/registration-request.schema.json` | `urn:hearth:schema:registration-request:v1` |
| `contracts/v1/registration-response.schema.json` | `urn:hearth:schema:registration-response:v1` |
| `contracts/v1/observation.schema.json` | `urn:hearth:schema:observation:v1` |
| `contracts/v1/command-request.schema.json` | `urn:hearth:schema:command-request:v1` |
| `contracts/v1/command-response.schema.json` | `urn:hearth:schema:command-response:v1` |
| `contracts/v1/common.schema.json` | `urn:hearth:schema:common:v1` |

`contracts/v1/embed.go` exposes these same files through `embed.FS`. The core and SDK compile them with `jsonschema/v6` at startup; non-Go adapters consume the JSON files directly.

### Registration payloads

Owner: local wire DTOs in `sdk/adapter/types.go` and `internal/platform/nats/wire.go`.

```go
type Registration struct {
    BindingKey string             `json:"binding_key"`
    Device     DeviceDescriptor   `json:"device"`
    Entities   []EntityDescriptor `json:"entities"`
}

type DeviceDescriptor struct {
    ExternalID *string `json:"external_id,omitempty"`
    Name       string  `json:"name"`
    Kind       string  `json:"kind"` // const "light" in v1
}

type EntityDescriptor struct {
    Key        string   `json:"key"`
    ExternalID string   `json:"external_id"`
    Name       string   `json:"name"`
    Kind       string   `json:"kind"`       // const "power"
    ValueType  string   `json:"value_type"` // const "boolean"
    Writable   bool     `json:"writable"`   // const true
    Operations []string `json:"operations"` // exactly ["set"]
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
```

Constraints:

- `binding_key` and Entity `key` use the slug pattern.
- Device and Entity names contain 1–128 Unicode characters.
- External IDs contain 1–256 characters when present.
- `entities` contains exactly one Entity in this slice; its key is unique within the binding.
- Re-registration with the same adapter ID, binding key, and Entity key returns the same canonical IDs.
- A conflicting external mapping rejects the whole registration transaction.

### Observation payload

```go
type Observation struct {
    EntityID           string  `json:"entity_id"`
    Value              bool    `json:"value"`
    ObservedAt         string  `json:"observed_at"`
    SourceUpdatedAt    *string `json:"source_updated_at,omitempty"`
    RefreshForCommand  *string `json:"refresh_for_command_id,omitempty"`
}
```

- `observed_at` is when the adapter freshly acquired the value.
- `source_updated_at` preserves an optional upstream last-change claim.
- A command refresh carries both `refresh_for_command_id = cmd_...` and envelope `causation_id = cmd_...`, and reuses the Command correlation ID.
- The SDK generates the `obs_...` envelope ID once per `PublishObservation` call.

### Command payloads

```go
type Command struct {
    ID            string
    CorrelationID string
    EntityID      string `json:"entity_id"`
    Operation     string `json:"operation"` // const "set"
    Value         bool   `json:"value"`
    Deadline      string `json:"deadline"`
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

The Command ID is the request envelope ID. `error` is required only for `rejected` and omitted for `accepted`. Error messages are safe for operator display and at most 512 characters.

### HTTP types

Owner: `internal/modules/devices/api/types.go`.

```go
type EntityBody struct {
    ID         string     `json:"id"`
    DeviceID   string     `json:"device_id"`
    Kind       string     `json:"kind"`
    ValueType  string     `json:"value_type"`
    Writable   bool       `json:"writable"`
    Operations []string   `json:"operations"`
    State      *StateBody `json:"state"`
}

type StateBody struct {
    Value           bool    `json:"value"`
    ObservationID   string  `json:"observation_id"`
    ObservedAt      string  `json:"observed_at"`
    SourceUpdatedAt *string `json:"source_updated_at,omitempty"`
    ReceivedAt      string  `json:"received_at"`
}

type SetCommandBody struct {
    Operation string `json:"operation"` // const "set"
    Value     bool   `json:"value"`
}

type CommandResultBody struct {
    CommandID     string `json:"command_id"`
    Status        string `json:"status"` // const "satisfied"
    ObservationID string `json:"observation_id"`
    Value         bool   `json:"value"`
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

Protocol limits are fixed constants in v1, not YAML fields: ten-second Command deadline, one-minute future skew, seven-day/one-GiB stream, 30-second acknowledgement wait, one pending acknowledgement, and at least eight-day receipt retention with current-State receipt pinning. Command records are retained without pruning in the first slice; a bounded retention policy is deferred until a history interface establishes its requirements.

## Interfaces

### Adapter SDK

Owner: `sdk/adapter`.

```go
func Connect(context.Context, Config) (*Session, error)
func (*Session) Register(context.Context, Registration) (Binding, error)
func (*Session) PublishObservation(context.Context, Observation) (ObservationID, error)
func (*Session) ServeCommands(context.Context, CommandHandler) error
func (*Session) Close() error

type CommandHandler func(context.Context, Command, Responder) error

type Responder interface {
    Accept() error
    Reject(code, message string) error
}
```

Interface contract:

- `Connect` validates the adapter slug, compiles embedded schemas, connects to NATS, and installs W3C propagation. It does not provision core-owned streams.
- `Register` performs one request/reply attempt. The adapter application retries it with bounded exponential backoff until context cancellation.
- `PublishObservation` generates one Observation envelope and retries that same bytes/ID across transient NATS disconnects. It returns the generated ID after JetStream publish acknowledgement, or returns that ID with an error when the context expires. There is no local outbox.
- `ServeCommands` subscribes to the adapter-scoped wildcard, starts an independent handler invocation for each valid request, and blocks until context cancellation or terminal serving failure. Handler invocations may overlap, including for the same Entity, so adapter code must be concurrency-safe. An adapter may serialize internally when its vendor protocol requires it, but the SDK and core provide no ordering guarantee.
- A `Responder` is one-shot. `Accept` or `Reject` sends the Core NATS reply. A second reply returns `ErrAlreadyResponded`. Returning without a reply returns/logs `ErrMissingResponse` and lets the core request time out.
- `Close` is idempotent and drains subscriptions within the caller's shutdown budget.
- Vendor calls, credentials, polling, state refresh, mapping, and checkpoints remain outside the SDK.

### Devices module

Owner: `internal/modules/devices/service.go`.

```go
type Repository interface {
    RegisterBinding(context.Context, RegisterBindingParams) (Binding, error)
    GetEntityView(context.Context, EntityID) (EntityView, error)
    CreateCommand(context.Context, CommandRecord) error
    MarkCommandAccepted(context.Context, CommandID, time.Time) error
    CompleteCommand(context.Context, CommandCompletion) error
    InterruptActiveCommands(context.Context, time.Time) error
    ProjectObservation(context.Context, ProjectObservationParams) (ProjectionResult, error)
    DeleteExpiredObservationReceipts(context.Context, time.Time) error
}

type CommandSender interface {
    Send(context.Context, string, CommandRequest) (CommandAcceptance, error)
}

func NewService(Repository, CommandSender, Dependencies) *Service
func (*Service) Register(context.Context, string, Registration) (Binding, error)
func (*Service) ProjectObservation(context.Context, string, Observation, time.Time) (ProjectionResult, error)
func (*Service) GetEntity(context.Context, EntityID) (EntityView, error)
func (*Service) SetPower(context.Context, EntityID, bool) (CommandResult, error)
```

`Dependencies` contains an injected clock and UUIDv7 generator for deterministic module tests. Interfaces are defined in the consuming `devices` package; production sqlc/NATS adapters and test adapters satisfy them.

`SetPower` behavior:

1. Resolve Entity and owning adapter.
2. Create a `cmd_` ID, correlation ID, and independent ten-second lifecycle context.
3. Register a Command-ID-keyed linked-Observation waiter before dispatch.
4. Commit a `requested` Command record before attempting NATS dispatch; a creation failure prevents dispatch and returns an internal error. This commit is the point of no return: subsequent HTTP cancellation does not cancel dispatch or the independent lifecycle.
5. Send Core NATS request/reply.
6. Persist `adapter_unavailable`, `rejected`, or `internal_failure` when dispatch or the adapter response fails; never retry or replay the Command.
7. On acceptance, set `accepted_at` without regressing a terminal status if a fast linked Observation was already committed.
8. Wait for `ProjectObservation` to commit an `applied` or `unchanged` matching Observation linked to the Command and transactionally mark only that record `satisfied`.
9. At the deadline, persist `outcome_timeout` unless the Command is already terminal.
10. Return `CommandResult` or the mapped stable error.
11. If HTTP cancellation occurs after the `requested` record commits, abandon the response but retain that lifecycle goroutine until linked outcome or deadline, recording the eventual terminal status.
12. Unregister the Command-ID-keyed waiter at lifecycle completion. Core restart loses all in-memory waits; startup marks persisted `requested` and `accepted` records `interrupted` and never redispatches them.

Command lifecycle writes are monotonic and idempotent. A linked Observation may be projected before the core processes the adapter's acceptance reply; satisfaction wins, and a later acceptance update may fill `accepted_at` but must not replace the terminal status.

Multiple `SetPower` calls for one Entity run concurrently without a guard, queue, or supersession. Each has an independent ID, record, waiter, and deadline. An Observation can satisfy only the Command named by its `refresh_for_command_id`; it cannot satisfy another active Command for the same Entity. Successful Commands may be immediately superseded by later Observations, and canonical State continues to follow the normal `observed_at` plus receive-order rules.

### HTTP

Owner: `internal/modules/devices/api`.

```http
GET /v1/entities/{entity_id}
POST /v1/entities/{entity_id}/commands
GET /healthz
GET /readyz
```

`GET /v1/entities/{entity_id}` returns HTTP 200 with `EntityBody`; `state` is `null` before the first accepted Observation. Unknown IDs return 404.

`POST /v1/entities/{entity_id}/commands` accepts:

```json
{"operation":"set","value":true}
```

It waits synchronously for a linked outcome or deadline. Status mapping is:

| Condition | HTTP | Error code |
| --- | ---: | --- |
| Invalid body, ID, operation | 400 | `invalid_request` |
| Unknown Entity | 404 | `entity_not_found` |
| No adapter responder | 503 | `adapter_unavailable` |
| Adapter rejects upstream call | 502 | `upstream_rejected` |
| No matching linked Observation by deadline | 504 | `outcome_timeout` |
| Internal failure | 500 | `internal_error` |

Echo v5 and Huma v2 are constructed by `internal/app/hearthd`. The `devices/api` package registers its own operations with stable operation IDs. `/healthz` and `/readyz` are plain loopback Echo routes. Huma exposes OpenAPI at runtime; no generated OpenAPI file is committed. The first slice persists Command history but exposes no Command-history HTTP operation or history UI.

### NATS protocol

```text
hearth.v1.adapter.<adapter>.register
hearth.v1.adapter.<adapter>.observation.<entity>
hearth.v1.adapter.<adapter>.command.<entity>.set
```

- `<adapter>` is the configured adapter slug.
- `<entity>` is the canonical Entity ID.
- Core validates that subject adapter/entity tokens match payload and current ownership.
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

The core acknowledges applied, unchanged, stale, duplicate, and durably recorded rejected Observations only after the SQLite transaction commits. Schema-invalid input is logged with subject, stream sequence, validation error, and safe metadata, then acknowledged. Transient SQLite/infrastructure errors remain unacknowledged.

## Persistence

Owner: `internal/platform/db` for migration/query/generated code; `internal/modules/devices/repository.go` for domain mapping and transaction behavior.

SQLite uses `modernc.org/sqlite`, foreign keys, WAL mode, a five-second busy timeout, and one writer connection. Goose migration `internal/platform/db/migrations/00001_initial.sql` creates:

```sql
-- +goose Up
PRAGMA foreign_keys = ON;

CREATE TABLE devices (
    id         TEXT PRIMARY KEY CHECK (id LIKE 'dev_%'),
    kind       TEXT NOT NULL CHECK (kind = 'light'),
    name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE entities (
    id         TEXT PRIMARY KEY CHECK (id LIKE 'ent_%'),
    device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind = 'power'),
    value_type TEXT NOT NULL CHECK (value_type = 'boolean'),
    writable   INTEGER NOT NULL CHECK (writable = 1),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE entity_operations (
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    operation TEXT NOT NULL CHECK (operation = 'set'),
    PRIMARY KEY (entity_id, operation)
);

CREATE TABLE commands (
    id                     TEXT PRIMARY KEY CHECK (id LIKE 'cmd_%'),
    entity_id              TEXT NOT NULL REFERENCES entities(id) ON DELETE RESTRICT,
    adapter_id             TEXT NOT NULL CHECK (length(adapter_id) BETWEEN 1 AND 63),
    operation              TEXT NOT NULL CHECK (operation = 'set'),
    value_boolean          INTEGER NOT NULL CHECK (value_boolean IN (0, 1)),
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

CREATE TABLE observation_receipts (
    receive_order  INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id TEXT NOT NULL UNIQUE CHECK (observation_id LIKE 'obs_%'),
    adapter_id     TEXT NOT NULL,
    entity_id      TEXT NOT NULL,
    disposition    TEXT NOT NULL CHECK (
        disposition IN ('applied', 'unchanged', 'stale', 'rejected')
    ),
    rejection_code TEXT CHECK (
        rejection_code IS NULL OR rejection_code IN (
            'future_observed_at', 'unknown_entity', 'wrong_adapter'
        )
    ),
    observed_at    TEXT NOT NULL,
    received_at    TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    CHECK (
        (disposition = 'rejected' AND rejection_code IS NOT NULL)
        OR (disposition <> 'rejected' AND rejection_code IS NULL)
    )
);

CREATE INDEX observation_receipts_expiry_idx
    ON observation_receipts(expires_at);

CREATE TABLE entity_states (
    entity_id         TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    observation_id    TEXT NOT NULL UNIQUE REFERENCES observation_receipts(observation_id),
    value_boolean     INTEGER NOT NULL CHECK (value_boolean IN (0, 1)),
    observed_at       TEXT NOT NULL,
    source_updated_at TEXT,
    received_at       TEXT NOT NULL,
    receive_order     INTEGER NOT NULL UNIQUE REFERENCES observation_receipts(receive_order)
);

-- +goose Down
DROP TABLE entity_states;
DROP INDEX observation_receipts_expiry_idx;
DROP TABLE observation_receipts;
DROP TABLE adapter_entity_mappings;
DROP TABLE adapter_bindings;
DROP INDEX commands_entity_requested_idx;
DROP TABLE commands;
DROP TABLE entity_operations;
DROP TABLE entities;
DROP TABLE devices;
```

Strict typed-ID, slug, and UTC timestamp parsing occurs at the module/transport edge; SQLite reinforces prefixes, enums, booleans, ownership uniqueness, and foreign keys.

Required sqlc queries, organized by `registration.sql`, `state.sql`, `receipts.sql`, and `commands.sql`, are:

```text
GetBinding
GetBindingByExternalDeviceID
CreateDevice
UpdateDeviceDescriptor
CreateEntity
UpdateEntityDescriptor
UpsertEntityOperation
CreateBinding
UpdateBindingExternalID
GetEntityMapping
GetEntityMappingByExternalID
CreateEntityMapping
UpdateEntityMappingExternalID
GetEntityView
GetEntityState
CreateCommand
GetCommand
MarkCommandAccepted
CompleteCommand
SatisfyCommandFromObservation
InterruptActiveCommands
GetObservationReceipt
InsertObservationReceipt
UpsertEntityState
DeleteExpiredObservationReceipts
```

The pruning query excludes the receipt referenced by current State:

```sql
-- name: DeleteExpiredObservationReceipts :exec
DELETE FROM observation_receipts
WHERE expires_at < ?
  AND NOT EXISTS (
      SELECT 1
      FROM entity_states
      WHERE entity_states.observation_id = observation_receipts.observation_id
  );
```

Checking `observation_id` protects both foreign keys because `entity_states.observation_id` and `entity_states.receive_order` reference the same receipt row. After a newer Observation advances State, the superseded receipt becomes eligible for the next pruning pass.

`RegisterBinding` and `ProjectObservation` own their complete SQLite transactions behind the repository interface. `ProjectObservation` updates a matching active Command to `satisfied` in the same transaction as its receipt and State projection. Command creation and all other lifecycle transitions also remain behind the repository interface. Generated sqlc types never cross the repository seam.

`outcome_observation_id` deliberately has no foreign key to `observation_receipts`: receipts are pruned after their idempotency window, while Command history is retained. It remains a typed immutable identifier, and satisfaction writes it only after inserting the corresponding receipt in the same transaction.

### Observation projection rules

For each schema-valid Observation, one transaction:

1. Return `duplicate` if the Observation ID already has a receipt.
2. Resolve Entity and adapter ownership.
3. Record `rejected/future_observed_at` if `observed_at` is more than one minute ahead of core time.
4. Record `rejected/unknown_entity` or `rejected/wrong_adapter` for identity failures.
5. Compare `observed_at`; use new `receive_order` as the equal-time tie-breaker.
6. Record `stale` without changing State when older.
7. Record `unchanged` and advance State evidence/timestamps for a newer same value.
8. Record `applied` and replace State for a newer different value.
9. For an `applied` or `unchanged` Observation carrying `refresh_for_command_id`, find an active `requested` or `accepted` Command for the same Entity and adapter. When the observed value matches its requested value, mark it `satisfied`, set `completed_at` and `outcome_observation_id`, and return the satisfied result for the in-memory waiter. A stale, rejected, wrong-value, unknown, or already-terminal Command link never satisfies an outcome.
10. Set receipt expiry to `received_at + 192h`.
11. Commit the receipt, State projection, and any Command satisfaction atomically before JetStream acknowledgement.

A startup task and hourly task delete expired receipts that are not referenced by current State. A current-State receipt remains past its nominal expiry until a newer Observation supersedes it.

## Home Assistant adapter

Owner: `internal/adapters/homeassistant`.

The adapter uses Home Assistant's WebSocket API directly:

1. Read per-process YAML and token file.
2. Connect and authenticate.
3. Register the configured binding through the SDK; retry with bounded exponential backoff and jitter until shutdown.
4. Call `get_states` after startup and reconnect.
5. Publish the configured light's power Observation.
6. Subscribe to `state_changed`.
7. Map `on`/`off` to boolean; reject unsupported/unavailable upstream values rather than inventing State.
8. For `set=true`, call `light.turn_on`; for `set=false`, call `light.turn_off`.
9. Reject the Command if the Home Assistant call fails.
10. Accept after Home Assistant reports successful service-call completion.
11. Explicitly call `get_states`, then durably publish a linked refresh Observation.

`observed_at` is adapter acquisition time. Home Assistant `last_updated` becomes optional `source_updated_at`. Home Assistant entity IDs, service names, contexts, and payloads do not leave this package. WebSocket request IDs, pending responses, and refresh publications are concurrency-safe: overlapping handlers may issue service calls and `get_states` requests concurrently, and each refresh Observation retains its own Command ID and correlation ID. The adapter does not serialize by Entity unless a demonstrated Home Assistant protocol constraint requires it. The adapter is deleted after migration completes.

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
4. Delete expired Observation receipts not referenced by current State.
5. Connect to NATS.
6. Idempotently provision/validate the stream and durable consumer.
7. Start the Observation consumer.
8. Construct the `devices` module and Echo/Huma transport.
9. Listen on loopback.

`GET /healthz` returns 200 whenever the HTTP process can serve. `GET /readyz` returns 200 only while SQLite responds, NATS is connected, JetStream resources match required configuration, and the Observation consumer is active; otherwise 503.

Adapters do not depend on process startup order. They reconnect to NATS and retry registration until the core responds.

## Project layout

```text
cmd/
├── hearthd/                         # new — thin core executable entry point
├── hearth-adapter-homeassistant/    # new — thin disposable-adapter entry point
└── hearth-simulator/                # new — thin simulator entry point
internal/
├── app/
│   ├── hearthd/                     # new — core composition, HTTP, lifecycle
│   ├── homeassistant/               # new — migration-adapter composition
│   └── simulator/                   # new — simulator composition
├── modules/
│   └── devices/
│       ├── api/                     # new — Huma operations and transport mapping
│       ├── model.go                 # new — Device, Entity, State, Command types
│       ├── service.go               # new — registration, projection, Command behavior
│       ├── repository.go            # new — identity, State, receipt, and Command-record persistence seam
│       ├── command.go               # new — durable lifecycle transitions plus concurrent in-memory outcome waits
│       └── errors.go                # new — domain errors
├── adapters/
│   ├── homeassistant/               # new — WebSocket client and HA mapping
│   └── simulator/                   # new — simulated external behavior and faults
└── platform/
    ├── config/                      # new — strict per-process YAML loading
    ├── db/
    │   ├── migrations/              # new — Goose SQLite migrations
    │   ├── queries/                 # new — sqlc query sources
    │   └── sqlc/                    # new/generated — query package
    └── nats/                        # new — core NATS/JetStream transport
sdk/
└── adapter/                         # new — public, stateless Go session facade
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
go.mod                                # new — github.com/mholtzscher/hearthd

go.sum                                # new — pinned Go dependency graph
sqlc.yaml                             # new — root SQLite generation config
```

No `api/openapi.yaml` is created. Huma's runtime OpenAPI document is covered by transport tests.

## Deliverables

| ID | Deliverable | Effort | Depends on |
| --- | --- | --- | --- |
| D1 | Go/devenv foundation, dependency lock, typed IDs, embedded JSON Schemas, YAML loaders | L | - |
| D2 | Stateless adapter SDK, NATS subjects/envelopes, schema validation, registration/Observation/Command contract tests | L | D1 |
| D3 | SQLite migration/sqlc layer, Command ledger, and idempotent binding registration in `devices` | L | D1, D2 |
| D4 | JetStream provisioning, durable Observation projection, receipt pruning, GET Entity, readiness | XL | D2, D3 |
| D5 | Audited ephemeral Command orchestration, POST endpoint, simulator happy path and complete failure matrix | XL | D4 |
| D6 | Disposable Home Assistant WebSocket adapter and real-light verification | L | D2, D5 |
| D7 | Broad checks, runtime OpenAPI assertions, recovery tests, docs reconciliation | L | D1–D6 |

Total relative effort is **XL**. No calendar estimate is asserted.

## Acceptance criteria

- [ ] `devenv test` (or the documented equivalent task) runs generation checks, tests, vet, and schema/OpenAPI assertions from a clean checkout.
- [ ] Every authoritative JSON Schema compiles and cross-binary fixtures validate.
- [ ] Repeating registration with the same adapter/binding/Entity keys returns identical canonical IDs.
- [ ] Conflicting binding and external mappings reject atomically without partial rows.
- [ ] A registered but unobserved Entity returns HTTP 200 with `state: null`.
- [ ] A JetStream-acknowledged Observation survives a core restart and projects after consumer recovery.
- [ ] Exact redelivery changes neither State nor receipt count.
- [ ] Receipt pruning deletes expired unreferenced receipts, retains the receipt backing current State without a foreign-key error, and deletes it after State advances.
- [ ] Older, same-value, future-skewed, wrong-owner, unknown-Entity, and malformed Observations produce the specified dispositions/diagnostics and acknowledgement behavior.
- [ ] A newer same-value Observation advances State timestamps and evidence.
- [ ] Every dispatched Command has a committed `requested` record first; failure to create that record prevents dispatch.
- [ ] Two simultaneous Commands for one Entity create distinct durable records, both dispatch without HTTP 409, and each can be satisfied only by its own linked Observation; the SDK invokes both handlers independently without imposing per-Entity ordering.
- [ ] Controlled opposite-value interleavings prove that both Commands may be satisfied at different observation times or one may time out when its linked Observation is stale or mismatched; final State follows `observed_at` and receive-order rules.
- [ ] Missing adapter, upstream rejection, and unsatisfied deadline persist the matching terminal status and stable failure code while mapping to HTTP 503, 502, and 504; unexpected failures after record creation persist `internal_failure` when SQLite remains writable and map to HTTP 500.
- [ ] An already-matched target is still dispatched, accepted, explicitly refreshed, and satisfied only by its linked Observation; the State projection and `satisfied` Command record reference the same Observation in one transaction.
- [ ] A linked Observation that wins the race with acceptance processing may satisfy the Command, and the later acceptance update records `accepted_at` without regressing its status.
- [ ] HTTP disconnect after the `requested` record commits does not cancel dispatch; its independent lifecycle remains active until linked outcome or deadline and records the eventual terminal status.
- [ ] Core restart marks `requested` and `accepted` Commands `interrupted`, never redispatches them, and does not let a later linked Observation change that terminal status even though the Observation may still advance State.
- [ ] Core restart after SQLite commit of State and Command satisfaction but before JetStream acknowledgement redelivers harmlessly without changing either record.
- [ ] Simulator passes duplicate, stale, malformed, unavailable-adapter, upstream-rejection, no-op-refresh, overlapping-opposite-command, outcome-timeout, interrupted-command, and restart-before-ack scenarios.
- [ ] The configured real Home Assistant light can be read and set both on and off through Hearthd, with each HTTP 200 tied to a matching linked Observation.
- [ ] Core and wire fixtures contain no Home Assistant service names or payload shapes.
- [ ] HTTP listens only on configured loopback and `/readyz` fails when SQLite, NATS, JetStream configuration, or the consumer is unavailable.

## Test strategy

| Layer | What | How |
| --- | --- | --- |
| Pure module | ID validation, registration conflicts, projection ordering, same-value advancement, Command transition monotonicity, concurrent waiter isolation, interleaved outcomes, deadline/result mapping | Inject in-memory Repository, CommandSender, clock, and ID generator |
| Repository | Transactions, constraints, idempotency, tie ordering, receipt pruning, current-State retention, Command lifecycle persistence, atomic outcome satisfaction, startup interruption | Temporary real SQLite; apply Goose; use generated sqlc queries |
| SDK/NATS | Subjects, envelopes, schema validation, publish acknowledgement/retry, concurrent Command handler invocation, request/reply, responder invariants, W3C headers | In-process NATS Server with JetStream |
| HTTP | Huma validation, operation IDs, nullable State, bodies, error/status mapping, runtime OpenAPI | Echo/Huma test server with fake module dependencies |
| Home Assistant adapter | Snapshot/event mapping, concurrent request/response correlation, service calls, command-linked no-op refresh, reconnect | Scripted WebSocket server using captured minimal fixtures and controlled interleavings; one manual/live verification |
| Process | Full failure matrix and restart behavior | Native devenv processes plus simulator and disposable SQLite/NATS state |

CI regenerates sqlc output and fails on diff. OpenAPI is inspected at runtime in tests but is not committed.

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Lost JetStream publish acknowledgement creates an uncertain adapter result | Medium | Medium | SDK retries identical envelope bytes/ID within context; core deduplicates by Observation ID |
| Home Assistant unchanged state has old `last_updated` | High | Medium | Separate adapter acquisition `observed_at` from optional `source_updated_at`; explicit command refresh |
| Adapter/core clock skew rejects valid input or poisons ordering | Medium | Medium | One-minute future bound, structured rejection diagnostics, preserve all three times |
| Three-process failure tests become slow or flaky | Medium | High | Keep most risk tests at module/repository/SDK seams; reserve a small process suite for integration-only behavior |
| Thin SDK expands into vendor framework | Medium | Medium | Freeze interface to registration, durable publication, command serving, and shutdown; keep vendor behavior outside |
| Home Assistant details leak into canonical contracts | Medium | High | Schema fixtures and package tests assert canonical Device/Entity/State/Command vocabulary only |
| Concurrent SDK handlers expose unsafe adapter or vendor-client state | Medium | High | Require concurrency-safe adapters, correlate upstream calls by request ID, test interleavings, and let adapters serialize only where their protocol requires it |
| Persisted Command history is mistaken for a delivery queue | Medium | High | Keep delivery on expiring Core NATS request/reply, mark active rows interrupted at startup, and forbid replay or redispatch |
| Concurrent Commands produce timing-dependent outcomes or immediately superseded successes | Accepted | Medium | Keep Command IDs, records, waiters, and linked Observations independent; order State by observation chronology and expose the full history rather than implying serialization |
| Core restart loses in-memory waiters | Accepted | Low for a light | Persist each attempt as interrupted for diagnosis; later Observations still update State; clients retry manually |
| Command satisfaction races adapter acceptance processing | Medium | Medium | Make transitions monotonic; allow a matching linked Observation to satisfy an active record and let later acceptance fill its timestamp without status regression |

## Success metrics

- The entire simulator failure matrix passes deterministically.
- One real configured light can be read and controlled on/off through Hearthd.
- Every Command attempt has a durable terminal history record, and every successful HTTP Command is tied to the matching command-linked Observation in both State and Command history.
- Core restart marks unfinished Commands interrupted without redispatch, and JetStream redelivery does not corrupt or regress canonical State or Command history.
- Home Assistant can later be removed without changing canonical Device/Entity IDs or the adapter wire contract.

## Open items

No blocking design questions remain. Before live verification, the repository owner supplies local Home Assistant URL, token file, Device identifier when available, Entity ID, display names, and binding key through ignored YAML/secret files.
