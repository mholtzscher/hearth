# Resource Discovery and Command History — Implementation Spec

**Status:** Ready for task breakdown
**Type:** Feature plan
**Effort:** XL (approximately 2–4 focused days, 75% confidence)
**Approved by:** User design confirmation
**Date:** 2026-08-26
**Baseline:** `main` at `4af8ede`

## Problem

Hearth's HTTP API can read or command an Entity only when the caller knows its canonical ID. Although SQLite stores Devices, Entities, current Entity States, and non-replayable Command records, clients cannot discover resources, retrieve a Device aggregate, or recover a durable Command outcome after losing the synchronous response.

This feature serves Hearth CLI and browser clients for one trusted household deployment.

## Decision

Add five read endpoints:

```text
GET /v1/entities
GET /v1/devices
GET /v1/devices/{device_id}
GET /v1/commands/{command_id}
GET /v1/entities/{entity_id}/commands
```

Entities remain top-level canonical resources because their globally unique IDs independently address State and Commands. Device membership is represented by `device_id`, Entity filtering, and Entities embedded in Device detail.

Collection endpoints use endpoint-scoped opaque cursor pagination, return full product representations without totals, and default `limit` to 50 with a range of 1–200. Device detail embeds every associated Entity using the existing Entity representation. Command responses omit adapter and correlation identifiers.

No new product data is persisted. Add only read queries and supporting indexes, mapping sqlc rows into domain models at the existing SQLite repository seam.

## Public HTTP Contract

Errors use Huma's standard RFC 9457 Problem Details model. Malformed JSON and handler-level semantic validation return HTTP 400; Huma structural validation returns HTTP 422; handlers preserve the endpoint-specific 404 and 5xx status mappings below. The API defines no custom error codes or Command-ID extension fields.

### Collection pagination

`GET /v1/entities`, `GET /v1/devices`, and `GET /v1/entities/{entity_id}/commands` accept:

| Query | Required | Contract |
|---|---:|---|
| `limit` | no | Integer; default 50; minimum 1; maximum 200. |
| `cursor` | no | Opaque endpoint-specific continuation token returned by the preceding page. |

Each response has an `items` array, including when empty, and omits `next_cursor` when no later page exists. Device detail applies the same 1–200 bounds to `entity_limit` (default 50), accepts `entity_cursor`, and omits `next_entity_cursor` when no later embedded Entity exists. No endpoint returns a total count.

Cursors are base64url-without-padding encodings of versioned JSON position documents. A cursor is valid only for its endpoint and applicable parent Device, parent Entity, or optional Entity `device_id` filter. Invalid encoding, version, scope, canonical ID, fields, UTC timestamp, or trailing JSON returns an HTTP 400 Problem Details response. Cursors are unsigned because this API remains trusted and loopback-bound; they convey position, not authority.

Private cursor types owned by `internal/modules/devices/api/pagination.go`:

```go
type idCursor struct {
	Version  int    `json:"v"`
	Resource string `json:"resource"`
	ID       string `json:"id"`
	DeviceID string `json:"device_id,omitempty"`
}

type commandCursor struct {
	Version     int    `json:"v"`
	Resource    string `json:"resource"`
	EntityID    string `json:"entity_id"`
	RequestedAt string `json:"requested_at"`
	ID          string `json:"id"`
}
```

`Version` is exactly `1`. `Resource` is `devices`, `entities`, `device_entities`, or `entity_commands`. Entity-list cursors copy the request's optional `device_id`; Device-detail Entity cursors copy the path Device ID and use their distinct resource scope; Command cursors copy the path Entity ID. Cursor codecs remain private to transport and do not enter service or repository interfaces.

The repository fetches `limit + 1` rows, returns at most `limit`, and sets `Page.HasMore` from the extra row. Transport derives `next_cursor` from the final returned item when `HasMore` is true; cursor response fields use `omitempty`.

### `GET /v1/entities`

