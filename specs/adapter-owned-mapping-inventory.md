# Adapter-owned mapping inventory implementation spec

**Status:** Draft for review
**Type:** Feature plan
**Effort:** L, approximately 1-2 focused days at 80% confidence
**Date:** 2026-09-01
**Baseline:** branch `z2m` at `fd9d55b`

## Problem and decision

A stateless Adapter can re-register external objects it still sees, but it cannot recover the canonical IDs of Bindings and Entities that disappeared while it was offline. Core owns those mappings and assigns canonical IDs. The Adapter SDK owns no durable state. After restart, absent objects therefore remain `unknown` because the new runtime cannot identify them.

The household HTTP Entity inventory is for management clients. It does not expose adapter-scoped Binding and Entity keys, so it cannot fill this gap.

Add a runtime-scoped, paginated Core NATS read for the calling Adapter instance's persisted mappings. Expose one page at a time through `Session.ListOwnedMappings`:

```text
(binding_key, device_id, entity_key, entity_id)
```

Order rows by `(binding_key ASC, entity_key ASC)`. Flat rows bound each page even though additive registration does not bound one Binding's lifetime Entity count. The read is deliberately narrow. It returns no descriptors, external IDs, State, availability, or enablement.

This is generic Core and SDK behavior with no Zigbee2MQTT vocabulary. It is a prerequisite for `zigbee2mqtt-adapter.md`.

## Behavioral contract

### Ownership and runtime fencing

- Adapter ID and runtime ID come only from the NATS subject.
- Core verifies that the subject runtime is the Adapter instance's active online runtime before reading mappings.
- Unknown, released, expired, and superseded runtimes receive the schema-defined `runtime_fenced` rejection.
- The repository query filters on the subject Adapter ID. An Adapter never receives another Adapter's rows.
- Runtime ID is fencing evidence, not an authentication credential. NATS account permissions remain the transport security boundary.
- Runtime verification precedes the repository read. The read has no side effects and needs no write transaction. If the supervisor expires the runtime after verification, that runtime may receive the authorized page. Later Adapter writes remain transactionally fenced.

### Pagination and cursor

- Wire `limit` is optional, defaults to 50, and accepts 1-200. An explicit wire value outside that range is schema-invalid.
- SDK `Limit == 0` omits wire `limit` and requests the default. Other SDK values outside 1-200 fail locally with `ValidationError`.
- `cursor` is optional and opaque to SDK callers.
- Core fetches `limit + 1` rows, returns at most `limit`, and includes `next_cursor` only when another row exists.
- No mappings, or a position after the final row, returns `items: []` without a cursor.
- Invalid cursor encoding, version, resource, Adapter scope, Binding key, Entity key, or trailing JSON receives `invalid_cursor`.

The Core NATS transport owns this unsigned, unpadded base64url JSON cursor:

```go
type ownedMappingCursor struct {
    Version    int    `json:"v"`
    Resource   string `json:"resource"`
    AdapterID  string `json:"adapter_id"`
    BindingKey string `json:"binding_key"`
    EntityKey  string `json:"entity_key"`
}
```

`Version` must equal `1`; `Resource` must equal `adapter_owned_mappings`. The cursor carries position, not authority.

### Retry and failure behavior

- The SDK schema-validates one request envelope and retries that envelope after transient no-response or reconnect failures until the call context ends.
- Retries preserve request ID, correlation ID, payload, and cursor.
- `runtime_fenced` fences the Session and returns `adapter.ErrRuntimeFenced`.
- `invalid_cursor` returns permanent `OwnedMappingsRejectedError` and is not retried.
- Core discards schema-invalid requests under the existing request/reply policy.
- Repository or response-publication failures produce no accepted response. The SDK treats no response as transient until its context ends.

## Wire contract

### Subject

```text
hearth.v1.adapter.<adapter_id>.runtime.<runtime_id>.mappings
```

Wildcard:

```text
hearth.v1.adapter.*.runtime.*.mappings
```

### Request

Schema ID: `urn:hearth:schema:owned-mappings-request:v1`

```json
{
  "id": "map_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "schema": "urn:hearth:schema:owned-mappings-request:v1",
  "emitted_at": "2026-09-01T12:00:00Z",
  "correlation_id": "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "data": {
    "limit": 50,
    "cursor": "eyJ2IjoxLC4uLn0"
  }
}
```

`data` allows no additional properties. Both fields are optional. When present, `cursor` is a non-empty string of at most 2048 bytes.

### Response

Schema ID: `urn:hearth:schema:owned-mappings-response:v1`

Accepted example:

```json
{
  "id": "rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "schema": "urn:hearth:schema:owned-mappings-response:v1",
  "emitted_at": "2026-09-01T12:00:00Z",
  "correlation_id": "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "causation_id": "map_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "data": {
    "status": "accepted",
    "items": [
      {
        "binding_key": "z2m-00124b0024abcdef",
        "device_id": "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
        "entity_key": "power",
        "entity_id": "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
      }
    ],
    "next_cursor": "eyJ2IjoxLC4uLn0"
  }
}
```

`items` is required, contains 0-200 rows, and preserves repository order. Omit `next_cursor` on the terminal page.

Rejected response data:

```json
{
  "data": {
    "status": "rejected",
    "error": {
      "code": "runtime_fenced",
      "message": "adapter runtime is no longer active"
    }
  }
}
```

The only rejection codes are `runtime_fenced` and `invalid_cursor`. Messages follow the existing bounded safe-message contract. Accepted and rejected response shapes are exclusive.

Add `map_id` to `common.schema.json` and include it in `causation_id`, so a response can identify its request.

## Types and interfaces

### Domain and repository

Owner: `internal/modules/devices/ownership.go`.

```go
type OwnedMapping struct {
    BindingKey string
    DeviceID   DeviceID
    EntityKey  string
    EntityID   EntityID
}

type OwnedMappingPosition struct {
    BindingKey string
    EntityKey  string
}

type OwnedMappingPageParams struct {
    After *OwnedMappingPosition
    Limit int
}

type ListOwnedMappingsParams struct {
    AdapterID string
    After     *OwnedMappingPosition
    Limit     int
}
```

`OwnedMappingPosition` is all-or-nothing. Binding and Entity keys follow the registration slug contract. Returned slices belong to the caller.

The service method is:

```go
func (service *Service) ListOwnedMappings(
    ctx context.Context,
    adapterID string,
    runtimeID RuntimeID,
    page OwnedMappingPageParams,
) (Page[OwnedMapping], error)
```

It validates Adapter ID, runtime ID, limit, and position completeness; verifies the active runtime through `AdapterReader`; builds repository parameters from the subject Adapter ID; and returns `ErrRuntimeFenced`, `ErrInvalidPage`, or the repository error without transport vocabulary.

Add the persistence capability to `Stores` and `SQLiteStores`:

```go
type OwnedMappingRepository interface {
    AdapterReader
    ListOwnedMappings(context.Context, ListOwnedMappingsParams) (Page[OwnedMapping], error)
}
```

```diff
 type Stores struct {
     Registration RegistrationRepository
+    OwnedMappings OwnedMappingRepository
 }
```

`SQLiteRepository` implements this interface with one indexed keyset query over `adapter_entity_mappings`, joined to `adapter_bindings` for `device_id`. No sqlc type crosses the repository seam.

### Core NATS server

Owner: `internal/modules/devices/nats/owned_mappings.go`.

```go
type OwnedMappingLister interface {
    ListOwnedMappings(
        context.Context,
        string,
        devices.RuntimeID,
        devices.OwnedMappingPageParams,
    ) (devices.Page[devices.OwnedMapping], error)
}

func StartOwnedMappingsServer(
    connection *nats.Conn,
    validator *contractsv1.Validator,
    lister OwnedMappingLister,
    logger *slog.Logger,
) (*OwnedMappingsServer, error)
```

The server owns subject parsing, wire/domain mapping, cursor encoding and decoding, default-limit application, rejection mapping, tracing, correlation, and response publication through the existing request/reply server. Application assembly starts and drains it with the session, registration, availability, and enablement servers.

### SDK

Owner: `sdk/adapter/types.go`, `errors.go`, and `session.go`.

```go
type OwnedMappingPageRequest struct {
    Limit  int
    Cursor string
}

type OwnedMapping struct {
    BindingKey string `json:"binding_key"`
    DeviceID   string `json:"device_id"`
    EntityKey  string `json:"entity_key"`
    EntityID   string `json:"entity_id"`
}

type OwnedMappingPage struct {
    Items      []OwnedMapping `json:"items"`
    NextCursor string         `json:"next_cursor,omitempty"`
}

type OwnedMappingsRejectionCode string

const OwnedMappingsInvalidCursor OwnedMappingsRejectionCode = "invalid_cursor"

type OwnedMappingsRejectedError struct {
    Code    OwnedMappingsRejectionCode
    Message string
}

func (session *Session) ListOwnedMappings(
    ctx context.Context,
    request OwnedMappingPageRequest,
) (OwnedMappingPage, error)
```

The SDK owns local limit validation, subject construction, schema encoding and decoding, transient retry, fencing, and response identity validation. It returns one page and does not cache mappings or collect all pages. Runtime fencing continues to use `adapter.ErrRuntimeFenced`, not `OwnedMappingsRejectedError`.

