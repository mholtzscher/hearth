# Unified, typed Entity support

- **Status:** Implemented 2026-08-22
- **Type:** D1-D3 refactoring
- **Effort:** XL

## Problem and decision

First light represents adapter-reported constraints and supported operation names independently, so the values can disagree and cannot carry per-operation support. Replace them with one catalog-validated support document and schema-backed Go bindings while keeping generic JSON at wire, persistence, and orchestration seams.

The approved design is:

1. JSON Schemas are authoritative for Entity support, State, and operation parameters.
2. Every support document is `{"state": {...}, "operations": {...}}`; presence of an operation-name key means the Entity supports it.
3. Registration and GET Entity expose `support`; separate `constraints` and `operations` fields are removed before v1 ships.
4. SQLite stores normalized `support_json` and no operation projection.
5. Generic typed definitions are erased behind the concrete `TypeCatalog`.
6. Operation support participates in Command resolution, but not outcome matching. Immutable type behavior, normalized parameters, and the absolute deadline preserve an active Command's meaning across re-registration.
7. The stateless generic SDK gains typed routing primitives and a power/v1 facade for registration, Commands, and Observations.

This reworks D1-D3 and their schemas, fixtures, tests, and documentation. It establishes the typed framework with only `hearth.power/v1`; D4-D6 behavior, runtime type loading, and Command/Observation wire payloads remain unchanged.

## Public JSON contracts

### Registration Entity

```diff
 type EntityDescriptor struct {
-    Key                 string          `json:"key"`
-    ExternalID          string          `json:"external_id"`
-    Name                string          `json:"name"`
-    Type                string          `json:"type"`
-    Constraints         json.RawMessage `json:"constraints"`
-    SupportedOperations []string        `json:"operations"`
+    Key        string          `json:"key"`
+    ExternalID string          `json:"external_id"`
+    Name       string          `json:"name"`
+    Type       string          `json:"type"`
+    Support    json.RawMessage `json:"support"`
 }
```

The structural v1 registration schema closes the support object's outer shape to required `state` and `operations` objects. Every operation-support value is an object, and operation keys use the existing subject-safe operation-name schema. It does not enumerate Entity types or operations; the resolved Entity-type schema closes and refines their contents.

Power/v1 accepts exactly:

```json
{
  "support": {
    "state": {},
    "operations": {
      "set": {}
    }
  }
}
```

### GET Entity

```diff
 type EntityBody struct {
     ID       string         `json:"id"`
     DeviceID string         `json:"device_id"`
     Name     string         `json:"name"`
     Type     string         `json:"type"`
-    Constraints         map[string]any `json:"constraints"`
-    SupportedOperations []string       `json:"operations"`
-    State               *StateBody     `json:"state"`
+    Support  map[string]any `json:"support"`
+    State    *StateBody     `json:"state"`
 }
```

Command and Observation JSON, errors, deadlines, concurrency, correlation, and causation semantics do not change.

## Schema-backed Entity-type contracts

Owner: `entitytypes` and `entitytypes/powerv1`.

```go
type JSONCodec[T any] struct {
    // compiled schema and optional typed invariant validator
}

func CompileJSONCodec[T any](
    schemaID string,
    schema json.RawMessage,
    validate func(T) error,
) (*JSONCodec[T], error)

func (*JSONCodec[T]) Decode(json.RawMessage) (T, json.RawMessage, error)
func (*JSONCodec[T]) Encode(T) (json.RawMessage, error)
```

`Decode` accepts exactly one JSON value, using `UseNumber`; validates it against the authoritative schema; decodes `T`; applies the typed validator; encodes normalized JSON from `T`; and validates that normalized form again. `Encode` marshals `T` and follows the same path. Schemas compile once during codec construction.

Power/v1 bindings:

```go
package powerv1

const TypeID = "hearth.power/v1"
const OperationSet = "set"

type State bool

type StateSupport struct{}
type SetSupport struct{}

type OperationSupport struct {
    Set SetSupport `json:"set"`
}

type Support struct {
    State      StateSupport     `json:"state"`
    Operations OperationSupport `json:"operations"`
}

type SetParameters struct {
    Value bool `json:"value"`
}

type Codecs struct {
    State         *entitytypes.JSONCodec[State]
    Support       *entitytypes.JSONCodec[Support]
    SetParameters *entitytypes.JSONCodec[SetParameters]
}

func Compile() (*Codecs, error)
```

The package embeds and exposes stable IDs for these authoritative schemas:

```text
entitytypes/powerv1/
├── state.schema.json            # boolean
├── support.schema.json          # exact state/operations/set shape
└── set-parameters.schema.json   # exact {"value": boolean}
```

It owns semantic data shapes and codecs. `devices` continues to own deadlines, outcome behavior, and registration policy; transport DTOs remain local to each binary.

## Domain and catalog contracts

Owner: `internal/modules/devices`.

```diff
+type EntitySupport json.RawMessage
+
 type Entity struct {
-    Constraints         json.RawMessage
-    SupportedOperations []OperationName
+    Support EntitySupport
 }

 type EntityDescriptor struct {
-    Constraints         json.RawMessage
-    SupportedOperations []OperationName
+    Support EntitySupport
 }
```

`OperationName`, `CommandRecord.OperationName`, `Value`, and `CommandParameters` remain generic.

Catalog construction is typed; `EntityTypeDefinition` erases the concrete types into sealed closures so one catalog can hold heterogeneous definitions:

```go
type OperationDefinition[State, Support any] struct {
    // sealed typed closures
}

func DefineOperation[
    State,
    Support,
    OperationSupport,
    Parameters any,
](
    name OperationName,
    parameters *entitytypes.JSONCodec[Parameters],
    selectSupport func(Support) (OperationSupport, bool),
    validateParameters func(Support, OperationSupport, Parameters) error,
    deadline time.Duration,
    satisfies func(Parameters, State) bool,
) OperationDefinition[State, Support]

type EntityTypeDefinition struct {
    // erased type ID and typed closures
}

func DefineEntityType[State, Support any](
    id EntityTypeID,
    state *entitytypes.JSONCodec[State],
    support *entitytypes.JSONCodec[Support],
    validateSupportedState func(Support, State) error,
    equalState func(State, State) bool,
    operations ...OperationDefinition[State, Support],
) (EntityTypeDefinition, error)

func NewTypeCatalog([]EntityTypeDefinition) (*TypeCatalog, error)
func NewFirstLightTypeCatalog() (*TypeCatalog, error)

func (*TypeCatalog) NormalizeSupport(EntityTypeID, EntitySupport) (EntitySupport, error)
func (*TypeCatalog) NormalizeState(Entity, Value) (Value, error)
func (*TypeCatalog) EqualState(Entity, Value, Value) (bool, error)
func (*TypeCatalog) ResolveCommand(Entity, OperationName, CommandParameters) (ResolvedCommand, error)
func (*TypeCatalog) Satisfies(Entity, CommandRecord, Value) (bool, error)
```

Construction rejects duplicate or empty Entity type IDs, duplicate or non-subject-safe operation names, missing codecs/functions, and non-positive deadlines.

`ResolveCommand` decodes typed Entity support, selects typed operation support, decodes typed parameters, applies cross-document validation, and returns normalized parameters plus the immutable deadline. `Satisfies` resolves the immutable type and recorded operation, decodes persisted parameters and State, and calls `satisfies(parameters, state)` without current support.

For power/v1, schema validation is sufficient for support and State. `set` is always present, has a ten-second deadline, and is satisfied exactly when `parameters.Value == bool(state)`.

## Registration and persistence

Registration normalizes support before any repository write:

```go
func (service *Service) normalizeRegistration(
    adapterID string,
    registration Registration,
) (Registration, error)
```

It copies the input, validates descriptor fields, replaces support with `catalog.NormalizeSupport` output, and passes only the normalized copy to the repository. Invalid support is `RegistrationInvalidDescriptor`. Re-registration transactionally replaces support while preserving canonical IDs and rejecting an Entity type change.

