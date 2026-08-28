# Entity Enablement — Implementation Spec

**Status:** Ready for task breakdown
**Type:** Feature plan
**Effort:** XL (approximately 3–5 focused days, 70% confidence)
**Approved by:** User design confirmation
**Date:** 2026-08-26
**Baseline:** `main` at `ef99fca`, plus the approved `CONTEXT.md` glossary change

## Problem and Decision

Hearth preserves canonical Device and Entity identity across mutable names, external identifiers, support, and Adapter ownership, but it has no non-destructive way to stop using one Entity. Deletion would discard identity and history; omission or unavailability would confuse operating condition with user or Adapter intent.

Add one Core-owned `enabled` boolean to every Entity:

- New Entities default to enabled; registration may choose the initial value only when creating an Entity.
- Management HTTP and the owning Adapter SDK may explicitly set the current value.
- Concurrent writes use SQLite commit order; the last commit wins.
- Disabled Entities remain visible and retain canonical identity, Binding, metadata, history, and last accepted State.
- Core enforces enablement. Adapters continue publishing Observations, and Core publishes no enablement-change notification.

This feature does not add Device enablement, ownership transfer, removal, retirement, deletion, availability, or Adapter-side Command cancellation. The existing trusted HTTP security policy is unchanged.

## Behavioral Contract

Enabled Entities retain all existing Command and Observation behavior.

### Commands

For a new Command attempt, Core:

1. validates the canonical Entity ID, operation name, parameter object, current Entity type and support, and normalized parameters;
2. generates Command and correlation IDs and resolves the absolute deadline; and
3. atomically reads current enablement and inserts the Command in one serializable SQLite transaction.

Invalid or unsupported input keeps the existing HTTP 400 behavior and creates no Command. For valid input, the transaction inserts `requested` when enabled or a terminal record when disabled with:

- `status = entity_disabled`;
- `failure_code = entity_disabled`;
- `requested_at` and `completed_at` set to the same Core-owned instant;
- `accepted_at = NULL` and `outcome_observation_id = NULL`; and
- the normalized operation, parameters, Adapter ID, deadline, and correlation fields that a requested Command would store.

A disabled attempt creates exactly one durable Command, allocates no waiter, sends no NATS Command, and returns HTTP 409 with its Command ID.

Disabling does not alter Commands already committed as `requested` or `accepted`. They may still be accepted, satisfied, fail, or reach their existing deadline. While disabled, only an otherwise valid Observation whose `refresh_for_command_id` identifies an active `requested` or `accepted` Command for the same Entity and Adapter may update State and satisfy that Command. Unknown, mismatched, expired, interrupted, or terminal links do not bypass disablement. Core does not claim that disabling cancels physical work already accepted by an Adapter.

Command creation and enablement mutation are serializable:

- Command-first commit produces `requested`; a later disable leaves it active.
- Disable-first commit produces terminal `entity_disabled` and no dispatch.

The `CreateCommand` repository transaction owns the definitive enablement check; a service read followed by an unconditional insert is insufficient.

### Observations

Exact-ID deduplication remains first. For a first-seen valid envelope, rejection precedence is:

1. `unknown_entity`;
2. `wrong_adapter`;
3. `entity_disabled`, unless the active-Command exception above applies;
4. `invalid_value` for an enabled or exempt Observation that violates current support.

An exempt Observation follows normal projection and outcome rules: it updates State as `applied` or `unchanged` and satisfies only its matching active Command when the catalog outcome policy matches before the deadline.

A disabled rejection leaves State and Commands unchanged, inserts an ordinary `rejected` Observation receipt with rejection code `entity_disabled` and the existing expiry policy, then acknowledges the JetStream message only after commit.

Core evaluates enablement when projecting, not when the Adapter publishes. An Observation queued while enabled may therefore be rejected after a disable commit, and one queued while disabled may apply after a re-enable commit. No generation, publication-time fence, or JetStream purge participates in this decision.

### Mutation and Registration

