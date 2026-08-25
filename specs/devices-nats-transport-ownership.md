# Devices NATS Transport Ownership — Implementation Spec

**Status:** Ready for task breakdown  
**Type:** Refactoring  
**Effort:** L (1–2 days, 80% confidence)  
**Approved by:** User design confirmation  
**Date:** 2026-08-25  
**Baseline:** `main` at `d5c8a34`

## Problem Statement

`internal/platform/nats` is not domain-neutral infrastructure: it defines Hearth v1 Registration, Observation, and Command routing and observation-specific JetStream policy. Core wire/domain translation also lives in `internal/app/hearthd/run.go`, although `docs/architecture.md` assigns transport mapping to the `devices` product module.

The SDK independently implements the same envelope, subject, codec, and trace mechanics. Its local payload DTOs are intentional: ADR 0011 makes the checked-in JSON Schemas, not shared Go payload types, the compatibility boundary. The duplicated low-level mechanics are not intentional.

This refactor corrects ownership without changing observable behavior.

## Decision

Create two explicit owners:

- `internal/contracts/v1/natswire` owns the internal Go implementation of mechanics shared by both sides of the Hearth v1 NATS contract: envelope encoding, schema-validated codecs, subject construction/parsing, and W3C trace-context headers.
- `internal/modules/devices/nats` owns core-side device NATS behavior: private core payload DTOs, wire/domain translation, command sending, registration serving, observation consumption, and observation JetStream policy.

`internal/app/hearthd` continues to create the NATS connection and JetStream client, compile the schema validator, assemble dependencies and readiness, and coordinate shutdown. It no longer constructs or translates wire payloads.

`sdk/adapter` retains its exported interface and local payload DTOs while replacing its hidden shared mechanics with `natswire`. The simulator's intentional raw fault scenarios may also use `natswire` directly.

Delete `internal/platform/nats` without a forwarding package or compatibility aliases and deliver the change atomically.

## Scope and Deliverables

| ID | Deliverable | Effort | Depends On |
|---|---|---:|---|
| D1 | Add the internal shared v1 NATS mechanics package | M | — |
| D2 | Move and deepen the core device NATS transport | M | D1 |
| D3 | Rewire `hearthd`, SDK, simulator, readiness, and integration harnesses | M | D1, D2 |
| D4 | Preserve compatibility coverage and update ownership documentation | M | D1–D3 |

Unless specified as an ownership change, preserve schemas and IDs, subjects and payloads, JetStream policy, transport semantics and errors, timing and concurrency, logging fields, readiness and shutdown, configuration, and every exported `sdk/adapter` API. Core and SDK payload DTOs remain separate and schema-governed. Connection creation and process lifecycle remain in `hearthd`.

Dependency flows from `internal/modules/devices/nats` to the parent `devices` package. The parent package must not import `devices/nats`, `nats.go`, or JetStream or expose their types in its interface.

## Types

### Shared contract mechanics

Owner: `internal/contracts/v1/natswire`.

`Envelope` is an internal implementation type; the JSON Schemas in `contracts/v1` remain authoritative. This package defines no Registration, Observation, Command, Binding, or response payload DTO.

```go
package natswire

type Envelope[T any] struct {
    ID            string  `json:"id"`
    Schema        string  `json:"schema"`
    EmittedAt     string  `json:"emitted_at"`
    CorrelationID string  `json:"correlation_id"`
    CausationID   *string `json:"causation_id,omitempty"`
    Data          T       `json:"data"`
}

type RegistrationRoute struct {
    AdapterID string
}

type ObservationRoute struct {
    AdapterID string
    EntityID  string
}

type CommandRoute struct {
    AdapterID     string
    EntityID      string
    OperationName string
}
```

### Private core wire DTOs

Owner: `internal/modules/devices/nats/wire.go`.

The type names are unexported. Their fields remain exported for `encoding/json`.