## Persistence

Add this generated query:

```sql
-- name: ListOwnedMappings :many
SELECT m.binding_key, b.device_id, m.entity_key, m.entity_id
FROM adapter_entity_mappings AS m
JOIN adapter_bindings AS b
  ON b.adapter_id = m.adapter_id AND b.binding_key = m.binding_key
WHERE m.adapter_id = sqlc.arg(adapter_id)
  AND (
    NOT sqlc.arg(has_after)
    OR m.binding_key > sqlc.arg(after_binding_key)
    OR (
      m.binding_key = sqlc.arg(after_binding_key)
      AND m.entity_key > sqlc.arg(after_entity_key)
    )
  )
ORDER BY m.binding_key ASC, m.entity_key ASC
LIMIT sqlc.arg(result_limit);
```

The repository requests `limit + 1`, sets `HasMore`, and returns at most `limit`. The existing primary key on `(adapter_id, binding_key, entity_key)` covers filtering and order. No migration is required.

## Implementation map

| Area | Changes |
|---|---|
| `contracts/v1` | Add `owned-mappings-request.schema.json` and `owned-mappings-response.schema.json`; update `common.schema.json`, `embed.go`, and validator tests. |
| `internal/contracts/v1/natswire` | Update `subjects.go` and its tests with the mappings subject and parser. |
| `internal/modules/devices` | Add `ownership.go` and tests; update `repository.go`, `service.go`, `sqlite_repository.go`, and SQLite integration tests. Add the query to `dbqueries/registration.sql` and regenerate `dbsqlc`. |
| `internal/modules/devices/nats` | Add `owned_mappings.go` and tests; add wire DTOs to `wire.go`. |
| `internal/app/hearthd` | Update `run.go` and integration tests to start and drain the server. |
| `sdk/adapter` | Update `types.go`, `errors.go`, `session.go`, and tests with page and rejection types plus the retrying method. |
| `docs/architecture.md` | Record the accepted generic mapping read after implementation. |

Implement in dependency order: domain and SQLite inventory; schemas, subject, cursor, and Core server; then SDK, assembly, documentation, and end-to-end tests.

## Verification and acceptance

The focused tests must protect these behaviors:

| Layer | Required evidence |
|---|---|
| Domain | Invalid pages and inactive runtimes fail before the mapping read. |
| SQLite | Only the Adapter's rows appear in stable order; composite pagination has no skips, repeats, or duplicates, including equal Binding prefixes. |
| Contract | Requests and responses enforce fields, bounds, causation, and accepted/rejected exclusivity; 0 and 200 response items pass while 201 fails. |
| Cursor | Malformed base64 or JSON, trailing JSON, wrong version/resource/Adapter, and malformed keys return `invalid_cursor`. |
| NATS | Subject identity controls scope; fenced reads reject; repository and publication failures do not produce accepted responses. |
| SDK | Transient retries reuse the complete envelope, pages stay explicit, permanent cursor rejection is not retried, and cancellation ends retry. |
| Integration | A claimed Adapter lists only committed registrations through real NATS request/reply and SQLite. |

Acceptance checklist:

- [ ] Common schemas accept `map_` request IDs and response causation IDs.
- [ ] Subject builders and parsers accept only the runtime-scoped mappings route.
- [ ] An active runtime with no mappings receives an accepted empty terminal page.
- [ ] Limits 1 and 200 work. SDK `Limit == 0` requests the wire default of 50; negative and greater-than-200 SDK values fail locally; explicit wire `0` is schema-invalid.
- [ ] Rows follow `(binding_key, entity_key)` order and pagination has no gaps or duplicates.
- [ ] One Adapter never receives another Adapter's rows.
- [ ] Unknown, released, expired, and superseded runtimes receive `runtime_fenced`.
- [ ] SDK retries preserve request identity and payload, stop on context cancellation, and fence the Session under existing behavior.
- [ ] The query uses the existing index and needs no migration.
- [ ] `hearthd` starts and drains the new server.
- [ ] Existing Adapter operations and first-party adapters remain source compatible.
- [ ] Focused mutation testing and `mise run validate` pass.

After adding tests, run Gremlins at the smallest affected scope under `./internal/modules/devices/...` and investigate surviving behavioral mutants. Finish with `mise run validate`.

## Non-goals

This feature does not add deletion or retirement; descriptor, State, health, availability, enablement, or external-ID inventory; cross-Adapter management reads; authentication changes; Adapter-owned durable caching; mapping-change notifications; or an SDK collect-all helper.

## Open questions

None.