The unshipped initial migration is rewritten; existing local development databases must be recreated:

```diff
 CREATE TABLE entities (
     id           TEXT PRIMARY KEY CHECK (id LIKE 'ent_%'),
     device_id    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
     name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
     type_id      TEXT NOT NULL CHECK (length(type_id) BETWEEN 1 AND 128),
-    constraints_json TEXT NOT NULL CHECK (...),
+    support_json TEXT NOT NULL CHECK (
+        json_valid(support_json) AND json_type(support_json) = 'object'
+    ),
     created_at   TEXT NOT NULL,
     updated_at   TEXT NOT NULL
 );
-
-CREATE TABLE entity_operations (...);
```

Registration writes `support_json` in its existing binding transaction, and State queries select it directly. The registration query methods for operation rows are removed and sqlc output is regenerated from migration/query sources.

`commands.operation`, normalized `parameters_json`, and absolute `deadline_at` remain unchanged. Because Entity type IDs and operation definitions are immutable and outcome matching excludes support, re-registration does not change active or historical Command interpretation.

## Typed SDK facade

The generic session remains in `sdk/adapter`; typed routing lives in `sdk/adapter/typed`; the built-in facade lives in `sdk/adapter/powerv1`.

```go
// sdk/adapter
type EntityMetadata struct {
    Key        string
    ExternalID string
    Name       string
}

// sdk/adapter/typed
type Command[P any] struct {
    ID            string
    CorrelationID string
    EntityID      string
    Parameters    P
    Deadline      time.Time
}

type Handler[P any] func(context.Context, Command[P], adapter.Responder) error
type ParameterDecoder[P any] func(json.RawMessage) (P, error)

type Route struct {
    // sealed entity ID, operation name, decoder, and erased handler
}

func Operation[P any](
    entityID string,
    operationName string,
    decode ParameterDecoder[P],
    handler Handler[P],
) (Route, error)

func NewCommandHandler(routes ...Route) (adapter.CommandHandler, error)
```

The router rejects duplicate `(entityID, operationName)` routes. Each concurrent SDK invocation matches both values, decodes and validates typed parameters through the type facade, parses the deadline, and invokes the typed handler without adding ordering or serialization. Vendor calls, responder use, refresh, and publication remain adapter behavior.

Power/v1 facade:

```go
package powerv1

type Support = contractpowerv1.Support
type State = contractpowerv1.State
type SetParameters = contractpowerv1.SetParameters
type SetCommand = typed.Command[SetParameters]

type Handlers struct {
    Set typed.Handler[SetParameters]
}

func NewEntityDescriptor(
    adapter.EntityMetadata,
    Support,
) (adapter.EntityDescriptor, error)

func NewCommandHandler(
    entityID string,
    support Support,
    handlers Handlers,
) (adapter.CommandHandler, error)

type ObservationInput struct {
    EntityID          string
    State             State
    AdapterReceivedAt time.Time
    SourceUpdatedAt   *time.Time
    RefreshForCommand *string
}

func NewObservation(ObservationInput) (adapter.Observation, error)
```

`NewEntityDescriptor` validates and normalizes support and sets type `hearth.power/v1`. `NewCommandHandler` validates support, requires `Handlers.Set`, and closes parameter validation over that support. `NewObservation` validates and encodes State and UTC timestamps. `Session.PublishObservation` retains publication, correlation, causation, and linked-command context rules. No configuration fields change.

## Ownership and layout