```go
package nats

import "encoding/json"

type registration struct {
    BindingKey string             `json:"binding_key"`
    Device     deviceDescriptor   `json:"device"`
    Entities   []entityDescriptor `json:"entities"`
}

type deviceDescriptor struct {
    ExternalID *string `json:"external_id,omitempty"`
    Name       string  `json:"name"`
    Kind       string  `json:"kind"`
}

type entityDescriptor struct {
    Key        string          `json:"key"`
    ExternalID string          `json:"external_id"`
    Name       string          `json:"name"`
    Type       string          `json:"type"`
    Support    json.RawMessage `json:"support"`
}

type binding struct {
    BindingKey string          `json:"binding_key"`
    DeviceID   string          `json:"device_id"`
    Entities   []entityBinding `json:"entities"`
}

type entityBinding struct {
    Key      string `json:"key"`
    EntityID string `json:"entity_id"`
}

type registrationResponse struct {
    Status  string             `json:"status"`
    Binding *binding           `json:"binding,omitempty"`
    Error   *registrationError `json:"error,omitempty"`
}

type registrationError struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

type observation struct {
    EntityID          string          `json:"entity_id"`
    Value             json.RawMessage `json:"value"`
    AdapterReceivedAt string          `json:"adapter_received_at"`
    SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
    RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

type command struct {
    EntityID      string          `json:"entity_id"`
    OperationName string          `json:"operation"`
    Parameters    json.RawMessage `json:"parameters"`
    Deadline      string          `json:"deadline"`
}

type commandResponse struct {
    CommandID string        `json:"command_id"`
    Status    string        `json:"status"`
    Error     *commandError `json:"error,omitempty"`
}

type commandError struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}
```

Domain, HTTP, persistence, configuration, schema, and exported SDK types do not change. `RegistrationServer` and `ObservationConsumer` move to `internal/modules/devices/nats` with their lifecycle semantics unchanged. The observation resource constants retain their names, values, and exported visibility:

```go
const (
    ObservationStreamName           = "HEARTH_OBSERVATIONS_V1"
    ObservationConsumerName         = "hearthd-state-v1"
    ObservationStreamMaxAge         = 7 * 24 * time.Hour
    ObservationStreamMaxBytes int64 = 1 << 30
    ObservationAckWait              = 30 * time.Second
)
```

## Interfaces

### Shared `natswire` interface

Owner: `internal/contracts/v1/natswire`.

```go
package natswire

func Encode[T any](
    validator *contractsv1.Validator,
    schemaID string,
    envelope Envelope[T],
) ([]byte, error)

func Decode[T any](
    validator *contractsv1.Validator,
    schemaID string,
    payload []byte,
) (Envelope[T], error)

func RegistrationWildcard() string
func RegistrationSubject(adapterID string) (string, error)
func ObservationWildcard() string
func ObservationSubject(adapterID, entityID string) (string, error)
func CommandSubject(adapterID, entityID, operationName string) (string, error)
func CommandWildcard(adapterID string) (string, error)
func ParseRegistrationSubject(subject string) (RegistrationRoute, error)
func ParseObservationSubject(subject string) (ObservationRoute, error)
func ParseCommandSubject(subject string) (CommandRoute, error)

func InjectTrace(ctx context.Context, headers natsgo.Header)
func ExtractTrace(ctx context.Context, headers natsgo.Header) context.Context
```

`Encode` marshals and then validates against `schemaID`; `Decode` validates and then unmarshals. Subject validation and error text remain equivalent to the current core implementation. Trace helpers use W3C `TraceContext` and keep the `propagation.TextMapCarrier` private. The package does not own connections, validators, publication, subscriptions, retries, logging, or JetStream resources.

### Core domain-facing seams

Owner: `internal/modules/devices/nats`. Interfaces live in the consuming transport package and contain only invoked methods; `*devices.Service` satisfies both unchanged.

```go
package nats

type Registrar interface {
    Register(
        context.Context,
        string,
        devices.Registration,
    ) (devices.Binding, error)
}

type ObservationProjector interface {
    ProjectObservation(
        context.Context,
        string,
        devices.Observation,
        time.Time,
    ) (devices.ProjectionResult, error)
}
```

