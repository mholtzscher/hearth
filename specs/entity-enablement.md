# Entity Enablement — Implementation Spec

**Status:** Ready for task breakdown
**Type:** Feature plan
**Effort:** XL (approximately 3–5 focused days, 70% confidence)
**Approved by:** User design confirmation
**Date:** 2026-08-26
**Baseline:** `main` at `ef99fca`, plus the approved `CONTEXT.md` glossary change

## Problem

Hearth preserves canonical Device and Entity identity across mutable names, external identifiers, support, and Adapter ownership. It currently has no non-destructive way to stop using one Entity. Deleting the row would discard identity and history, while treating omission or unavailability as removal would confuse temporary operating condition with user or Adapter intent.

Two concrete actors need the same bounded behavior:

- a management client needs to suppress an Entity without deleting it; and
- an owning Adapter needs to create or later mark an optional Entity disabled without changing its canonical identity.

The feature must preserve Hearth's existing Command and Observation guarantees. In particular, disabling cannot pretend to cancel Adapter work already in progress, and an Observation from a disabled Entity must still be acknowledged so JetStream does not redeliver it indefinitely.

## Decision

Add one Core-owned `enabled` boolean to every Entity.

- New Entities default to enabled.
- Registration may choose initial enablement only when creating an Entity.
- Management HTTP and the owning Adapter SDK may explicitly set enablement afterward.
- Concurrent mutations use SQLite commit order; the last committed value wins.
- Disabled Entities remain visible and retain canonical identity, Binding, metadata, history, and last accepted State.
- Device enablement, removal, retirement, and permanent deletion remain outside this feature.

Enablement is Core policy. The SDK does not suppress Observation publication locally, and Core does not publish enablement-change notifications. This keeps Core authoritative and avoids a second synchronization protocol whose missed enable event could leave an Adapter permanently silent.

## Behavioral Contract

### Enabled Entity

An enabled Entity behaves exactly as an Entity does today:

- valid Observations from the owning Adapter advance State;
- supported valid Command requests create and dispatch Commands; and
- linked matching Observations may satisfy active Commands.

### Disabled Entity

A disabled Entity:

- remains in Entity lists, direct Entity reads, and Device-detail embedded Entity lists;
- retains its Binding, metadata, Command history, and last accepted State;
- rejects new valid Command attempts before dispatch;
- durably records those attempts as terminal `entity_disabled` Commands;
- durably records unrelated incoming Observations as rejected with `entity_disabled` under the existing receipt-retention policy; and
- acknowledges those Observation messages only after the rejection receipt commits.

Disablement is reversible and is not availability, ownership transfer, household removal, or deletion.

### Commands Already in Progress

Disabling takes effect immediately for new Command creation, but it does not cancel Commands that were already durably created as `requested` or `accepted`.

Those active Commands retain their normal lifecycle until they are satisfied, fail, or reach their deadline. While the Entity is disabled, an otherwise valid Observation may still update State and satisfy a Command only when its `refresh_for_command_id` identifies an active `requested` or `accepted` Command for the same Entity and Adapter.

This exception is bounded by the existing Command deadline. A linked Observation for an unknown, mismatched, or terminal Command does not bypass disablement.

Core does not claim that disabling cancels physical work. An Adapter that accepted a Command may still change the external object.

### New Command Attempts

Command validation precedes the enablement outcome:

1. validate the canonical Entity ID, operation name, parameter object, current Entity type, current support, and normalized parameters;
2. generate the Command and correlation IDs and resolve its absolute deadline;
3. atomically inspect current enablement and insert the Command record in one SQLite transaction;
4. insert `requested` when enabled, or terminal `entity_disabled` when disabled.

An invalid or unsupported request returns the existing HTTP 400 behavior and creates no Command record. A valid request blocked by disablement creates exactly one terminal record and returns HTTP 409.

The terminal record has:

- `status = entity_disabled`;
- `failure_code = entity_disabled`;
- `requested_at` and `completed_at` set to the same Core-owned instant;
- `accepted_at = NULL`;
- `outcome_observation_id = NULL`; and
- the same normalized operation, parameters, Adapter ID, deadline, and correlation fields that a requested Command would have stored.

No waiter is created and no NATS Command is sent.

### Command-versus-Disable Race

Command creation and enablement mutation are serializable SQLite transactions.