Setting enablement is idempotent. A same-value write succeeds without changing `updated_at`, history, Commands, or events. A changed value updates only the boolean and generic `updated_at`; there is no transition history, dedicated timestamp, event, or persisted provenance. Management and owning-Adapter writes use no source precedence, revision, or compare-and-swap; SQLite commit order decides concurrent writes.

The repository checks Adapter ownership and mutates atomically. An Adapter may update only an Entity currently mapped to its Adapter ID.

Each registration Entity descriptor may include optional `initially_enabled`:

- omitted resolves to `true` for creation;
- a new Entity is created directly with the supplied value;
- an existing Entity ignores it and preserves current enablement; and
- every accepted submitted mapping returns actual current `enabled` in request order.

These semantics make lost-response retry safe: a retry cannot overwrite an intervening management or SDK mutation. Registration remains additive; omission leaves an Entity unchanged.

## Public HTTP Contract

### Entity Representation

Add required `enabled` to every `EntityBody` returned by:

- `GET /v1/entities/{entity_id}`;
- `GET /v1/entities`; and
- `GET /v1/devices/{device_id}` embedded Entity lists.

Disabled Entities remain in all collections with their nullable or last accepted `state`. No enablement list filter is added.

### `PATCH /v1/entities/{entity_id}`

- **Operation ID:** `update-entity`
- **Tag:** `Entities`
- **Summary:** `Update an Entity`
- **Request:** `application/json`
- **Success:** HTTP 200 with the complete updated `EntityBody`
- **Errors:** malformed canonical ID → 400; Huma structural validation → 422; unknown valid ID → 404 `entity not found`; other failure → 500 `internal error`. All errors use Problem Details.

The only accepted body is:

```json
{
  "enabled": false
}
```

Missing, null, wrong-type, empty, or unknown body fields receive Huma's HTTP 422 structural validation. This is a typed Entity PATCH, not a generic patch engine. It adds no ETag or conditional request header.

### Disabled Command Problem

A valid Command attempt against a disabled Entity returns `application/problem+json` with HTTP 409:

```json
{
  "type": "about:blank",
  "title": "Conflict",
  "status": 409,
  "detail": "entity is disabled",
  "code": "entity_disabled",
  "command_id": "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
}
```

`code` and `command_id` are stable Problem Details extensions. The Command is immediately available from `GET /v1/commands/{command_id}` and Entity Command history. Existing mappings remain unchanged for missing Entity, Adapter unavailability, upstream rejection, outcome timeout, and internal failure.

## Adapter NATS Contract

### Route and Schemas

Use Core NATS request/reply on:

```text
hearth.v1.adapter.<adapter>.enablement.<entity_id>
```

The payload repeats the Entity ID. Core discards a route/payload mismatch as permanently invalid without invoking the domain service. Add `EntityEnablementSubject`, `EntityEnablementWildcard`, and `ParseEntityEnablementSubject` to `internal/contracts/v1/natswire`.

Add `ena_` UUIDv7 request IDs to the common schema and causation-ID union, plus:

```text
urn:hearth:schema:entity-enablement-request:v1
urn:hearth:schema:entity-enablement-response:v1
```

The request has an `ena_` message ID, a `cor_` correlation ID, no `causation_id`, and this `data`:

```json
{
  "entity_id": "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "enabled": false
}
```

Accepted response `data`:

```json
{
  "status": "accepted",
  "entity_id": "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "enabled": false
}
```

Rejected response `data`:

```json
{
  "status": "rejected",
  "error": {
    "code": "wrong_adapter",
    "message": "entity is owned by another adapter"
  }
}
```

Permanent rejection codes are `unknown_entity` and `wrong_adapter`. The response schema uses `oneOf`: accepted requires `entity_id` and `enabled` and omits `error`; rejected requires `error` and omits accepted fields. Infrastructure failures produce no schema-level rejection; Core logs the failure and the single request/reply attempt fails or times out.

Responses use `rep_` IDs, copy the request correlation ID, and set `causation_id` to the `ena_` request ID. Before returning, the SDK validates schema, causation, correlation, request route/payload identity, and accepted response identity.

### Delivery