The command transport implements the existing `devices.CommandSender` seam:

```go
package nats

type CommandSender struct {
    connection *natsgo.Conn
    validator  *contractsv1.Validator
}

func NewCommandSender(
    connection *natsgo.Conn,
    validator *contractsv1.Validator,
) *CommandSender

func (sender *CommandSender) Send(
    ctx context.Context,
    adapterID string,
    request devices.CommandRequest,
) (devices.CommandAcceptance, error)
```

Lifecycle and resource interfaces remain focused:

```go
package nats

func StartRegistrationServer(
    connection *natsgo.Conn,
    validator *contractsv1.Validator,
    registrar Registrar,
    logger *slog.Logger,
) (*RegistrationServer, error)

func (server *RegistrationServer) Drain() error

func StartObservationConsumer(
    baseContext context.Context,
    consumer jetstream.Consumer,
    validator *contractsv1.Validator,
    projector ObservationProjector,
    logger *slog.Logger,
) (*ObservationConsumer, error)

func (consumer *ObservationConsumer) Active() bool
func (consumer *ObservationConsumer) Stop()
func (consumer *ObservationConsumer) Drain()
func (consumer *ObservationConsumer) Closed() <-chan struct{}

func ProvisionObservationResources(
    context.Context,
    jetstream.JetStream,
) (jetstream.Consumer, error)

func ValidateObservationResources(
    context.Context,
    jetstream.JetStream,
) error
```

### SDK compatibility

`sdk/adapter` retains its current exported source interface, including `Config`, `Session`, payload and responder types, rejection and validation types, and exported errors. It removes its private envelope, subject builders/parsers and validation, header carrier, and stored propagator in favor of `natswire`.

Connection creation, JetStream publication retry, command concurrency and responses, ID generation, logging, and transient error classification remain in the SDK. Existing validation still makes shared subject errors impossible; handle them without changing caller-visible error classification or text.

## Behavioral Contracts

### Registration

- Subscribe to the current wildcard and flush before reporting startup success.
- Preserve validation and logging order: discard missing reply subjects, malformed routes, invalid schema payloads, and caused root registrations as today.
- Extract incoming W3C trace context before invoking `Registrar` and inject it into replies.
- Map strings to `devices.DeviceKind` and `devices.EntityTypeID`; deep-copy optional external IDs and `devices.EntitySupport` JSON.
- Map accepted bindings to wire strings. Map `devices.RegistrationRejectedError` to a schema-valid `rejected` response rather than a server error.
- Preserve correlated reply ID generation, log messages and attributes, drain behavior, and closed-connection handling.

### Observations

- Preserve metadata and stream-sequence extraction, schema validation, route/payload and `Nats-Msg-Id` matching, causation/link checks, timestamp validation, trace extraction, and their current order.
- Parse observation, entity, and linked-command IDs with the corresponding `devices.Parse*ID` functions. Parse timestamps with `time.RFC3339Nano`; copy JSON values and pointers before calling `ObservationProjector` with the route adapter ID and UTC JetStream timestamp.
- Acknowledge permanently malformed observations. Leave projector and infrastructure failures unacknowledged for redelivery, and acknowledge successful projection only after the projector returns.
- Preserve the one-minute future-clock warning, safe log attributes, and `Active`, `Stop`, `Drain`, and `Closed` behavior.

### Commands

- Convert domain IDs and operation names to strings, copy parameters, and format deadlines in UTC. Preserve subject routing, schema validation, envelope timestamps, W3C trace context, request/reply behavior, and causation, correlation, and command ID checks.
- Return `devices.CommandAcceptance{Accepted: false}, nil` for a valid upstream rejection.
- NATS no-responder, timeout, disconnected, closed, and draining failures must satisfy `errors.Is(err, devices.ErrAdapterUnavailable)`; return other failures with existing context.