```text
entitytypes/
├── codec.go, codec_test.go                         # new — schema-backed typed JSON
└── powerv1/
    ├── types.go, schemas.go, schemas_test.go       # new — bindings and schema access
    └── *.schema.json                               # new — support, State, set parameters
contracts/v1/
├── registration-request.schema.json                # modify — support field
└── embed_test.go                                   # modify — fixtures
sdk/adapter/
├── types.go, session_test.go                       # modify — support DTO/fixtures
├── typed/handler.go, handler_test.go               # new — typed routing
└── powerv1/facade.go, facade_test.go               # new — typed power facade
internal/modules/devices/
├── model.go, catalog.go, catalog_test.go            # modify — support and typed catalog
├── registration.go, registration_test.go            # modify — normalization
└── sqlite_repository.go, sqlite_repository_test.go  # modify — support persistence
internal/platform/db/
├── migrations/00001_initial.sql                    # modify — support_json
├── queries/{registration,state}/*.sql              # modify — support_json
└── sqlc/**                                         # regenerate only
CONTEXT.md, docs/architecture.md, specs/first-light.md  # modify — reconcile language/contracts
```

`devices` owns product behavior, `internal/platform/db` owns persistence implementation, `sdk/adapter` owns adapter transport/facades, and public `entitytypes` owns semantic schemas and Go bindings. No new product module or repository seam is introduced.

## Deliverables

| ID | Deliverable | Effort | Depends on |
| --- | --- | --- | --- |
| U1 | Authoritative schemas, typed codec, and power/v1 bindings | L | - |
| U2 | Generic typed catalog and concrete power/v1 definition | L | U1 |
| U3 | Unified registration contract and typed SDK facade | L | U1 |
| U4 | Domain normalization, `support_json`, sqlc regeneration, repository behavior | L | U2, U3 |
| U5 | First-light/architecture reconciliation and broad validation | M | U1-U4 |

## Acceptance and verification

- [x] Registration exposes only `support` for Entity capabilities; structural validation enforces the common object shape and subject-safe operation keys without enumerating catalog contents.
- [x] The power/v1 support schema accepts exactly `{"state":{},"operations":{"set":{}}}` and rejects missing `set`, unknown operations, and extra fields.
- [x] Codecs reject malformed, trailing, schema-invalid, invariant-invalid, and binding-drifted values and return deterministic normalized JSON.
- [x] A test-only typed definition proves State, Entity support, operation support, parameters, validation, equality, and outcome callbacks cross the erased catalog without raw JSON in typed callbacks.
- [x] `ResolveCommand` rejects unsupported operations and support/parameter incompatibility before Command creation; `Satisfies` still matches a recorded Command after Entity support changes.
- [x] Registration persists normalized support, re-registration replaces it without changing canonical IDs, and restart reads the same support. SQLite contains `entities.support_json` and no `entity_operations` table.
- [x] A Go SDK consumer registers power/v1, handles typed `SetParameters`, and creates a typed boolean Observation without constructing `json.RawMessage` or switching on operation strings.
- [x] Existing generic Session behavior remains unchanged, including Command/Observation JSON, concurrent handlers, one-shot responders, publication acknowledgement/retry, and trace/correlation/causation propagation.

Tests remain local to the owning seam:

| Layer | Verification |
| --- | --- |
| Entity-type contracts | Embedded schema compilation, codec strictness/normalization, Go-binding conformance |
| Catalog | Generic erasure, power behavior, support-aware validation, immutable outcomes |
| SDK | Typed routing/facade plus existing in-process NATS contract and concurrency tests |
| Repository | Temporary migrated SQLite registration, update, rollback, restart, and schema assertions |
| Documentation/contracts | Cross-binary fixtures and first-light/API reconciliation |

Required validation is `devenv test`; it must regenerate sqlc without diff and pass formatting, schema compilation, fixtures, tests, and vetting.

## Risks

| Risk | Mitigation |
| --- | --- |
| Generic construction obscures the single concrete type | Keep erasure local to `catalog.go`, expose only the two constructors, and prove them with one focused generic test definition. |
| Go bindings drift from authoritative schemas | Validate both input and normalized encoded output and retain schema/binding conformance fixtures. |
| Mutable support changes active Command meaning | Exclude support from outcome callbacks and persist normalized parameters plus absolute deadlines. |
| Pre-release v1 consumers or local databases retain the old shape | Update fixtures atomically and recreate local databases when the rewritten initial migration lands. |