`Session.SetEntityEnabled` performs one Core NATS request/reply attempt without JetStream or automatic retry. Because the setter is idempotent, callers may retry the desired value under their own context and policy. No notification, subscription, SDK cache, callback, or local publication gate is added.

## Implementation Contract

### Domain and Registration Types

Owner: `internal/modules/devices/model.go` and `registration.go`.

```diff
type Entity struct {
    ID        EntityID
    DeviceID  DeviceID
    AdapterID string
    Name      string
    TypeID    EntityTypeID
    Support   EntitySupport
+   Enabled   bool
}

const (
    RejectionUnknownEntity  ObservationRejection = "unknown_entity"
    RejectionWrongAdapter   ObservationRejection = "wrong_adapter"
    RejectionInvalidValue   ObservationRejection = "invalid_value"
+   RejectionEntityDisabled ObservationRejection = "entity_disabled"
)

const (
    // existing statuses
+   CommandStatusEntityDisabled CommandStatus = "entity_disabled"
)

const (
    // existing failure codes
+   CommandFailureEntityDisabled CommandFailureCode = "entity_disabled"
)
```

```diff
type EntityDescriptor struct {
    Key        string
    ExternalID string
    Name       string
    TypeID     EntityTypeID
    Support    EntitySupport
+   InitiallyEnabled *bool
}

type EntityBinding struct {
    Key      string
    EntityID EntityID
+   Enabled  bool
}
```

Copy `InitiallyEnabled` with the descriptor. The service resolves `nil` only for creation; the repository never applies it to an existing Entity.

### Repository

Owner: `internal/modules/devices/repository.go`.

```go
var (
    ErrEntityDisabled     = errors.New("entity disabled")
    ErrEntityWrongAdapter = errors.New("entity belongs to another adapter")
)

type SetEntityEnabledParams struct {
    EntityID      EntityID
    Enabled       bool
    RequiredOwner *string
    UpdatedAt     time.Time
}
```

```diff
type Repository interface {
    // existing methods
+   SetEntityEnabled(context.Context, SetEntityEnabledParams) (EntityWithState, error)
}

type CommandLedger interface {
-   CreateCommand(context.Context, CommandRecord) error
+   CreateCommand(context.Context, CommandRecord) (CommandRecord, error)
    // existing methods
}
```

`RequiredOwner == nil` selects trusted management; non-nil requires an exact current Adapter mapping in the update transaction. Repository results retain the existing owned-copy guarantee for mutable JSON and pointers.

### HTTP API Types

Owner: `internal/modules/devices/api/types.go`, `patch_entity.go`, and `errors.go`.

```diff
type EntityBody struct {
    ID       string         `json:"id"`
    DeviceID string         `json:"device_id"`
    Name     string         `json:"name"`
    Type     string         `json:"type"`
    Support  map[string]any `json:"support"`
+   Enabled  bool           `json:"enabled"`
    State    *StateBody     `json:"state"`
}

type PatchEntityBody struct {
    Enabled bool `json:"enabled"`
}
```

```go
type PatchEntityInput struct {
    EntityID string          `path:"entity_id" doc:"Canonical Hearth Entity ID"`
    Body     PatchEntityBody `doc:"Mutable Entity fields"`
}

type PatchEntityOutput struct {
    Body EntityBody
}
```

Define a private API Problem Details type for disabled Commands. It must implement Huma status-error and Problem Details content-type behavior and expose `code` and `command_id` in runtime OpenAPI for HTTP 409.

### SDK Types

Owner: `sdk/adapter/types.go` and `errors.go`.

```diff
type EntityDescriptor struct {
    // existing fields
+   InitiallyEnabled *bool `json:"initially_enabled,omitempty"`
}

type EntityBinding struct {
    Key      string `json:"key"`
    EntityID string `json:"entity_id"`
+   Enabled  bool   `json:"enabled"`
}

type EntityEnablementRequest struct {
    EntityID string `json:"entity_id"`
    Enabled  bool   `json:"enabled"`
}

type EntityEnablementResponse struct {
    Status   string                 `json:"status"`
    EntityID string                 `json:"entity_id,omitempty"`
    Enabled  *bool                  `json:"enabled,omitempty"`
    Error    *EntityEnablementError `json:"error,omitempty"`
}

type EntityEnablementError struct {
    Code    EntityEnablementRejectionCode `json:"code"`
    Message string                        `json:"message"`
}
```