- **Operation ID:** `list-entities`
- **Tag:** `Entities`
- **Summary:** `List Entities and their current State`
- **Errors:** HTTP 400 for invalid cursors or Device IDs; HTTP 422 for structural validation; HTTP 500 for internal failures

Optional `device_id` is a canonical Hearth Device ID. Omission lists all household Entities; an unknown valid Device ID returns an empty collection.

Items use the existing `EntityBody` exactly, including full `support` and nullable current `state`. Results are ordered by canonical `id ASC`; subsequent pages select `id > cursor.id` under the same Device filter. This tie-free ID ordering defines the contract and is approximately creation ordered because IDs use UUIDv7.

### `GET /v1/devices`

- **Operation ID:** `list-devices`
- **Tag:** `Devices`
- **Summary:** `List Devices`
- **Errors:** HTTP 400 for invalid cursors; HTTP 422 for structural validation; HTTP 500 for internal failures

Items contain only `id`, `kind`, and `name`. Results are ordered by canonical `id ASC`; subsequent pages select `id > cursor.id`.

### `GET /v1/devices/{device_id}`

- **Operation ID:** `get-device`
- **Tag:** `Devices`
- **Summary:** `Get a Device and its Entities`
- **Errors:**
  - Invalid ID or Entity cursor: HTTP 400 Problem Details
  - Structural validation: HTTP 422 Problem Details
  - Unknown valid ID: HTTP 404 Problem Details, `device not found`
  - Other: HTTP 500 Problem Details, `internal error`

The response contains `id`, `kind`, `name`, `entities`, and optional `next_entity_cursor`. Entities use `EntityBody` exactly and are ordered by canonical `id ASC`. `entity_limit` defaults to 50 and accepts 1–200; `entity_cursor` continues after the final Entity from the preceding Device-detail page. Device-detail cursors are scoped to this endpoint and Device and cannot be exchanged with `GET /v1/entities` cursors.

Registration remains additive without a persisted aggregate limit. Device detail fetches only `entity_limit + 1` associated Entities, so query and response work remain bounded regardless of aggregate size.

### Command representation

Both Command endpoints use:

```go
type CommandRecordBody struct {
	ID                   string         `json:"id"`
	EntityID             string         `json:"entity_id"`
	Operation            string         `json:"operation"`
	Parameters           map[string]any `json:"parameters"`
	Status               string         `json:"status"`
	RequestedAt          string         `json:"requested_at"`
	DeadlineAt           string         `json:"deadline_at"`
	AcceptedAt           *string        `json:"accepted_at,omitempty"`
	CompletedAt          *string        `json:"completed_at,omitempty"`
	OutcomeObservationID *string        `json:"outcome_observation_id,omitempty"`
	FailureCode          *string        `json:"failure_code,omitempty"`
}
```

Timestamps are UTC RFC3339Nano, and `parameters` is the normalized persisted operation-parameter object. Optional fields mirror persistence: `accepted_at` when recorded; `completed_at` for terminal statuses; `outcome_observation_id` only for `satisfied`; and `failure_code` only for failed terminal statuses.

`adapter_id` and `correlation_id` remain internal diagnostics. Responses do not synthesize an outcome value because historical Observation values are not persisted and current State may have advanced beyond the satisfying Observation.

### `GET /v1/commands/{command_id}`

- **Operation ID:** `get-command`
- **Tag:** `Commands`
- **Summary:** `Get a Command record`
- **Errors:**
  - Invalid ID: HTTP 400 Problem Details, `command_id must be a canonical Hearth Command ID`
  - Structural validation: HTTP 422 Problem Details
  - Unknown valid ID: HTTP 404 Problem Details, `command not found`
  - Other: HTTP 500 Problem Details, `internal error`

Returns `CommandRecordBody` as audit evidence without changing or replaying the Command.

### `GET /v1/entities/{entity_id}/commands`