- If Command creation commits first, the record is `requested`; a later disable does not alter it, and its linked Observation exception remains available.
- If disabling commits first, the valid Command attempt is inserted terminal as `entity_disabled` and is never dispatched.

A service-level read followed by a separate unconditional Command insert is not sufficient. The repository transaction that inserts the Command owns the definitive enablement check.

### Observations

Observation projection keeps exact-ID deduplication first. For a first-seen valid envelope, rejection precedence is:

1. `unknown_entity` when the canonical Entity does not exist;
2. `wrong_adapter` when the publisher does not own the Entity;
3. `entity_disabled` when the Entity is disabled and the Observation is not linked to a matching active Command;
4. `invalid_value` when an enabled or active-Command-exempt Observation does not satisfy current Entity support.

An Observation admitted by the active-Command exception follows normal projection rules: it updates State as `applied` or `unchanged`, and it satisfies only its matching active Command when the catalog outcome policy matches before the deadline.

A disabled rejection does not update State or satisfy a Command. It inserts an ordinary Observation receipt with disposition `rejected`, rejection code `entity_disabled`, and the existing expiry policy. Core then acknowledges the JetStream message.

Enablement is evaluated when Core processes the Observation, not when the Adapter published it. Therefore:

- an Observation published while enabled may be rejected if disabling commits before projection; and
- an Observation published while disabled may apply if re-enabling commits before projection.

No enablement generation, publication timestamp fence, or JetStream purge is introduced.

### Enablement Mutation

Setting enablement is idempotent. Setting the current value succeeds without creating history, changing Command records, or producing an event.

The management and owning-Adapter paths mutate the same boolean. There is no source precedence, ETag, expected revision, compare-and-swap, transition table, or persisted enablement provenance. SQLite commit order defines the accepted order of concurrent writes.

The repository performs the Adapter ownership check and mutation atomically. An Adapter can mutate only an Entity currently mapped to its Adapter ID.

### Registration

Each Entity descriptor may include optional `initially_enabled`:

- omitted means `true`;
- a new Entity is created with the supplied value; and
- an existing Entity ignores the supplied initial value and preserves current enablement.

Every accepted submitted Entity mapping returns actual current `enabled`, whether the Entity was created or already existed. This makes lost-response retry safe: the first request may create the Entity, while a retry treats it as existing and cannot overwrite a later management or SDK mutation.

Registration remains additive. Omission does not disable an Entity.

## Public HTTP Contract

### Entity Representation

Add required `enabled` to every `EntityBody`, including:

- `GET /v1/entities/{entity_id}`;
- `GET /v1/entities` items; and
- Entities embedded by `GET /v1/devices/{device_id}`.

Disabled Entities remain in every existing collection and preserve their nullable or last accepted `state` unchanged. No enablement list filter is added.

### `PATCH /v1/entities/{entity_id}`

- **Operation ID:** `update-entity`
- **Tag:** `Entities`
- **Summary:** `Update an Entity`
- **Request:** `application/json`
- **Success:** HTTP 200 with the complete updated `EntityBody`
- **Errors:**
  - malformed canonical ID: HTTP 400 Problem Details;
  - missing, null, wrong-type, empty, or unknown body fields: Huma structural validation, HTTP 422 Problem Details;
  - unknown valid Entity ID: HTTP 404 Problem Details, `entity not found`;
  - other failure: HTTP 500 Problem Details, `internal error`.

The only accepted body is:

```json
{
  "enabled": false
}
```

This establishes an Entity PATCH route but no generic patch engine. The typed input contains only `enabled`; future fields are added only after their ownership and reconciliation behavior is specified.

The existing trusted unauthenticated HTTP policy remains unchanged. No ETag or conditional request header is added.

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

`code` and `command_id` are stable Problem Details extension fields. The Command is queryable immediately through `GET /v1/commands/{command_id}` and appears in Entity Command history.

Existing Command error mappings remain unchanged for invalid input, missing Entity, Adapter unavailability, upstream rejection, outcome timeout, and internal failure.

## Adapter NATS Contract

Add Core NATS request/reply for explicit owning-Adapter mutation.

### Subject

```text
hearth.v1.adapter.<adapter>.enablement.<entity_id>
```

The Adapter ID and Entity ID are present in the route. The request payload repeats the Entity ID, and Core rejects a route/payload mismatch as a permanently invalid request without invoking the domain service.

Add `EntityEnablementSubject`, `EntityEnablementWildcard`, and `ParseEntityEnablementSubject` to `internal/contracts/v1/natswire`.