```go
type EntityEnablementRejectionCode string

const (
    EntityEnablementUnknownEntity EntityEnablementRejectionCode = "unknown_entity"
    EntityEnablementWrongAdapter  EntityEnablementRejectionCode = "wrong_adapter"
)

type EntityEnablementRejectedError struct {
    Code    EntityEnablementRejectionCode
    Message string
}
```

`Enabled` is a pointer in the response DTO so accepted `false` is present while rejected responses omit it; accepted responses require non-nil `Enabled`. Core NATS DTOs remain private to `internal/modules/devices/nats`, and SDK DTOs do not depend on the domain package.

### Service and Consumer Interfaces

Owner: new `internal/modules/devices/enablement.go`.

```go
func (service *Service) SetEntityEnabled(
    ctx context.Context,
    entityID EntityID,
    enabled bool,
) (EntityWithState, error)

func (service *Service) SetOwnedEntityEnabled(
    ctx context.Context,
    adapterID string,
    entityID EntityID,
    enabled bool,
) (bool, error)
```

Keep these as two policy-specific entry points over one private implementation. The HTTP consumer interface exposes only the trusted management method; the NATS consumer interface exposes only the owner-scoped method. The service does not infer authority from `context.Context` or transport metadata, and callers do not pass a source enum or optional authorization flag.

Both methods validate the Entity ID; the owner-scoped method also validates the Adapter slug. They delegate to the same private helper, which takes one Core-owned UTC timestamp and calls the repository with `RequiredOwner == nil` for management or the Adapter ID for NATS. HTTP receives the owned updated Entity view; NATS/SDK receives its Core-confirmed `enabled` value.

Add the management method to the consumer-owned API interface in `internal/modules/devices/api/register.go` and register the route beside existing Entity routes:

```diff
type Devices interface {
    GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
+   SetEntityEnabled(context.Context, devices.EntityID, bool) (devices.EntityWithState, error)
    ExecuteCommand(context.Context, devices.EntityID, devices.OperationName, devices.CommandParameters) (devices.CommandResult, error)
    // existing read methods
}
```

Handlers translate IDs, DTOs, domain errors, and Problem Details; enablement decisions remain in the service and repository.

Owner: new `internal/modules/devices/nats/enablement.go`.

```go
type EntityEnablementSetter interface {
    SetOwnedEntityEnabled(context.Context, string, devices.EntityID, bool) (bool, error)
}

func StartEntityEnablementServer(
    *nats.Conn,
    *contractsv1.Validator,
    EntityEnablementSetter,
    *slog.Logger,
) (*EntityEnablementServer, error)
```

The server owns decoding, subject/payload matching, trace propagation, domain error mapping, response encoding, logging, and subscription drain. Application assembly starts and drains it alongside `RegistrationServer`.

Owner: `sdk/adapter/session.go`.

```go
func (session *Session) SetEntityEnabled(
    ctx context.Context,
    entityID string,
    enabled bool,
) (bool, error)
```

The method returns `ValidationError` for local contract or ID failures and `EntityEnablementRejectedError` for permanent Core rejection.

## Persistence and Service Integration

### Migration `00003_entity_enablement.sql`

Add immutable Goose migration `internal/platform/db/migrations/00003_entity_enablement.sql`.

Up must:

1. add `entities.enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))`, enabling existing Entities;
2. rebuild `commands` to admit `entity_disabled` in status and failure-code checks while preserving every row, constraint, foreign key, timestamp, and the 00002 `(entity_id, requested_at DESC, id DESC)` index;
3. rebuild `observation_receipts` and `entity_states` together so receipts admit `entity_disabled` while preserving receipt IDs, `receive_order`, uniqueness, current-State references, and all existing constraints; and
4. run with foreign-key checking enabled and leave `PRAGMA foreign_key_check` empty.