- **Operation ID:** `list-entity-commands`
- **Tag:** `Commands`
- **Summary:** `List an Entity's Command history`
- **Errors:** HTTP 400 for invalid IDs or cursors; HTTP 422 for structural validation; HTTP 404 for an unknown Entity; HTTP 500 for internal failures

Items use `CommandRecordBody` and are ordered by `(requested_at DESC, id DESC)`. A subsequent page selects:

```sql
requested_at < cursor.requested_at
OR (requested_at = cursor.requested_at AND id < cursor.id)
```

Persisted `requested_at` values use a fixed nine-digit fractional-second UTC representation so SQLite text ordering preserves nanosecond instants; migration 00002 normalizes existing RFC3339Nano values before rebuilding the index. The ID tie-breaker makes equal timestamps deterministic. The service verifies the parent Entity before listing: an unknown valid Entity returns HTTP 404, while an existing Entity without Commands returns HTTP 200 with `items: []`.

## Domain Contract

Owner: `internal/modules/devices/model.go`.

```go
type EntityWithState struct {
	Entity Entity
	State  *State
}

type DeviceAggregate struct {
	Device   Device
	Entities Page[EntityWithState]
}

type GetDeviceParams struct {
	ID            DeviceID
	AfterEntityID *EntityID
	EntityLimit   int
}

type ListDevicesParams struct {
	AfterID *DeviceID
	Limit   int
}

type ListEntitiesParams struct {
	DeviceID *DeviceID
	AfterID  *EntityID
	Limit    int
}

type ListEntityCommandsParams struct {
	EntityID          EntityID
	BeforeRequestedAt *time.Time
	BeforeID          *CommandID
	Limit             int
}

type Page[T any] struct {
	Items   []T
	HasMore bool
}
```

`EntityWithState` models an Entity and its nullable current State; `DeviceAggregate` models a Device with those domain compositions. Both are transport-independent domain read objects: they contain no JSON tags, HTTP field choices, or response-shaping behavior. The API package alone translates them into `EntityBody`, `DeviceBody`, and `DeviceDetailBody`.

`Page` is shared internal domain vocabulary for collection use cases, including the embedded Device-detail Entity page, and never carries HTTP cursors. Service results own their slices, raw JSON bytes, and pointer fields; repository/sqlc storage must not alias returned mutable data.

Add to `internal/modules/devices/repository.go`:

```go
var (
	ErrDeviceNotFound  = errors.New("device not found")
	ErrInvalidPage     = errors.New("invalid page")
	// Existing: ErrEntityNotFound and ErrCommandNotFound.
)
```

`ErrInvalidPage` covers limits outside 1–200 and partially specified Command positions. Transport rejects cursor syntax before invoking the service. No new configuration or event types are required.

## Interfaces and Ownership

### Service use cases

Owner: new `internal/modules/devices/read.go`.

```go
func (service *Service) ListDevices(context.Context, ListDevicesParams) (Page[Device], error)
func (service *Service) GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error)
func (service *Service) ListEntities(context.Context, ListEntitiesParams) (Page[EntityWithState], error)
func (service *Service) GetCommand(context.Context, CommandID) (CommandRecord, error)
func (service *Service) ListEntityCommands(context.Context, ListEntityCommandsParams) (Page[CommandRecord], error)
```

The service:

- validates canonical path, filter, and position IDs even outside HTTP;
- requires limits from 1 through 200;
- requires Command position timestamp and ID to be both absent or present and normalizes the timestamp to UTC;
- calls `repository.GetEntity` before listing an Entity's Commands;
- returns owned mutable data; and
- performs no writes or NATS publication.

### Repository seam

Add to the existing `Repository` in `internal/modules/devices/repository.go`:

```go
ListDevices(context.Context, ListDevicesParams) (Page[Device], error)
GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error)
ListEntities(context.Context, ListEntitiesParams) (Page[EntityWithState], error)
GetEntity(context.Context, EntityID) (EntityWithState, error)
GetCommand(context.Context, CommandID) (CommandRecord, error)
ListEntityCommands(context.Context, ListEntityCommandsParams) (Page[CommandRecord], error)
```

Repository methods are named for domain resources and return only domain models or transport-independent domain compositions. They do not expose API DTOs or encode endpoint response shapes. The existing Entity/current-State lookup is renamed to `GetEntity` and remains the parent-existence lookup. The SQLite implementation owns `limit + 1` querying, row mapping, `HasMore`, truncation, context propagation, and persistence-error mapping; sqlc types remain inside the adapter.

`GetDevice` reads Device metadata for existence and then uses the indexed Device-filtered Entity query with `limit + 1`; this keeps each response bounded, distinguishes unknown Devices from empty or exhausted Entity pages, and avoids N+1 State loading. The concrete `SQLiteRepository.GetCommand` already exists; expose it through the domain repository and service.

### HTTP consumer and registration

Owner: new `internal/modules/devices/api/register.go`.

```go
type Devices interface {
	GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error)
	ExecuteCommand(context.Context, devices.EntityID, devices.OperationName, devices.CommandParameters) (devices.CommandResult, error)
	ListDevices(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error)
	GetDevice(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error)
	ListEntities(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error)
	GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
	ListEntityCommands(context.Context, devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error)
}
```

The consumer-owned interface contains exactly this route family's use cases; `*devices.Service` satisfies it in production.

Application assembly changes from an `/v1/entities` group to `/v1`:

```diff
-entities := huma.NewGroup(api, "/v1/entities")
-devicesapi.Register(entities, devices)
+v1 := huma.NewGroup(api, "/v1")
+devicesapi.Register(v1, devices)
```

`devicesapi.Register` registers `/entities`, `/entities/{entity_id}`, `/entities/{entity_id}/commands`, `/devices`, `/devices/{device_id}`, and `/commands/{command_id}` on that group. Existing Entity-detail and Command-execution methods, paths, operation IDs, tags, summaries, and schemas remain unchanged. All seven operations retain Huma's generated HTTP 422 structural-validation response and use the standard `ErrorModel`; malformed JSON remains HTTP 400.

Echo/Huma construction and the version prefix remain owned by `hearthd`; the `devices` API package owns its operations. OpenAPI remains runtime-generated and is verified through runtime compatibility tests rather than a committed artifact.

## API Transport Types

Owner: `internal/modules/devices/api/types.go` plus endpoint-specific input/output types.

```go
type DeviceBody struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type DeviceDetailBody struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	Name     string       `json:"name"`
	Entities         []EntityBody `json:"entities"`
	NextEntityCursor *string      `json:"next_entity_cursor,omitempty"`
}

type EntityCollectionBody struct {
	Items      []EntityBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type DeviceCollectionBody struct {
	Items      []DeviceBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type CommandCollectionBody struct {
	Items      []CommandRecordBody `json:"items"`
	NextCursor *string             `json:"next_cursor,omitempty"`
}
```

Each endpoint has explicit Huma input/output types. List inputs use `default:"50" minimum:"1" maximum:"200"`; path IDs remain transport strings and handlers parse them into domain IDs. Handlers decode cursors, call one service use case, and translate domain results with `entityBody`, `deviceBody`, `deviceDetailBody`, and `commandRecordBody` mappers plus shared JSON helpers. These mappers are the only owners of response-specific field selection and JSON conversion; handlers contain no SQL or business decisions.

## Persistence

### Migration

Add immutable Goose migration `internal/platform/db/migrations/00002_resource_read_indexes.sql`:

```sql
-- +goose Up
-- Normalize RFC3339Nano requested_at values to nine fractional digits.
UPDATE commands SET requested_at = /* fixed-width UTC expression */;

CREATE INDEX entities_device_id_idx ON entities(device_id, id);
DROP INDEX commands_entity_requested_idx;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

-- +goose Down
DROP INDEX commands_entity_requested_idx;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC);
DROP INDEX entities_device_id_idx;
```