### JetStream, assembly, and integration fixtures

- Move observation resource policy without changing constants or configuration.
- `RuntimeReadiness.Check` continues to validate SQLite, NATS connectivity, stream/consumer configuration, and active consumption in that order. `hearthd.Run` retains startup and shutdown order.
- `hearthd` assembles `devicesnats.NewCommandSender`, `StartRegistrationServer(..., service, ...)`, and `StartObservationConsumer(..., service, ...)`; it no longer defines `natsCommandSender`, `registrationHandler`, `observationHandler`, `domainObservation`, or `copyStringPointer`.
- Replace the restart-before-ack matrix test's wire-level `ObservationHandler` decorator with an `ObservationProjector` decorator. It may inspect `devices.Observation.RefreshForCommand`; it must delegate to the real service before returning the same injected error.
- Raw linked-observation fixtures use `natswire.Envelope` with an SDK or test-local Observation DTO, not exported core payload types.

## Project Layout

```text
contracts/
└── v1/
    └── compatibility_test.go                  # move to internal/modules/devices/nats/wire_test.go

docs/
└── architecture.md                            # modify — record core and shared NATS ownership

specs/
├── first-light.md                             # modify — replace stale core wire DTO owner path
└── devices-nats-transport-ownership.md        # new — this implementation contract

internal/
├── contracts/
│   └── v1/
│       └── natswire/
│           ├── codec.go                       # new — schema-validated generic envelope codec
│           ├── codec_test.go                  # new — authoritative-schema codec coverage
│           ├── envelope.go                    # new — internal generic envelope type
│           ├── subjects.go                    # new — v1 subject construction and parsing
│           ├── subjects_test.go               # new — round-trip and unsafe-token coverage
│           └── trace.go                       # new — private carrier plus W3C inject/extract helpers
│
├── modules/
│   └── devices/
│       └── nats/
│           ├── command_sender.go              # new/move — domain CommandSender NATS adapter
│           ├── command_sender_test.go         # new/move — dispatch, correlation, rejection, unavailable mapping
│           ├── jetstream.go                   # move — observation stream and consumer policy
│           ├── jetstream_test.go              # move — provisioning, validation, ack/redelivery behavior
│           ├── observation.go                 # new/move — consumer, private delivery mapping, projector seam
│           ├── observation_test.go            # new/move — mapping, permanent failure, clock, and ack behavior
│           ├── registration.go                # new/move — server, Registrar seam, request/response mapping
│           ├── registration_test.go           # new/move — accepted, rejected, correlated response mapping
│           ├── wire.go                        # move/modify — private core payload DTOs
│           └── wire_test.go                   # move — schema and cross-binary fixture round trips
│
├── app/
│   ├── hearthd/
│   │   ├── run.go                             # modify — assembly only; remove transport translation
│   │   ├── run_integration_test.go            # modify — use domain-facing devices/nats constructors
│   │   ├── server.go                          # modify — readiness imports devices/nats
│   │   ├── readiness_test.go                  # modify — moved resource/consumer imports
│   │   └── simulator_matrix_integration_test.go # modify — domain projector decorators and natswire raw fixtures
│   └── simulator/
│       ├── run.go                             # modify — raw fault publisher uses natswire
│       └── run_test.go                        # modify — moved resource imports
│
└── platform/
    └── nats/                                  # delete — all production code and tests reassigned above

sdk/
└── adapter/
    ├── session.go                             # modify — consume shared hidden mechanics; preserve behavior
    └── session_test.go                        # modify — verify SDK against moved core resources/shared mechanics
```

The tree lists every affected baseline path outside the deleted package. Final import searches catch stale references but do not authorize unrelated reorganization.

## Atomic Migration

1. Add `internal/contracts/v1/natswire` and its tests.
2. Move core NATS behavior to `internal/modules/devices/nats`, fold in the `hearthd` translation, and make core payload DTOs private.
3. Update `hearthd`, SDK, simulator, readiness, tests, and ownership documentation to their final interfaces and imports.
4. Delete `internal/platform/nats` and run all verification gates.