Down must:

1. map `entity_disabled` Commands to terminal `internal_failure` / `internal_error`;
2. delete `entity_disabled` receipts before rebuilding; deletion must fail on an unexpected current-State reference, and tests must prove normal projection creates none;
3. rebuild receipt/State and Command tables with prior checks and indexes; and
4. remove `entities.enabled`.

Migration tests cover empty and populated upgrades containing Devices, Entities, current State, receipts, and every pre-existing Command status, plus the explicit lossy down mapping.

### Query Sources

Modify SQL sources and regenerate sqlc output:

- `queries/registration/registration.sql`: read current `e.enabled`, insert resolved creation enablement, and exclude enablement from `UpdateEntityDescriptor`.
- `queries/state/state.sql`: select `e.enabled` in every Entity/current-State read, add the transactional update, and retain disabled Entities in all reads.
- `queries/commands/commands.sql` and `queries/receipts/receipts.sql`: retain insert shapes; repositories supply the new terminal fields and rejection code.

Generated persistence types remain behind the `devices` repository.

### Transaction Ownership

`SQLiteRepository.SetEntityEnabled` owns one serializable transaction:

1. load the Entity, current Adapter mapping, and nullable State;
2. return `ErrEntityNotFound` if absent or, when `RequiredOwner` is set, `ErrEntityWrongAdapter` unless it exactly matches the current mapping;
3. update `enabled` and generic `updated_at` only when the value changes;
4. load the transaction-consistent Entity/current-State view; and
5. commit.

`SQLiteRepository.CreateCommand` becomes transactional, classifies current enablement, inserts either the candidate `requested` record or its terminal `entity_disabled` form, and returns the committed record.

Observation projection keeps its serializable transaction and classifies disabled/active-linked Observations before State normalization and projection.

### Command and Registration Flow

`Service.ExecuteCommand` retains transport-independent validation and catalog resolution. After constructing the candidate `requested` record, it:

1. calls transactional `repository.CreateCommand`;
2. returns `CommandExecutionError{CommandID: id, Err: ErrEntityDisabled}` immediately for `entity_disabled`; or
3. adds the waiter after the requested record commits, then starts existing dispatch and outcome handling.

The post-commit waiter is safe because dispatch has not begun. Existing disconnect, timeout, failure, and satisfying-Observation behavior remains unchanged.

Registration adds the optional request field and required response field:

```diff
// contracts/v1/registration-request.schema.json
+"initially_enabled": { "type": "boolean", "default": true }

// contracts/v1/registration-response.schema.json
-"required": ["key", "entity_id"]
+"required": ["key", "entity_id", "enabled"]
+"enabled": { "type": "boolean" }
```

JSON Schema's `default` is descriptive; Core resolves omission. Because registration responses use `additionalProperties: false`, old strict SDK schemas reject the new response. Core, embedded contracts, and first-party SDK therefore ship atomically; mixed-version registration is unsupported. Stored Bindings and canonical IDs remain compatible.

## Project Layout