### Message IDs and Schemas

Add `ena_` UUIDv7 request IDs to the common schema and causation-ID union.

```text
urn:hearth:schema:entity-enablement-request:v1
urn:hearth:schema:entity-enablement-response:v1
```

Request `data`:

```json
{
  "entity_id": "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "enabled": false
}
```

The request envelope has no `causation_id`. It uses an `ena_` message ID and a `cor_` correlation ID.

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

Rejection codes are:

- `unknown_entity`; and
- `wrong_adapter`.

They are permanent configuration or ownership failures. Internal infrastructure failures produce no schema-level rejection; the Core logs the failure and the single request/reply attempt fails or times out.

Responses use `rep_` IDs, copy the request correlation ID, and set `causation_id` to the `ena_` request ID. The SDK validates schema, causation, correlation, route/payload identity, and accepted result identity before returning.

### Delivery and Retry

`Session.SetEntityEnabled` performs one Core NATS request/reply attempt. It does not automatically retry and does not use JetStream. The setter is idempotent, so the caller may retry the desired value under its own context and policy.

No enablement-change event or subscription is added.

## Types

### Domain Types

Owner: `internal/modules/devices/model.go` and `registration.go`.

```diff
diff --git a/internal/modules/devices/model.go b/internal/modules/devices/model.go
@@
 type Entity struct {
     ID        EntityID
     DeviceID  DeviceID
     AdapterID string
     Name      string
     TypeID    EntityTypeID
     Support   EntitySupport
+    Enabled   bool
 }
@@
 const (
     RejectionUnknownEntity ObservationRejection = "unknown_entity"
     RejectionWrongAdapter  ObservationRejection = "wrong_adapter"
     RejectionInvalidValue  ObservationRejection = "invalid_value"
+    RejectionEntityDisabled ObservationRejection = "entity_disabled"
 )
@@
 const (
     CommandStatusRequested          CommandStatus = "requested"
@@
     CommandStatusInterrupted        CommandStatus = "interrupted"
+    CommandStatusEntityDisabled     CommandStatus = "entity_disabled"
 )
@@
 const (
@@
     CommandFailureCoreRestarted CommandFailureCode = "core_restarted"
+    CommandFailureEntityDisabled CommandFailureCode = "entity_disabled"
 )
```

```diff
diff --git a/internal/modules/devices/registration.go b/internal/modules/devices/registration.go
@@
 type EntityDescriptor struct {
     Key        string
     ExternalID string
     Name       string
     TypeID     EntityTypeID
     Support    EntitySupport
+    InitiallyEnabled *bool
 }
@@
 type EntityBinding struct {
     Key      string
     EntityID EntityID
+    Enabled  bool
 }
```

`InitiallyEnabled` is copied with the rest of the descriptor. The service resolves `nil` to `true` only for creation; the repository never applies it to an existing Entity.

### Repository Parameters and Errors

Owner: `internal/modules/devices/repository.go`.

```diff
diff --git a/internal/modules/devices/repository.go b/internal/modules/devices/repository.go
@@
 var (
@@
     ErrOutcomeTimeout      = errors.New("command outcome timeout")
+    ErrEntityDisabled      = errors.New("entity disabled")
+    ErrEntityWrongAdapter  = errors.New("entity belongs to another adapter")
 )
+
+type SetEntityEnabledParams struct {
+    EntityID       EntityID
+    Enabled        bool
+    RequiredOwner  *string
+    UpdatedAt      time.Time
+}
@@
 type Repository interface {
@@
+    SetEntityEnabled(context.Context, SetEntityEnabledParams) (EntityWithState, error)
 }
@@
 type CommandLedger interface {
-    CreateCommand(context.Context, CommandRecord) error
+    CreateCommand(context.Context, CommandRecord) (CommandRecord, error)
```

`RequiredOwner == nil` is the trusted management path. A non-nil Adapter ID requires an exact current mapping match in the same transaction as the update. Repository results own all mutable JSON and pointer data.

### API Types

Owner: `internal/modules/devices/api/types.go` and new `patch_entity.go`.

```diff
diff --git a/internal/modules/devices/api/types.go b/internal/modules/devices/api/types.go
@@
 type EntityBody struct {
     ID       string         `json:"id"`
     DeviceID string         `json:"device_id"`
     Name     string         `json:"name"`
     Type     string         `json:"type"`
     Support  map[string]any `json:"support"`
+    Enabled  bool           `json:"enabled"`
     State    *StateBody     `json:"state"`
 }
+
+type PatchEntityBody struct {
+    Enabled bool `json:"enabled"`
+}
```