There is no data or deployment migration, feature flag, or mixed-version requirement. Rollback is reverting the atomic change.

## Acceptance Criteria

### Ownership and compatibility

- [ ] `internal/platform/nats` is deleted, and `rg 'internal/platform/nats|platformnats' --glob '*.go'` finds no Go source references.
- [ ] Architecture and first-light documentation identify the new owners; only this migration spec may retain the old path.
- [ ] `natswire` contains only shared envelope, codec, subject, route, and trace mechanics. Core payload DTOs are private to `devices/nats`.
- [ ] The parent `devices` package does not import `devices/nats`, `nats.go`, or JetStream or expose their types; transport dependencies point from `devices/nats` to `devices`.
- [ ] `hearthd/run.go` contains assembly but no NATS wire construction, parsing, copying, registration rejection mapping, or command error mapping.
- [ ] `hearthd` creates and drains the focused device NATS adapters while retaining connection and lifecycle ownership.
- [ ] The public `sdk/adapter` source interface is unchanged.
- [ ] Subjects, validation and errors, schema IDs, and core/SDK payload JSON remain compatible.
- [ ] Registration, observation, command, JetStream, readiness, logging, concurrency, startup, and shutdown contracts above remain unchanged.
- [ ] Restart-before-ack still proves that redelivery does not change state or terminal command data; duplicate and malformed simulator scenarios still pass.

### Verification

- [ ] Focused `natswire` and `devices/nats` tests pass.
- [ ] `go test ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] `devenv test` passes, including formatting and generation checks.
- [ ] No unrelated files change.

## Test Strategy

| Layer | Coverage |
|---|---|
| Shared unit | Authoritative-schema envelope validation and round trips; subject round trips, parsing, and unsafe-token rejection; W3C header propagation. |
| Core mapping unit | Registrar/projector stubs capture typed IDs, copied JSON and pointers, timestamps, route adapter, and server receipt time; registration accepted, rejected, and infrastructure outcomes. |
| Core transport unit | Registration correlation and drain; observation validation order, permanent acknowledgement, projector-error redelivery, clock warning, and lifecycle. |
| Core/SDK integration | Command accepted/rejected responses, IDs, correlation, and `devices.ErrAdapterUnavailable`; cross-binary DTO fixtures validated against `contracts/v1`. |
| Application integration | Assembly, readiness, recovery, simulator matrix, and projector-based restart-before-ack failure injection retain existing assertions. |

Focused tests establish translation directly; broad repository tests do not replace them.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Validation or logging order changes while moving handlers | Medium | High | Port focused malformed-message, acknowledgement, and log assertions before deleting the old package. |
| SDK behavior changes under shared subject helpers | Low | High | Preserve validation order and test public error paths and first-party adapters. |
| Private DTOs weaken compatibility coverage | Medium | Medium | Move fixture tests beside the private core DTOs and continue comparing SDK DTOs against authoritative schemas. |
| Projector decoration changes restart-before-ack semantics | Medium | High | Delegate first, inject the error second, and retain state, command, and redelivery assertions. |
| New package dependencies create an import cycle | Low | High | Keep `devices` independent of `devices/nats`; keep `natswire` independent of core and SDK payload packages. |

## Trade-offs

- An internal shared mechanics package avoids making Go helpers an external compatibility boundary.
- Separate core and SDK payload DTOs preserve ADR 0011 and language-neutral schema authority at the cost of duplicate DTO definitions.
- Relocating the whole core transport with narrow consuming interfaces enables direct mapping tests; focused constructors keep lifecycle coordination in application assembly.
- An atomic migration avoids temporary aliases and dual ownership but produces a larger review and rollback unit.

Revisit the boundary if another product module needs genuinely domain-neutral NATS infrastructure, a non-Go adapter needs generated schema-derived code, or repeated DTO drift justifies schema generation. Open questions at this baseline: none.