```text
CONTEXT.md                                                   # already modified — approved glossary term
contracts/v1/
├── common.schema.json                                       # modify — ena_ ID and causation
├── embed.go, embed_test.go                                  # modify — embed and verify schemas
├── entity-enablement-request.schema.json                    # new — setter request
├── entity-enablement-response.schema.json                   # new — setter response
├── registration-request.schema.json                         # modify — creation-only initial value
└── registration-response.schema.json                        # modify — current value in mappings
docs/architecture.md                                        # modify — accepted behavior and contracts
internal/app/hearthd/
├── run.go                                                   # modify — start/drain NATS server
├── server_test.go                                           # modify — PATCH and OpenAPI compatibility
└── simulator_matrix_integration_test.go                     # modify — assembled scenario
internal/contracts/v1/natswire/
├── subjects.go                                              # modify — subject and parser
└── subjects_test.go                                         # modify — route validation
internal/modules/devices/
├── api/
│   ├── command.go, command_test.go                          # modify — durable 409
│   ├── errors.go                                            # modify — Problem Details extension
│   ├── mapping.go, types.go                                 # modify — enabled response and PATCH DTO
│   ├── patch_entity.go, patch_entity_test.go                # new — Entity PATCH
│   ├── register.go                                          # modify — consumer method and route
│   └── resource_reads_test.go                               # modify — disabled visibility
├── command.go, command_test.go                              # modify — atomic classification
├── enablement.go, enablement_test.go                        # new — management/owner use cases
├── model.go, registration.go, registration_test.go          # modify — fields and initial semantics
├── nats/
│   ├── enablement.go, enablement_test.go                    # new — Core request/reply server
│   ├── registration.go, registration_test.go                # modify — initial/current mapping
│   └── wire.go                                              # modify — private DTOs
├── observation.go                                           # modify — owned copies
├── repository.go                                            # modify — mutation seam and errors
├── sqlite_observations.go, sqlite_observations_test.go      # modify — projection policy
├── sqlite_reads.go, sqlite_reads_test.go                    # modify — read mapping
└── sqlite_repository.go, sqlite_repository_test.go          # modify — transactions and registration
internal/platform/db/
├── migrations/00003_entity_enablement.sql                   # new — column and checked-enum rebuilds
├── queries/
│   ├── registration/registration.sql                       # modify — create/read enablement
│   └── state/state.sql                                     # modify — read/update enablement
├── sqlc/                                                    # regenerate
└── db_test.go                                               # modify — up/down/FK coverage
sdk/adapter/
├── errors.go                                                # modify — typed permanent rejection
├── session.go, session_test.go                              # modify — one-attempt setter
└── types.go                                                 # modify — registration and setter DTOs
```

## Deliverables

| Deliverable | Effort | Depends On |
|---|---:|---|
| D1. Domain types, registration contracts, migration, sqlc, and read mapping | L | — |
| D2. Atomic mutation, disabled Observation projection, and Command creation | L | D1 |
| D3. Entity PATCH, disabled Command Problem Details, and OpenAPI | M | D1, D2 |
| D4. Adapter NATS setter and Go SDK method | L | D1, D2 |
| D5. Runtime races, architecture docs, and clean-checkout verification | L | D2, D3, D4 |

Total effort is **XL, approximately 3–5 focused days at 70% confidence**. The principal risks are SQLite checked-table migration, cross-transaction races, and custom HTTP 409/OpenAPI representation.

## Acceptance Criteria

### Registration and Reads

- [ ] Schemas accept omitted, `true`, and `false` `initially_enabled` and reject non-booleans.
- [ ] Omission creates enabled; explicit false creates disabled without a transient enabled commit.
- [ ] Re-registration preserves current enablement, including a lost-response retry after an intervening HTTP or SDK mutation.
- [ ] Accepted submitted mappings return actual current `enabled` in request order.
- [ ] Direct reads, Entity lists, and Device-detail Entities include disabled Entities, required `enabled`, and retained nullable/last State.

### HTTP Mutation

- [ ] PATCH accepts exactly one required boolean `enabled`, returns the complete Entity, and treats same-value writes as successful no-ops.
- [ ] Missing, null, wrong-type, empty, and unknown fields return Huma 422; invalid IDs return 400; unknown valid IDs return 404.
- [ ] Runtime OpenAPI includes `update-entity`, required `enabled` request/response fields, the declared errors, and the custom disabled-Command 409 fields.

### SDK Mutation

- [ ] Schemas, subjects, Core server, and SDK round-trip accepted true and false with valid identity, correlation, and causation checks.
- [ ] `Session.SetEntityEnabled` makes one request/reply attempt and returns the Core-confirmed boolean.
- [ ] Unknown Entity and wrong Adapter return distinct typed permanent errors.
- [ ] Malformed envelopes and route/payload mismatches do not invoke the domain setter; ownership check and mutation are atomic.
- [ ] No notification, automatic retry, SDK cache/callback, or local publication gate is introduced.

### Commands