Endpoint input/output:

```go
type PatchEntityInput struct {
    EntityID string          `path:"entity_id" doc:"Canonical Hearth Entity ID"`
    Body     PatchEntityBody `doc:"Mutable Entity fields"`
}

type PatchEntityOutput struct {
    Body EntityBody
}
```

Define a private or API-owned Problem Details extension type for disabled Commands. It must implement Huma's status-error and Problem Details content-type behavior and expose `code` and `command_id` in runtime OpenAPI for HTTP 409.

### SDK and Wire Types

Owner: `sdk/adapter/types.go` and `errors.go`.

```diff
diff --git a/sdk/adapter/types.go b/sdk/adapter/types.go
@@
 type EntityDescriptor struct {
@@
     Support    json.RawMessage `json:"support"`
+    InitiallyEnabled *bool     `json:"initially_enabled,omitempty"`
 }
@@
 type EntityBinding struct {
     Key      string `json:"key"`
     EntityID string `json:"entity_id"`
+    Enabled  bool   `json:"enabled"`
 }
+
+type EntityEnablementRequest struct {
+    EntityID string `json:"entity_id"`
+    Enabled  bool   `json:"enabled"`
+}
+
+type EntityEnablementResponse struct {
+    Status   string                  `json:"status"`
+    EntityID string                  `json:"entity_id,omitempty"`
+    Enabled  *bool                   `json:"enabled,omitempty"`
+    Error    *EntityEnablementError  `json:"error,omitempty"`
+}
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

`EntityEnablementResponse.Enabled` is a pointer because accepted `false` must still be present on the wire while rejected responses omit the field. The SDK requires a non-nil value for accepted responses before returning the boolean.

The Core-side NATS DTOs remain private to `internal/modules/devices/nats`; SDK DTOs contain no domain package dependency.

## Interfaces

### Service Use Cases

Owner: new `internal/modules/devices/enablement.go`.

```go
func (service *Service) SetEntityEnabled(
    context.Context,
    EntityID,
    bool,
) (EntityWithState, error)