Primary keys already support direct lookups and household-wide ID pagination. The Entity index supports Device-filtered lists and embedded Device-detail pages; the extended Command index supports deterministic history ordering. New Command writes use the same fixed-width UTC representation normalized by the migration.

### sqlc query sources

Modify `internal/platform/db/queries/state/state.sql`:

- `ListDevices`: `id > after_id`, `ORDER BY id ASC`, caller-provided `limit + 1`.
- `GetDevice`: direct Device metadata lookup for existence and detail fields; embedded Entities use `ListEntitiesByDevice` pagination.
- `ListEntities`: household Entities and current States after an Entity ID.
- `ListEntitiesByDevice`: the same domain data constrained by Device ID.
- Rename the existing Entity/current-State query to `GetEntity` so generated persistence names follow the domain repository vocabulary.

Modify `internal/platform/db/queries/commands/commands.sql`:

- `ListEntityCommandsFirstPage`: Entity-constrained, newest first.
- `ListEntityCommandsAfter`: Entity-constrained with strict `(requested_at, id)` keyset position.

Use explicit select lists. Regenerate affected sqlc packages; never hand-edit generated files. Command orchestration, projection, receipt-retention, and transaction ownership remain unchanged.

## Project Layout

```text
docs/architecture.md                                      # modify: accepted HTTP behavior
internal/app/hearthd/
├── server.go                                             # modify: pass /v1 group
└── server_test.go                                        # modify: route/OpenAPI compatibility
internal/modules/devices/
├── api/
│   ├── command_history.go, command_history_test.go       # new: Command reads
│   ├── devices.go, devices_test.go                       # new: Device reads
│   ├── list_entities.go, list_entities_test.go           # new: Entity list
│   ├── pagination.go, pagination_test.go                 # new: scoped cursors
│   ├── register.go                                       # new: consumer seam and registration
│   ├── get_entity.go, get_entity_test.go                 # modify: existing operations under /v1
│   └── types.go                                          # modify: read DTOs
├── model.go, repository.go                               # modify: domain and repository contract
├── observation.go, command.go                            # modify: use domain GetEntity operation
├── read.go, read_test.go                                 # new: read use cases
├── sqlite_observations.go                                # modify: domain naming for existing Entity read
├── sqlite_reads.go                                       # new: sqlc row mapping
└── sqlite_repository_test.go                             # modify: SQLite read behavior
internal/platform/db/
├── migrations/00002_resource_read_indexes.sql            # new
├── queries/{commands/commands.sql,state/state.sql}       # modify
└── sqlc/{commands,state}/                                # regenerate
specs/resource-discovery-and-command-history.md            # this spec
```

## Deliverables

| Deliverable | Effort | Depends On |
|---|---:|---|
| D1. Domain models, migration, sqlc queries, and SQLite reads | L | — |
| D2. Entity-list service and HTTP slice | M | D1 |
| D3. Device-list/detail service and HTTP slice | L | D1 |
| D4. Command-get/history service and HTTP slice | L | D1 |
| D5. `/v1` registration, OpenAPI regression coverage, docs, and verification | M | D2, D3, D4 |

Total: XL, approximately 2–4 focused days. The main estimate risk is public OpenAPI fixture breadth and sqlc row mapping, not novel technology.

## Scope Boundaries

- Reads do not add Device/Entity mutation or Command dispatch, cancellation, retry, replay, or asynchronous creation behavior; adapters remain authoritative through registration.
- Historical Observation/State values, adapter bindings and identifiers, external identifiers, correlation IDs, and availability remain private or unavailable.
- Collections support only the specified Device filter and ordering; no additional filters, sorts, offsets, or totals are added.
- The API remains trusted and loopback-bound by default; authentication and network exposure are unchanged.
- Entity IDs remain the canonical route identity. Device membership uses the chosen filter and embedding contracts rather than nested Entity routes.