- [ ] Invalid or unsupported disabled-Entity requests return 400 and create no Command.
- [ ] A valid disabled attempt creates one exact terminal `entity_disabled` record, no waiter, and no Adapter request; its 409 has stable `code` and `command_id`.
- [ ] That Command is immediately available through direct and history reads with normalized parameters and all specified terminal fields.
- [ ] A deterministic real-SQLite race proves Command-first creates active work and disable-first creates a terminal disabled Command.
- [ ] A Command active before disablement retains every existing accepted, satisfied, failed, and timeout path.

### Observations

- [ ] An unlinked owning-Adapter Observation projected while disabled commits one rejected `entity_disabled` receipt, is acknowledged, and leaves State unchanged; exact duplicates remain duplicates.
- [ ] Rejection precedence is unknown Entity, wrong Adapter, disabled, then invalid value outside the active-Command exception.
- [ ] A valid Observation linked to a matching requested/accepted Command may update State and satisfy it while disabled; unknown, mismatched, expired, interrupted, and terminal links cannot.
- [ ] Processing-time tests cover queued Observations across both disable and re-enable commits.
- [ ] Existing receipt retention and current-State receipt pinning invariants remain valid.

### Persistence and Compatibility

- [ ] Migration 00003 upgrades empty and populated databases without losing rows, IDs, receive order, State references, Command history, indexes, constraints, or foreign-key validity.
- [ ] Down migration performs the specified lossy mappings and leaves a schema accepted by the prior application.
- [ ] sqlc output is generated only from migration/query sources and remains reproducible.
- [ ] Repository methods expose only owned domain models, with no sqlc leakage.
- [ ] The assembled runtime covers registration, HTTP disable, disabled Command history, rejected Observation acknowledgement, active-Command completion, SDK re-enable, and subsequent State projection.
- [ ] Existing simulator, Home Assistant Adapter, health/readiness, registration, State, Command, history, and OpenAPI behavior remains green.
- [ ] Core/contracts/SDK atomic shipping is documented and cross-binary contract tested; stored identities remain compatible.
- [ ] `devenv test`, `git diff --check`, and final diff/status review pass.

## Test Placement

| Layer | Owned risk |
|---|---|
| Contract | Registration and enablement schema shape, strict fields, `ena_` IDs, causation, malformed and extra fields. |
| Service | ID/Adapter validation, registration default/copy semantics, owned copies, and disabled result before waiter creation. |
| Repository | Migrated SQLite mutation/no-op/ownership, retry safety, atomic classification, checked enums, and coordinated writer races. |
| Observation | Deduplication, rejection precedence, active-link boundary, processing-time ordering, receipt retention. |
| HTTP | PATCH validation/mapping, disabled visibility, custom 409 Problem Details, OpenAPI, existing mappings. |
| NATS/SDK | Subject parsing, schema round trips, typed rejections, identity/correlation/causation validation, one-attempt behavior. |
| Runtime | Assembled SQLite, NATS/JetStream, Core, SDK, and HTTP disable/re-enable flow. |
| Migration/generation | Empty/populated up/down, foreign keys, indexes, checks, and reproducible sqlc output. |

Use service tests for transport-independent rules, migrated SQLite for every transactional claim, API tests for Huma behavior, and runtime tests only for cross-seam risks.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Table rebuild loses or rewires current-State receipts | Medium | High | Rebuild receipts and State together; assert IDs, order, foreign keys, and projections across up/down. |
| Command and disable commits classify in the wrong order | Medium | High | Classify and insert in one repository transaction; coordinate real SQLite race tests. |
| Active-linked Observation exception is too broad | Medium | High | Require exact Command, Entity, Adapter, active status, and deadline; test mismatch and terminal boundaries. |
| Strict registration response breaks mixed Core/SDK versions | High during rolling upgrade | Medium | Ship Core/contracts/SDK atomically and run cross-binary registration contract tests. |
| Disabled traffic consumes consumer/SQLite capacity | Medium | Medium | Commit only the bounded receipt, acknowledge promptly after commit, and retain current pruning. |

## Verification

Run the repository validation gate, which regenerates, formats, tidies, and executes all checks:

```sh
devenv test
git diff --check
git status --short
```

---
*Spec approved for task decomposition.*