func (service *Service) SetOwnedEntityEnabled(
    context.Context,
    string,
    EntityID,
    bool,
) (bool, error)
```

Both methods:

- validate the canonical Entity ID;
- the owner-scoped method also validates the Adapter slug;
- take one Core-owned UTC timestamp;
- call the same repository mutation with or without `RequiredOwner`; and
- return owned data.

The HTTP method returns the updated Entity view. The owner-scoped method narrows that result to the Core-confirmed boolean for NATS/SDK callers.

### HTTP Consumer Interface

Add to the consumer-owned interface in `internal/modules/devices/api/register.go`:

```diff
 type Devices interface {
     GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
+    SetEntityEnabled(context.Context, devices.EntityID, bool) (devices.EntityWithState, error)
     ExecuteCommand(context.Context, devices.EntityID, devices.OperationName, devices.CommandParameters) (devices.CommandResult, error)
```

Register `PATCH /entities/{entity_id}` beside the existing GET and Command routes. Handlers translate only IDs, DTOs, domain errors, and Problem Details; enablement decisions remain in the service/repository.

### NATS Consumer Interface

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

The server owns request decoding, subject/payload matching, trace extraction/injection, domain error mapping, response encoding, logging, and subscription drain. Application assembly starts and drains it alongside `RegistrationServer`.

### SDK Method

Owner: `sdk/adapter/session.go`.

```go
func (session *Session) SetEntityEnabled(
    ctx context.Context,
    entityID string,
    enabled bool,
) (bool, error)
```

The method performs one schema-validated Core NATS request/reply attempt and returns the accepted Core value. It returns `ValidationError` for local contract/ID failures and `EntityEnablementRejectedError` for permanent Core rejection.

No SDK state cache, handler, callback, subscription, or publication gate is added.

## Persistence

### Migration `00003_entity_enablement.sql`

Add immutable Goose migration `internal/platform/db/migrations/00003_entity_enablement.sql`.

The Up migration must:

1. add `entities.enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))` so existing Entities become enabled;
2. rebuild `commands` to admit `entity_disabled` in both status and failure-code checks while preserving all rows and the current `(entity_id, requested_at DESC, id DESC)` index from migration 00002;
3. rebuild `observation_receipts` and `entity_states` together so the receipt check admits `entity_disabled` while every State foreign key continues to reference the copied receipt ID and receive order;
4. preserve observation `receive_order`, uniqueness, current-State receipt references, Command foreign keys, timestamps, and every existing check constraint; and
5. run with foreign-key checking enabled and prove `PRAGMA foreign_key_check` is empty after migration.

The Down migration must:

1. map `entity_disabled` Commands to terminal `internal_failure` / `internal_error`, because the prior schema cannot represent the newer outcome;
2. delete every receipt whose rejection code is `entity_disabled` before rebuilding the receipt table; the delete must fail on an unexpected current-State reference, and migration tests prove the normal projection invariant leaves no such reference;
3. rebuild receipt/State and Command tables with the prior checks and indexes; and
4. remove `entities.enabled`.

Migration tests must cover empty databases and upgraded databases containing Devices, Entities, current State, Observation receipts, and every pre-existing Command status. Down-migration semantic loss is explicit and test-covered.

### Query Sources

Modify source SQL only; regenerate sqlc outputs.

`internal/platform/db/queries/registration/registration.sql`:

- select current `e.enabled` with existing Entity mappings;
- insert resolved initial enablement in `CreateEntity`; and
- never update enablement in `UpdateEntityDescriptor`.

`internal/platform/db/queries/state/state.sql`:

- select `e.enabled` in every Entity/current-State read;
- add the Entity enablement update used inside a repository transaction; and
- retain disabled Entities in all list/detail queries.

`internal/platform/db/queries/commands/commands.sql` keeps its insert shape; the repository chooses the inserted terminal fields. Generated Command models pick up the extended checks through the migration schema.

`internal/platform/db/queries/receipts/receipts.sql` keeps its insert shape; the new rejection code is passed through the existing nullable string field.

### Transaction Ownership

`SQLiteRepository.SetEntityEnabled` owns one serializable transaction:

1. load the Entity, current mapping, and nullable State;
2. return `ErrEntityNotFound` when absent;
3. when `RequiredOwner` is present, return `ErrEntityWrongAdapter` unless it equals the current mapping Adapter ID;
4. update `enabled` and generic `updated_at` only when the value changes;
5. load and return the transaction-consistent Entity/current-State view; and
6. commit.

`SQLiteRepository.CreateCommand` also becomes transactional. It checks current Entity enablement and inserts either the supplied `requested` record or its terminal `entity_disabled` form before committing. It returns the committed record so the service knows whether to create a waiter and dispatch.

Observation projection retains its existing serializable transaction and adds the disabled/active-linked classification before State normalization and projection.

## Command Service Changes

`Service.ExecuteCommand` keeps transport-independent validation and catalog resolution before durable creation.

After constructing the candidate `requested` record:

1. call transactional `repository.CreateCommand`;
2. when the returned status is `entity_disabled`, return `CommandExecutionError{CommandID: id, Err: ErrEntityDisabled}` immediately;
3. otherwise add the in-memory waiter before starting dispatch;
4. preserve the existing HTTP-disconnect, timeout, failure, and satisfying-Observation behavior.

Adding the waiter after the requested record commits is safe because Core has not dispatched the Command yet. Disabled records never allocate a waiter.

No Adapter cancellation message or new in-memory lifecycle is added.

## Registration Contract Changes

```diff
diff --git a/contracts/v1/registration-request.schema.json b/contracts/v1/registration-request.schema.json
@@
             "properties": {
@@
+              "initially_enabled": { "type": "boolean", "default": true },
```

```diff
diff --git a/contracts/v1/registration-response.schema.json b/contracts/v1/registration-response.schema.json
@@
-                "required": ["key", "entity_id"],
+                "required": ["key", "entity_id", "enabled"],
                 "properties": {
@@
+                  "enabled": { "type": "boolean" }
```

JSON Schema's `default` is descriptive; Core explicitly resolves omission to `true`.

The v1 registration response change is incompatible with an old strict SDK schema because `additionalProperties` is false. Core, embedded contracts, and the first-party SDK therefore ship atomically. This specification does not add dual schemas, compatibility aliases, or mixed-version negotiation. Existing stored Bindings and canonical IDs remain compatible.

## Project Layout

```text
CONTEXT.md                                                   # modify — canonical Entity enablement term (already approved)
contracts/v1/
├── common.schema.json                                       # modify — ena_ ID and causation
├── embed.go, embed_test.go                                  # modify — register and verify new schemas
├── entity-enablement-request.schema.json                    # new — SDK-to-Core setter request
├── entity-enablement-response.schema.json                   # new — accepted/rejected setter response
├── registration-request.schema.json                         # modify — creation-only initial enablement
└── registration-response.schema.json                        # modify — actual enablement in mappings
docs/architecture.md                                        # modify — accepted enablement behavior and contracts
internal/app/hearthd/
├── run.go                                                   # modify — start/drain enablement NATS server
├── server_test.go                                           # modify — PATCH and OpenAPI compatibility
└── simulator_matrix_integration_test.go                     # modify — assembled enable/disable scenario
internal/contracts/v1/natswire/
├── subjects.go                                              # modify — enablement subject/route
└── subjects_test.go                                         # modify — route validation
internal/modules/devices/
├── api/
│   ├── command.go, command_test.go                          # modify — 409 problem with durable Command ID
│   ├── errors.go                                            # modify — Problem Details extension
│   ├── mapping.go                                           # modify — enabled response field
│   ├── patch_entity.go, patch_entity_test.go                # new — typed Entity PATCH
│   ├── register.go                                          # modify — consumer method and route
│   ├── resource_reads_test.go                               # modify — disabled visibility
│   └── types.go                                             # modify — enabled, PATCH, problem DTOs
├── command.go, command_test.go                              # modify — atomic creation and disabled result
├── enablement.go, enablement_test.go                        # new — management/owner use cases
├── model.go, registration.go, registration_test.go          # modify — domain fields and initial semantics
├── nats/
│   ├── enablement.go, enablement_test.go                    # new — Core request/reply server
│   ├── registration.go, registration_test.go                # modify — initial/current mapping
│   └── wire.go                                              # modify — private enablement DTOs
├── observation.go                                           # modify — copy new domain fields
├── repository.go                                            # modify — mutation seam and errors
├── sqlite_observations.go, sqlite_observations_test.go      # modify — disabled projection and active exception
├── sqlite_reads.go, sqlite_reads_test.go                    # modify — map enabled
└── sqlite_repository.go, sqlite_repository_test.go          # modify — mutation/Command transactions and registration
internal/platform/db/
├── migrations/00003_entity_enablement.sql                   # new — enabled field and checked-enum rebuilds
├── queries/
│   ├── registration/registration.sql                       # modify — create/read enablement
│   └── state/state.sql                                     # modify — read/update enablement
├── sqlc/                                                    # regenerate — generated persistence code
└── db_test.go                                               # modify — migration upgrade/down/foreign-key coverage
sdk/adapter/
├── errors.go                                                # modify — permanent enablement rejection type
├── session.go, session_test.go                              # modify — one-attempt setter
└── types.go                                                 # modify — registration and setter DTOs
specs/entity-enablement.md                                   # new — this feature contract
```

No Entity-type manifest, typed facade, Home Assistant payload, configuration file, readiness rule, or new product module is added.

## Deliverables

| Deliverable | Effort | Depends On |
|---|---:|---|
| D1. Domain types, registration contract, migration, sqlc, and read mapping | L | — |
| D2. Atomic enablement mutation, disabled Observation projection, and Command creation | L | D1 |
| D3. Entity PATCH, disabled Command Problem Details, and OpenAPI | M | D1, D2 |
| D4. Adapter NATS setter and Go SDK method | L | D1, D2 |
| D5. Runtime race scenarios, architecture docs, and clean-checkout verification | L | D2, D3, D4 |

Total effort is **XL, approximately 3–5 focused days at 70% confidence**. The main risks are SQLite checked-table migration, cross-transaction race coverage, and custom 409 OpenAPI representation—not unfamiliar technology.

## Non-Goals

- Device enablement or child-Entity cascade behavior
- Entity retirement, household removal, archival, or permanent deletion
- Adapter availability, upstream presence, stale State, or discovery
- Registration omission changing enablement
- Binding release, ownership transfer, or Binding history
- Enablement transition history, provenance, timestamps, revisions, ETags, or source precedence
- A generic Entity patch engine or other mutable Entity fields
- Adapter enablement notifications, SDK state caching, local publication gating, polling, or resynchronization
- Adapter-side Command cancellation
- JetStream for enablement mutation or change events
- Disabled-Entity list filtering or hidden resources
- Authentication or authorization changes to the trusted HTTP interface
- Mixed-version Core/SDK registration compatibility

## Acceptance Criteria

### Registration and Reads

- [ ] Registration request schemas accept omitted, `true`, and `false` `initially_enabled` values and reject non-booleans.
- [ ] Omitted initial enablement creates an enabled Entity; explicit false creates a disabled Entity without a transient enabled commit.
- [ ] Re-registering an existing Entity with either initial value preserves current enablement.
- [ ] A lost registration response followed by retry preserves any intervening HTTP or SDK enablement change.
- [ ] Every submitted registration mapping returns actual current `enabled` in request order.
- [ ] Direct reads, Entity lists, and Device-detail embedded Entities include disabled Entities and required `enabled`, retaining last State.

### HTTP Mutation

- [ ] `PATCH /v1/entities/{entity_id}` accepts exactly one required boolean `enabled` field and returns the complete updated Entity.
- [ ] Missing, null, wrong-type, empty, and unknown fields produce Huma HTTP 422 validation responses.
- [ ] Invalid IDs return 400, unknown valid IDs return 404, and same-value writes return 200 without side effects.
- [ ] Runtime OpenAPI contains `update-entity`, required `enabled` request/response fields, and the specified errors.

### SDK Mutation

- [ ] The new schemas, subjects, Core server, and SDK round-trip accepted true and false values with valid correlation and causation.
- [ ] `Session.SetEntityEnabled` performs one request/reply attempt and returns the Core-confirmed boolean.
- [ ] Unknown Entity and wrong Adapter return distinct typed permanent errors.
- [ ] Route/payload mismatches and malformed envelopes never invoke the domain setter.
- [ ] Ownership verification and mutation are atomic.
- [ ] No notification subject, SDK cache, or local publication gate is introduced.

### Commands

- [ ] Invalid or unsupported requests against a disabled Entity return 400 and create no Command.
- [ ] A valid disabled attempt creates one terminal `entity_disabled` Command, allocates no waiter, sends no Adapter request, and returns 409 with stable `code` and `command_id`.
- [ ] The resulting Command is immediately available through direct and history reads with normalized parameters and exact terminal fields.
- [ ] A deterministic race test proves Command-first commit yields an active Command while disable-first commit yields a terminal disabled Command.
- [ ] A Command active before disabling can still be accepted, satisfied, failed, or timed out under existing rules.

### Observations

- [ ] An unlinked owning-Adapter Observation processed while disabled commits a rejected `entity_disabled` receipt, is acknowledged, and leaves State unchanged.
- [ ] Duplicate disabled Observations remain duplicate and create no second receipt.
- [ ] Unknown Entity and wrong Adapter retain precedence over disabled; disabled retains precedence over value validation outside the active-Command exception.
- [ ] A valid Observation linked to a matching requested/accepted Command may update State and satisfy it while disabled.
- [ ] A link to an unknown, mismatched, expired, interrupted, or otherwise terminal Command does not bypass disablement.
- [ ] Processing-time tests prove queued Observations follow enablement at transaction time in both disable and re-enable directions.
- [ ] Rejected receipt retention and current-State receipt pinning continue to satisfy existing invariants.

### Persistence and Verification

- [ ] Migration 00003 upgrades empty and populated databases without losing rows, IDs, receive order, State references, Command history, indexes, or foreign-key validity.
- [ ] Down migration has the specified explicit semantic mapping and leaves a schema accepted by the prior application.
- [ ] sqlc output is generated only from migration/query sources and passes generation checks.
- [ ] Repository methods return domain models without sqlc leakage and preserve owned-copy guarantees.
- [ ] The assembled runtime scenario covers registration, HTTP disable, disabled Command history, rejected Observation acknowledgement, active-Command completion, SDK re-enable, and subsequent State projection.
- [ ] Existing simulator, Home Assistant Adapter, health/readiness, registration, State, Command, history, and OpenAPI behavior remains green.
- [ ] `devenv test`, `git diff --check`, and final diff/status review pass.

## Test Strategy

| Layer | Required coverage |
|---|---|
| Contract | Registration initial/default/current fields; enablement request/response oneOf rules; `ena_` IDs; causation; malformed and extra fields. |
| Service | ID/Adapter validation, management and owner mutation, owned copies, registration default/copy semantics, disabled Command result without waiter. |
| Repository | Real migrated SQLite for mutation/no-op/ownership, registration retry, atomic Command classification, checked enums, and concurrent writer races. |
| Observation | Disabled rejection receipt, duplicate, precedence, active-linked exception, terminal-link rejection, disable/re-enable processing order. |
| HTTP | PATCH validation and mapping; disabled visibility; custom 409 Problem Details and OpenAPI; existing error regressions. |
| NATS/SDK | Subject parsing, schema round trips, ownership rejections, correlation/causation mismatch, one-attempt behavior, no local cache. |
| Runtime | Assembled SQLite + NATS/JetStream + Core servers + SDK + HTTP flow across disable and re-enable. |
| Migration/generation | Empty/up/down populated migration, foreign-key check, indexes, check constraints, reproducible sqlc output. |

Use service tests for transport-independent validation, migrated SQLite tests for every transactional claim, API tests for Huma-owned behavior, and runtime tests only for cross-seam risks. Do not duplicate the entire domain matrix through every transport.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| SQLite table rebuild drops or rewires current-State receipts | Medium | High | Rebuild receipt and State tables together; seed referenced/unreferenced receipts; assert IDs, receive order, foreign keys, and projections after upgrade/down. |
| Command slips in after disable or is incorrectly blocked before it | Medium | High | Make Command classification and insert one repository transaction; coordinate deterministic transactions in real SQLite race tests. |
| Active linked Observation bypass is too broad | Medium | High | Require exact Command ID, Entity, Adapter, and active status; test every mismatched and terminal case. |
| Strict v1 registration response breaks mixed Core/SDK versions | High under rolling upgrade | Medium | Ship monorepo Core/contracts/SDK atomically; declare mixed versions unsupported; add cross-binary contract tests. |
| Disabled Adapter traffic consumes SQLite/consumer capacity | Medium | Medium | Acknowledge after a minimal receipt transaction; retain current bounded receipt pruning; treat broader noisy-Adapter isolation as a separate product problem. |
| PATCH route implies future mutable fields | Medium | Low | Accept only the specified typed field; require future ownership/reconciliation decisions before extending it. |
| Last-writer-wins obscures which actor changed enablement | Medium | Low | Keep this version intentionally current-state-only; rely on request diagnostics and add persisted provenance only after a concrete operator need. |

## Trade-offs

| Chose | Over | Reason |
|---|---|---|
| Enabled/disabled | Retirement lifecycle | The requested behavior is reversible operational participation, not final household removal. |
| One current boolean | source holds, provenance, or history | It is the minimum state that supports both writers and last-commit-wins. |
| Core-only enforcement | Adapter notification and local gating | It remains correct across disconnects without a second synchronization protocol. |
| Active Command draining | rejecting disable or claiming cancellation | Disabling is immediate for new work without starvation or false physical-cancellation claims. |
| Terminal disabled Command record | precondition-only 409 | The user chose durable evidence for every valid attempted Command. |
| Processing-time Observation policy | enablement generations/fences | It avoids wire generations and JetStream coordination and matches the chosen queued-message semantics. |
| Typed Entity PATCH | enablement action routes or generic patch engine | It establishes a resource update seam without speculative fields or machinery. |
| Core NATS request/reply | JetStream mutation | The idempotent immediate setter needs correlated authority, not durable asynchronous work. |
| Distinct SDK ownership errors | silent no-op or collapsed rejection | Trusted operators need to distinguish stale identity from wrong ownership. |
| In-place v1 contract update | dual-version negotiation | The first-party monorepo ships Core and SDK together and has no approved mixed-version requirement. |

## Success Metrics

- A management client can disable and re-enable an Entity without changing its canonical ID, Binding, metadata, history, or last State.
- An owning Adapter can choose initial enablement and later set it explicitly through the typed SDK; another Adapter cannot.
- Disabled Observation traffic is acknowledged without changing State, while pre-disable Commands retain deterministic outcomes.
- Every valid disabled Command attempt is durably inspectable by the ID returned in its HTTP 409 response.
- All accepted race outcomes are proven against real SQLite, and the clean-checkout validation gate passes without generated drift.

## Verification

Use repository tasks rather than invoking underlying generation, formatting, lint, test, or vet tools directly:

```sh
devenv tasks run --mode single hearth:generate
devenv tasks run --mode single hearth:format
devenv test
git diff --check
git status --short
```

## Open Questions

None. Device enablement, removal, availability, notification, provenance, and mixed-version support are explicitly deferred rather than left unresolved.

---
*Spec approved for task decomposition.*