## Verification Strategy

| Layer | Required coverage |
|---|---|
| Cursor unit | Round trips; versions; malformed input; endpoint/filter/parent scope; canonical ID and UTC timestamp validation. |
| Service unit | ID/page validation; unknown parent; repository parameters; owned copies; read-only behavior. |
| Repository integration | Domain-only interface contract; ordering and ties; `limit + 1`; Device filtering; nullable State and JSON mapping; Device aggregate; unknown records; indexes. |
| API unit | Routes and metadata; query defaults/bounds; empty arrays; cursors; domain-to-body mapping; DTOs; statuses/errors; omitted internal fields. |
| Application integration | Existing route compatibility; all seven runtime OpenAPI operations; native Huma 400/422 Problem Details; unchanged health/readiness. |
| Generation/migration | sqlc reproducibility; empty and upgraded database migration; down migration restores prior indexes. |

Use table-driven API/service tests and migrated temporary SQLite databases for persistence. Do not duplicate registration, State-projection, or Command-orchestration coverage through read handlers.

## Acceptance and Success Criteria

- [ ] A client with only the base URL can enumerate Devices and Entities, navigate from a Device to full Entity support/current State, use existing Entity commands, retrieve a Command by returned ID, and traverse an Entity's Command history.
- [ ] All five endpoints match the specified methods, paths, operation metadata, DTOs, ordering, cursor behavior, and declared errors; existing Entity detail and Command execution remain HTTP/OpenAPI compatible.
- [ ] Collection limits default to 50 and accept 1–200; pages fetch one extra row, return deterministic keyset order, always encode `items` as an array, and omit `next_cursor` on the last page.
- [ ] Cursor validation enforces encoding, version, endpoint, Device filter, parent Entity, canonical IDs, complete fields, and UTC timestamps with an HTTP 400 Problem Details response.
- [ ] Entity listing returns full `EntityBody` values and an unknown valid Device filter returns an empty page. Device listing returns metadata; Device detail returns bounded ordered Entity pages with endpoint- and Device-scoped cursors, and returns the specified 400/404 errors.
- [ ] Command responses expose only specified product fields and persisted state, without internal IDs or synthesized outcomes. History distinguishes unknown Entity from empty history and remains deterministic for equal timestamps.
- [ ] Service and repository reads preserve context cancellation, return owned domain data, perform no writes, and emit no NATS messages; API DTOs and sqlc types do not cross the repository seam, and only API mappers shape HTTP response bodies.
- [ ] The migration normalizes existing Command request timestamps, applies to empty and current databases, and reverses its index changes; sqlc output is regenerated from source.
- [ ] `docs/architecture.md` records the accepted behavior, and focused module/API/application/database tests cover the verification table.
- [ ] Under a stable database snapshot, Command-history traversal visits every matching record exactly once. Existing simulator, Home Assistant adapter, health/readiness, and current endpoint contracts remain unchanged.
- [ ] Repository gate passes: `devenv test`, then `git diff --check` and `git status --short` inspection.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Device join mapping duplicates or drops aggregate data | Medium | Medium | Explicit nullable-row handling and real SQLite tests for empty, stateless, stateful, and multi-Entity Devices. |
| Command parameters reveal household behavior if network exposure changes | Low on loopback | Medium | Preserve loopback scope; require authentication/authorization review before network exposure. |
| Concurrent registration changes data between pages | Medium | Low | Keyset ordering avoids insertion-before-cursor duplicates; no cross-request snapshot isolation is promised. |
| Registration regrouping changes existing OpenAPI | Medium | High | Assert runtime metadata, response schemas, and standard Problem Details for both existing operations. |

No unresolved product or architecture decisions remain.

---
*Spec approved for task decomposition.*
