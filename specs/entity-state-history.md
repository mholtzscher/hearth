# Entity State History — Implementation Spec

**Status:** Implemented
**Type:** Feature plan
**Effort:** XL (approximately 2–4 focused days, 75% confidence)
**Date:** 2026-09-04
**Baseline:** `bcc55d2`

## Problem Statement

**Who:** The technical self-hoster operating and diagnosing a Hearth household through the dashboard and HTTP API.

**What:** Hearth exposes only each Entity's current canonical State. SQLite retains Observation metadata, but not the normalized value associated with each accepted Observation, so an operator cannot inspect how State evidence changed over time.

**Why it matters:** Without recent State history, an operator cannot distinguish device behavior from adapter, normalization, or command-outcome problems. `EntityDetailPage` already presents Command and availability history, leaving State as the missing diagnostic timeline.

**Evidence:**

- `entity_states` contains one current row per Entity.
- `observations` contains each first-seen Observation's disposition and timestamps but no normalized State value or `source_updated_at`.
- The seven-day JetStream stream retains raw Observations but has no HTTP read path and is not a canonical-State query model.
- `resource-discovery-and-command-history.md` deliberately excluded historical values because they were not persisted.

The cost of not solving this is continued reliance on raw stream inspection for diagnosis and no dashboard view of recent canonical State updates.

## Proposed Solution

Enrich each first-seen Observation with the normalized State value, when the Observation advances canonical State, and with the Observation's optional source timestamp. The observation remains the single durable audit row: no parallel history table or second insert is introduced.

Expose retained observations through:

```text
GET /v1/entities/{entity_id}/state/history
```

The dashboard adds an Entity State history section with an endpoint-scoped filter, cursor pagination, a compact dependency-free SVG step chart, and a diagnostic table. History follows the existing Observation lifecycle: normally 30 days under the single Observation retention setting, while the observation backing current State remains preserved until a newer State supersedes it and pruning makes it eligible.

### Recorded observations

Each non-duplicate projection commits exactly one enriched observation in the existing projection transaction:

| Disposition | `state_value_json` | `rejection_code` | `source_updated_at` |
|---|---|---|---|
| `applied` | normalized canonical State value | `NULL` | copied when supplied |
| `unchanged` | normalized canonical State value | `NULL` | copied when supplied |
| `rejected` | `NULL`; raw rejected values are never persisted | set | copied when supplied |

A duplicate writes nothing because its original first-seen observation already owns the history record. `unchanged` is retained because Hearth's canonical State evidence, Observation ID, and timestamps advance even when its value is equivalent.

Rejected `unknown_entity` and `stale_runtime` observations remain valid observation records even when their requested Entity ID does not reference an existing Entity. `observations.entity_id` therefore remains an unconstrained canonical-ID-shaped string. The Entity-scoped read first verifies that its parent Entity exists and then matches retained observations by ID.

## Scope and Deliverables

| ID | Deliverable | Effort | Depends On |
|---|---|---:|---|
| D1 | Enrich Observations and projection persistence | M | — |
| D2 | Add domain, service, and SQLite Entity State history reads | M | D1 |
| D3 | Add the HTTP contract, cursor codec, and OpenAPI coverage | M | D2 |
| D4 | Add the dashboard chart, filters, table, and agent-browser verification | L | D3 |
| D5 | Record the accepted architecture and run repository gates | M | D1, D2, D3, D4 |

Total: **XL**, approximately 2–4 focused days. The principal estimate risks are chart edge cases, broad Go interface-fake updates, and end-to-end browser fixture preparation rather than unfamiliar technology.

## Non-Goals

- No history mutation, replay, or recovery from the JetStream stream.
- No reconstruction of values received before this feature starts recording them.
- No additional time-range filters, sorts, totals, offsets, or cross-Entity history queries.
- No separate State-history retention setting; history follows the single Observation retention setting.
- No new NATS subjects, JetStream consumers, SDK methods, or Adapter changes.
- No raw rejected value persistence or exposure.
- No authentication or network-exposure change.
- No new frontend dependency or frontend unit-test framework.

## Types

### Domain types

Owner: new `internal/modules/devices/entity_state_history.go`.

```go
// EntityStateHistoryEntry is one retained first-seen Observation for an Entity, newest-first by ReceiveOrder.
type EntityStateHistoryEntry struct {
	ObservationID     ObservationID
	Value             Value // Nil only when Disposition is rejected.
	Disposition       ObservationDisposition
	Rejection         *ObservationRejection
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	ObservedAt        time.Time
	ReceiveOrder      int64
}

// EntityStateHistoryFilter selects retained Observation dispositions for one Entity.
type EntityStateHistoryFilter string

const (
	EntityStateHistoryFilterUpdates   EntityStateHistoryFilter = "state-updates"
	EntityStateHistoryFilterAll       EntityStateHistoryFilter = "all"
	EntityStateHistoryFilterApplied   EntityStateHistoryFilter = "applied"
	EntityStateHistoryFilterUnchanged EntityStateHistoryFilter = "unchanged"
	EntityStateHistoryFilterRejected  EntityStateHistoryFilter = "rejected"
)

// ListEntityStateHistoryParams defines one keyset page of an Entity's retained State history.
type ListEntityStateHistoryParams struct {
	EntityID           EntityID
	Filter             EntityStateHistoryFilter
	BeforeReceiveOrder *int64
	Limit              int
}
```

`state-updates` means `applied` plus `unchanged`, matching Hearth's rule that either disposition advances canonical State evidence. An empty domain filter defaults to `state-updates`. `Value`, `SourceUpdatedAt`, and `Rejection` are returned as owned copies.

No event or configuration types change.

### HTTP response types

Owner: new `internal/modules/devices/api/entity_state_history.go`.

```go
type EntityStateHistoryBody struct {
	ObservationID     string  `json:"observation_id"`
	Value             any     `json:"value,omitempty"`
	Disposition       string  `json:"disposition"`
	RejectionCode     *string `json:"rejection_code,omitempty"`
	AdapterReceivedAt string  `json:"adapter_received_at"`
	SourceUpdatedAt   *string `json:"source_updated_at,omitempty"`
	ObservedAt        string  `json:"observed_at"`
}

type EntityStateHistoryCollectionBody struct {
	Items      []EntityStateHistoryBody `json:"items"`
	NextCursor *string                  `json:"next_cursor,omitempty"`
}
```

`value` is omitted for rejected rows. Accepted values are decoded from normalized JSON at the API mapping seam. All timestamps are UTC RFC3339Nano.

### Dashboard type

Modify `web/src/api/types.ts`:

```diff
+export interface EntityStateHistoryEntry {
+  observation_id: string;
+  value?: unknown;
+  disposition: "applied" | "unchanged" | "rejected" | string;
+  rejection_code?: string;
+  adapter_received_at: string;
+  source_updated_at?: string;
+  observed_at: string;
+}
```

## Interfaces

### Service use case

Owner: `internal/modules/devices/entity_state_history.go`.

```go
func (service *Service) ListEntityStateHistory(
	context.Context,
	ListEntityStateHistoryParams,
) (Page[EntityStateHistoryEntry], error)
```

The service:

- validates the canonical Entity ID;
- accepts limits from 1 through 200;
- defaults an empty filter to `state-updates` and rejects unknown filters;
- rejects `BeforeReceiveOrder` values below 1;
- calls `stores.Reads.GetEntity` first, returning `ErrEntityNotFound` for an unknown parent;
- calls the repository only after validation and parent lookup;
- returns owned mutable values and pointers; and
- performs no writes or NATS publication.

Invalid page or filter input returns `ErrInvalidPage`, preserving the existing read-service convention.

### Repository seam

Modify `internal/modules/devices/repository.go`:

```diff
 type ReadRepository interface {
     ListDevices(context.Context, ListDevicesParams) (Page[Device], error)
     GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error)
     ListEntities(context.Context, ListEntitiesParams) (Page[EntityWithState], error)
     GetEntity(context.Context, EntityID) (EntityWithState, error)
     GetCommand(context.Context, CommandID) (CommandRecord, error)
     ListEntityCommands(context.Context, ListEntityCommandsParams) (Page[CommandRecord], error)
+    ListEntityStateHistory(context.Context, ListEntityStateHistoryParams) (Page[EntityStateHistoryEntry], error)
 }
```

The SQLite adapter owns query selection, `limit + 1`, row mapping, timestamp parsing, `HasMore`, truncation, context propagation, and persistence-error wrapping. Generated sqlc types remain inside the adapter.

`ObservationRepository` does not change. Observation enrichment occurs inside its existing `ProjectObservation` implementation and transaction.

### HTTP input, handler, and registration

Owner: `internal/modules/devices/api/entity_state_history.go`.

```go
type ListEntityStateHistoryInput struct {
	EntityID    string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit       int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Cursor      string `query:"cursor"`
	Disposition string `query:"disposition" default:"state-updates"`
}

type ListEntityStateHistoryOutput struct {
	Body EntityStateHistoryCollectionBody
}
```

`Disposition` deliberately has no Huma enum tag because unsupported semantic filter values return HTTP 400 rather than structural-validation HTTP 422.

Modify the consumer-owned interface in `internal/modules/devices/api/register.go`:

```diff
 type Devices interface {
     // Existing use cases omitted.
+    ListEntityStateHistory(
+        context.Context,
+        devices.ListEntityStateHistoryParams,
+    ) (devices.Page[devices.EntityStateHistoryEntry], error)
 }
```

Register on the existing `/v1` group:

```text
GET /entities/{entity_id}/state/history
```

Operation metadata:

- **Operation ID:** `list-entity-state-history`
- **Tag:** `Entities`
- **Summary:** `List an Entity's State history`
- **Errors:** HTTP 400 for invalid IDs, cursors, pages, or filters; HTTP 422 for Huma structural validation; HTTP 404 with `entity not found`; HTTP 500 with `internal error`

The handler parses the Entity ID, decodes and verifies the cursor, calls one service use case, maps domain entries, and derives `next_cursor` from the final returned entry only when `HasMore` is true.

### Cursor interface

Owner: `internal/modules/devices/api/pagination.go`.

```go
type entityStateHistoryCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	ParentID     string `json:"parent_id"`
	ReceiveOrder int64  `json:"receive_order"`
	Filter       string `json:"filter"`
}
```

The cursor uses version `1`, resource `entity_state_history`, the path Entity ID, the effective filter, and the last returned `receive_order`. It is unsigned, unpadded canonical base64url JSON and conveys position rather than authority.

Bad encoding, noncanonical encoding, trailing or unknown JSON fields, wrong version/resource/parent/filter, an invalid parent ID, or `receive_order < 1` returns HTTP 400. A cursor cannot be reused after changing filters or Entities.

## Detailed Persistence Design

### Schema source of truth

Hearth has no deployments, so modify the existing initial Goose migration directly rather than adding an upgrade or compatibility migration. Existing development databases must be recreated. If Hearth gains a deployment before this feature lands, this decision must be revisited and an additive migration specified.

Modify `internal/platform/db/migrations/00001_initial.sql`:

```diff
 CREATE TABLE observations (
     receive_order       INTEGER PRIMARY KEY AUTOINCREMENT,
     observation_id      TEXT NOT NULL UNIQUE CHECK (substr(observation_id, 1, 4) = 'obs_'),
     adapter_id          TEXT NOT NULL,
     runtime_id          TEXT REFERENCES adapter_runtimes(runtime_id) ON DELETE RESTRICT,
     entity_id           TEXT NOT NULL,
     disposition         TEXT NOT NULL CHECK (
         disposition IN ('applied', 'unchanged', 'rejected')
     ),
     rejection_code      TEXT CHECK (
         rejection_code IS NULL OR rejection_code IN (
             'unknown_entity', 'wrong_adapter', 'entity_disabled',
             'invalid_value', 'stale_runtime'
         )
     ),
+    state_value_json    TEXT CHECK (
+        state_value_json IS NULL OR json_valid(state_value_json)
+    ),
     adapter_received_at TEXT NOT NULL,
+    source_updated_at   TEXT,
     observed_at         TEXT NOT NULL,
     CHECK (
-        (disposition = 'rejected' AND rejection_code IS NOT NULL)
-        OR (disposition <> 'rejected' AND rejection_code IS NULL)
+        (disposition = 'rejected'
+            AND rejection_code IS NOT NULL
+            AND state_value_json IS NULL)
+        OR (disposition <> 'rejected'
+            AND rejection_code IS NULL
+            AND state_value_json IS NOT NULL)
     )
 );

 CREATE INDEX observations_observed_at_idx
     ON observations(observed_at);
+
+CREATE INDEX observations_entity_history_idx
+    ON observations(entity_id, receive_order DESC);
+
+CREATE INDEX observations_entity_disposition_history_idx
+    ON observations(entity_id, disposition, receive_order DESC);
+
+CREATE INDEX observations_entity_updates_history_idx
+    ON observations(entity_id, receive_order DESC)
+    WHERE disposition IN ('applied', 'unchanged');
```

The Down section drops `observations`, so no additional Down operation is required beyond dropping the new index before the table if the explicit index-drop ordering is retained.

This design intentionally does not add an Entity foreign key. Observation idempotency and rejection auditing must work for unknown Entity IDs. Entity deletion is not currently a product operation; if introduced later, inaccessible observations expire through the existing retention path.

### Projection write

Modify `internal/modules/devices/dbqueries/observations.sql` so `InsertObservation` writes `state_value_json` and `source_updated_at`. Modify `ProjectObservation` in `internal/modules/devices/sqlite_observations.go` to pass:

- the normalized value for `applied` and `unchanged`;
- SQL `NULL` for `rejected`;
- the source timestamp for every disposition when supplied.

Classification already produces `normalized`, `disposition`, and `rejection` before `InsertObservation`, so no new write step and no `persistObservationState` signature change are needed. Observation insertion, State upsert, optional Command satisfaction, and commit remain one transaction.

Update the successful raw SQL observation fixtures in `internal/platform/db/db_test.go` and `internal/modules/devices/sqlite_reads_test.go` to include normalized `state_value_json` (`true` for their existing applied power Observations). The invalid-ID fixture in `db_test.go` also supplies normalized `state_value_json` so it still isolates the invalid-ID constraint rather than failing the new accepted-value constraint.

### History queries

Add three static query pairs to `internal/modules/devices/dbqueries/observations.sql`:

| Filter | First-page / continuation queries | Predicate and index |
|---|---|---|
| `all` | `ListEntityStateHistoryFirstPage` / `ListEntityStateHistoryAfter` | Entity equality; `observations_entity_history_idx` |
| `state-updates` | `ListEntityStateUpdatesFirstPage` / `ListEntityStateUpdatesAfter` | Entity equality and literal `disposition IN ('applied', 'unchanged')`; partial `observations_entity_updates_history_idx` |
| Single disposition | `ListEntityStateHistoryByDispositionFirstPage` / `ListEntityStateHistoryByDispositionAfter` | Entity and bound disposition equality; `observations_entity_disposition_history_idx` |

All select only the domain fields required by `EntityStateHistoryEntry`, order by `receive_order DESC`, and use the caller's `limit + 1`. Every continuation query additionally requires `receive_order < before_receive_order`. The SQLite adapter selects the query pair from the validated effective filter; the three single-disposition filters share one pair.

Filter predicates are exact:

| Filter | SQL disposition set |
|---|---|
| `state-updates` | `applied`, `unchanged` |
| `all` | `applied`, `unchanged`, `rejected` |
| `applied` | `applied` |
| `unchanged` | `unchanged` |
| `rejected` | `rejected` |

The service validates the filter before SQL. Bind Entity ID, cursor position, limit, and the single disposition where applicable; do not construct SQL dynamically or use a generic `OR`/`CASE` filter-switch predicate. The `state-updates` predicate must match the partial-index predicate literally so SQLite can use that index.

`LIMIT` bounds returned rows, not examined rows. Each query must seek into its matching index and read in result order without scanning unrelated dispositions or sorting the entire Entity history. This is important because Hearth shares one SQLite connection between reads, projection, Commands, and heartbeats. The two additional indexes trade write/storage overhead for selective reads without adding another write statement.

Verify first and continuation pages for every filter with `EXPLAIN QUERY PLAN`: require the corresponding indexed search and no temporary ordering B-tree, without asserting SQLite's full diagnostic string. Use small unchanged-heavy and rejected-heavy fixtures, including sparse and absent matches. Scale fixtures and concurrent latency measurements are excluded by the implementation review decision to keep validation fast; no scale or throughput guarantee is made.

### Retention

One Core setting bounds Observation retention: `observation_retention` in the hearthd YAML configuration, defaulting to 30 days and rejecting values below 8 days (above the seven-day JetStream retention). No per-row expiry is stored. Each startup or hourly prune deletes non-current observations with `observed_at < now - observation_retention`, including their State-history values, and preserves the observation referenced by current `entity_states`. Once a newer State replaces that reference, an already-aged older observation becomes eligible on the next pass. Retention changes therefore apply to already persisted observations without rewriting rows; increasing retention cannot restore already deleted history.

History therefore normally covers at least the configured retention window, not an unlimited audit log. One older current-State entry may remain as an anchor. Pruning is eligibility, not an exact TTL: a background startup pass applies a changed setting after recovery, followed by a shared pass one hour after each preceding pass completes. The app owns scheduling and joins the worker before SQLite closes; devices owns retention policy and deletion behavior. Sweeps never gate readiness. No separate history expiry field, archive, aggregate, soft delete, cascade, or additional prune loop is added.

## Public HTTP Contract

`GET /v1/entities/{entity_id}/state/history`

Query parameters:

| Query | Required | Contract |
|---|---:|---|
| `limit` | no | Default 50; minimum 1; maximum 200. |
| `cursor` | no | Opaque continuation token returned by the preceding page. |
| `disposition` | no | `state-updates` default, `all`, `applied`, `unchanged`, or `rejected`. |

Items order by `receive_order DESC`. Later pages use a strict lower receive order under the same Entity and filter. The response always encodes `items` as an array and omits `next_cursor` on the final page.

Pagination is not a snapshot. Observations inserted after the first page have higher receive orders and require restarting at the first page to see them. Pruning may remove entries not yet visited; `next_cursor` means more rows existed when that page was read, not that they will remain. A valid continuation cursor can therefore return HTTP 200 with `items: []` and no next cursor. Cursor positions need not reference an existing observation. No snapshot token, long-lived transaction, or retention pin is introduced.

An existing Entity with no retained matching observations returns HTTP 200 with `items: []`. An unknown valid Entity returns HTTP 404. The response exposes no Adapter ID, runtime ID, raw rejected value, observation expiry, or internal receive order.

## Dashboard Design

Owner: new `web/src/components/entity-state-history.tsx`, composed by `web/src/pages/EntityDetailPage.tsx` below Availability history.

### Filter and pagination

A segmented control offers `State updates` (default), `All`, `Applied`, `Unchanged`, and `Rejected`. Changing the filter, Entity route, or `useBaseUrlVersion` resets the cursor to the first page. The request query always carries the effective filter. `Next page` follows the existing dashboard cursor pattern.

An always-available `Latest / Refresh history` action preserves the effective filter, resets the cursor, and fetches the first page, including when already on that page. It is independent of Entity detail's Refresh action; no automatic polling or Command-completion refresh is required. Keep this action visible on empty continuation pages and errors. Show loading and request errors, and prevent obsolete requests from replacing results after a filter, Entity, or server change. Explain that newer observations require refreshing and older entries may expire while paging.

### Chart

A dependency-free responsive SVG chart uses accepted rows from the current page in ascending receive order by reversing response order; never sort by timestamps. The horizontal axis is explicitly labeled `Observation sequence (not elapsed time)`. Accepted rows occupy evenly spaced ordinal positions, including positions reserved for malformed accepted rows. `observed_at` supplies timestamp annotations only: horizontal distance does not represent duration, and equal or decreasing timestamps do not alter positions.

`State updates`, `All`, and `Applied` render steps between valid accepted values within the page, holding the preceding value until the next plotted position. Do not extend paths beyond the page's first or last accepted observation. `Unchanged` renders isolated points, never connecting lines: filtered-out applied observations may contain intervening State changes. Its caption states `Unchanged observations only; intervening State changes are not shown.` Rejected rows have no State value and do not break a path; malformed accepted rows represent unknown values and must break it. A valid run of one point remains visible.

Per Entity type:

- `hearth.power/v1`: 0/1 steps with Off/On labels.
- `hearth.brightness/v1`: integer stepped line using current support bounds when available.
- `hearth.colortemp/v1`: integer-mired stepped line using current support bounds when available.
- `hearth.temperature/v1`: milli-Celsius values displayed as °C.
- unknown future types: table remains available and the chart displays `Chart unavailable for this Entity type.`

For every numeric chart, the vertical domain is the union of current support bounds, when available, and every plotted accepted historical value. Apply constant-value padding only after that union is computed. A support range narrowed after an Observation was recorded therefore cannot clip or misposition that retained historical value.

Required edge behavior:

- no matching rows: show the existing empty-table message and `No State updates to chart.`;
- rejected-only page: show the table and `Rejected observations have no canonical State value to chart.`;
- one accepted row: render a centered point with its formatted value rather than a zero-width path;
- constant accepted values: expand the vertical domain by a type-appropriate padding so the line remains visible;
- equal or decreasing timestamps: preserve evenly spaced receive-order positions and original timestamp annotations;
- malformed API value despite the server contract: omit its point but preserve its ordinal position, break the path on both sides, keep the table's raw JSON, and state the skipped count in the caption;
- all accepted values malformed: render no path and show `No valid State values to chart.` with the skipped count;
- `Unchanged` with intervening applied changes: show isolated points without implying State continuity.

The caption reports the page-local minimum and maximum `observed_at`, number of plotted values, and number of rejected or malformed rows skipped. Timestamp bounds describe observation metadata, not elapsed duration along the axis. It does not imply that one page is the Entity's complete retained history or that spacing measures how long a State persisted.

### Table

Columns are Value, Disposition, Observation ID, Observed at, Adapter received at, and Source updated at. Values use Entity-type-aware formatting and preserve raw JSON in a title tooltip. Rejected rows display `—` for Value and a rejection-code chip or text. Observation IDs use the existing mono/truncation pattern.

No npm dependency is added.

## Project Layout

```text
docs/
└── architecture.md                                      # modify — accepted State-history retention and HTTP behavior
internal/
├── app/
│   └── hearthd/
│       └── http_handler_test.go                         # modify — runtime OpenAPI and existing-operation regression coverage
├── modules/
│   └── devices/
│       ├── api/
│       │   ├── entity_state_history.go                  # new — Huma input/output, mapping, handler
│       │   ├── entity_state_history_test.go             # new — route, validation, mapping, and errors
│       │   ├── pagination.go                            # modify — Entity State history cursor codec
│       │   ├── pagination_test.go                       # modify — cursor scope/filter/malformed cases
│       │   └── register.go                              # modify — consumer interface and operation registration
│       ├── dbqueries/
│       │   └── observations.sql                             # modify — enriched insert and history list queries
│       ├── dbsqlc/                                      # regenerate — generated observation/history query code
│       ├── entity_state_history.go                      # new — domain types and read use case
│       ├── entity_state_history_test.go                 # new — validation, parent lookup, copies, read-only behavior
│       ├── repository.go                                # modify — ReadRepository method
│       ├── read_test.go                                 # modify — existing ReadRepository fake remains complete
│       ├── sqlite_entity_state_history.go               # new — history query selection and domain mapping
│       ├── sqlite_entity_state_history_test.go           # new — filters, ordering, pagination, retention, mapping
│       ├── sqlite_observations.go                       # modify — enrich observation insert in projection transaction
│       ├── sqlite_observations_test.go                  # modify — dispositions, duplicates, unknown IDs, atomicity
│       └── sqlite_reads_test.go                         # modify — accepted observation fixture includes normalized value
└── platform/
    └── db/
        ├── db_test.go                                   # modify — expiry fixtures include normalized accepted values
        └── migrations/
            └── 00001_initial.sql                        # modify — enriched observation columns and history index
specs/
└── entity-state-history.md                              # modify — implementation contract
web/
└── src/
    ├── api/
    │   └── types.ts                                     # modify — EntityStateHistoryEntry transport type
    ├── components/
    │   └── entity-state-history.tsx                     # new — filters, chart, table, pagination
    └── pages/
        └── EntityDetailPage.tsx                         # modify — compose State history section
```

D1 owns the migration, observation query, projection, and projection tests. D2 owns the domain use case, repository interface/adapter, and their tests. D3 owns the API file, cursor, registration, and server OpenAPI test. D4 owns the dashboard files and browser evidence. D5 owns architecture documentation and final validation.

## Test Strategy

Each test protects an explicit behavior and should fail under a named plausible defect.

| Layer | Behavior protected | Plausible defect detected | Method and oracle |
|---|---|---|---|
| Projection integration | Every first-seen disposition stores one correctly enriched observation in the existing transaction | rejected/unchanged omitted, raw invalid value stored, partial commit | Migrated SQLite plus domain projection rules |
| Projection integration | Unknown-Entity and stale-runtime rejections still commit observations | accidental Entity FK or parent lookup makes idempotency fail | Existing rejection contract and observation schema invariant |
| Duplicate regression | Redelivery adds no observation/history row | duplicate creates a second point | Observation ID idempotency contract |
| Repository integration | Filters, strict keyset ordering, `limit + 1`, final-page behavior | wrong predicate, skipped/duplicated rows, off-by-one | Seeded receive orders and expected disposition sets |
| Repository integration | Every filter uses a selective ordered index on first and continuation pages | sparse/no-match filter scans unrelated observations or sorts full history | `EXPLAIN QUERY PLAN` on small skewed fixtures with sparse and absent matches |
| Repository integration | Pagination remains valid across inserts and pruning | new rows appear below an old cursor, missing cursor row fails, or empty continuation errors | Insert newer observations and prune unseen observations between requests; assert strict order and valid empty final page |
| Repository integration | Retention removes non-current rows older than the configured window and preserves current State's observation | prune deletes the current anchor, keys on Adapter/source time, or leaks aged history | Configured-window retention rule with real SQLite foreign/reference behavior |
| Repository integration | JSON and timestamps map to owned domain values | sqlc leakage, aliasing, local/noncanonical times | Domain type contract and mutation-after-return checks |
| Service unit | Validation precedes parent lookup/read; unknown parent is distinct from empty history | repository called on invalid input or 404 collapsed into empty page | Domain validation and `ErrEntityNotFound` contract |
| Cursor unit | Canonical round trip and version/resource/parent/filter/order scoping | cursor reused across filters or Entities | Cursor interface defined above |
| API unit | Metadata, defaults, 400/422/404/500 mapping, empty arrays, omitted rejected values | transport changes public contract | Huma operation and HTTP contract above |
| Application integration | New operation appears while existing operations remain unchanged | registration accidentally moves or overwrites routes | Runtime OpenAPI document |
| Browser verification | Filter, pagination reset, per-type chart/table formatting, and edge states are operable | compiled UI renders incorrectly or stale cursor survives | Agent-browser workflow below |
| Generation/schema | sqlc output matches migration/query sources and a fresh DB migrates | generated drift or invalid initial schema | Repository generation and migration tasks |

### Agent-browser verification

After implementation:

1. Run the normal local NATS, `hearthd`, and dashboard development processes against a disposable database populated through normal registration and Observation projection paths.
2. Through those normal paths, prepare the four built-in Entity types plus no-history, one-point, constant-value, rejected-only, and multi-page histories. Include Off (unchanged), On (applied), Off (applied), Off (unchanged) in receive order and verify that `Unchanged` shows isolated Off points rather than a continuous Off path.
3. Load the installed browser workflow with `agent-browser skills get core --full` before issuing browser commands.
4. Open each Entity detail page through the Vite origin, capture an accessibility snapshot, exercise every filter and pagination transition, and verify the visible chart caption and table values.
5. Change the configured base URL or Entity route and verify the cursor resets. From both the first and an older page, project a new matching observation, use `Latest / Refresh history`, and verify it appears without changing the filter or reloading the browser. Verify loading/error feedback and that obsolete requests cannot replace the current scope's results.
6. In separate dev-only browser sessions, use `agent-browser network route` before the first real navigation to mock the exact Entity-detail and State-history responses for otherwise unreachable defensive cases: an unsupported Entity type, a schema-wrong but valid JSON value between two valid values, all accepted values malformed, equal and decreasing timestamps, and an empty continuation page after pruning. Verify broken paths around malformed values, the explicit non-time axis label, receive-order positions despite timestamp regressions, and the refresh action on the empty page. Remove each route after its scenario. Network mocking is permitted only against the local development origin and adds no production fixture endpoint.
7. Capture screenshots for the four supported chart types plus empty, rejected-only, unsupported-type, and malformed-value states.
8. Check the browser console after each scenario and require no uncaught errors.

Normal-path scenarios establish integration behavior. Mocked responses establish only defensive dashboard rendering; repository and API tests remain the oracle for server contracts. Agent-browser is not a substitute for repository, service, cursor, or API tests.

## Acceptance Criteria

- [x] Every non-duplicate Observation commits exactly one observation in the existing projection transaction; `applied` and `unchanged` store only normalized State JSON, while `rejected` stores no value.
- [x] Duplicate delivery writes no new observation or history entry.
- [x] Unknown-Entity and stale-runtime rejections remain durable and do not fail an Entity foreign key.
- [x] Every successful raw SQL fixture for an accepted observation supplies normalized `state_value_json`; intentionally invalid fixture inserts supply otherwise-valid columns to retain their original failure purpose.
- [x] History uses the existing observation prune path: non-current entries older than the configured retention window disappear and the observation backing current State survives until superseded.
- [x] `GET /v1/entities/{entity_id}/state/history` matches the specified method, path, metadata, DTO, ordering, filters, cursor scope, and errors.
- [x] `state-updates` consistently means `applied` plus `unchanged` in service, SQL, cursor, HTTP, and dashboard vocabulary.
- [x] Every filter uses an indexed ordered search on first and continuation pages without scanning unrelated dispositions or sorting full Entity history; query plans are checked on small skewed fixtures, including sparse and absent matches.
- [x] With no intervening writes or pruning and a stable filter, cursor traversal returns every matching observation exactly once in descending receive order.
- [x] Concurrent inserts require restarting at the first page; pruning unseen rows or the cursor's observation does not invalidate the cursor, and an exhausted continuation returns an empty final page.
- [x] An unknown Entity returns 404; an existing Entity without matching retained history returns HTTP 200 with `items: []`.
- [x] Responses omit raw rejected values, Adapter/runtime IDs, expiry, and receive order.
- [x] The dashboard renders the specified four Entity types and all empty, rejected-only, single-point, constant-value, equal-time, malformed-value, and unsupported-type fallbacks without a new npm dependency.
- [x] The chart labels its ordinal axis as non-time, preserves receive order for equal/decreasing timestamps, uses isolated points for `Unchanged`, and breaks paths around malformed accepted values, including an all-malformed fallback.
- [x] `Latest / Refresh history` preserves the filter and fetches new observations from first, older, empty, and error pages without a browser reload; obsolete requests cannot overwrite a new scope.
- [x] Agent-browser evidence covers filters, pagination resets, refresh, chart/table formatting, screenshots, and a clean browser console.
- [x] `docs/architecture.md` records the accepted persistence, retention, and endpoint behavior.
- [x] `mise run web-build` passes.
- [x] `mise run validate` passes and the resulting generated/formatting diff is reviewed.

## Success Metrics

- All first-seen Observations after the feature lands are represented exactly once for their observation lifetime.
- A stable filtered history can be traversed without gaps or duplicates.
- An operator can diagnose recent State updates for every built-in Entity type from `EntityDetailPage` without inspecting JetStream.
- The feature adds no projection write round trip, NATS resource, background loop, runtime configuration, or frontend dependency.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|---|---:|---:|---|
| Enriching observations blurs idempotency and read-model responsibilities | Medium | Medium | Use explicit `state_value_json` naming, keep the domain read behind `ReadRepository`, and document observation-history ownership in architecture. |
| An Entity foreign key breaks unknown-Entity rejection durability | Medium | High | Keep `observations.entity_id` unconstrained and test unknown/stale rejection commits against real SQLite. |
| Directly changing migration 00001 leaves an existing development DB stale | High for active developers | Low | Document that local DBs must be recreated; revisit with an additive migration if deployment status changes. |
| Filter or cursor drift causes missing or repeated rows | Medium | Medium | Bind the effective filter into the cursor and test stable traversal for every filter. |
| Filtered reads monopolize the shared SQLite connection | Medium | High | Use three indexed query families and verify sparse/no-match plans. Scale and concurrent latency testing are explicitly out of scope. |
| Charts imply missing transitions or elapsed durations | Medium | Medium | Label the ordinal axis, render `Unchanged` as isolated points, break paths at malformed values, and verify decreasing timestamps. |
| Chart domains collapse for sparse or constant values | High | Medium | Specify explicit empty, one-point, constant, equal-time, and unsupported-type rendering and verify with agent-browser. |
| Raw rejected payloads could expose unsafe or malformed data | Low | High | Persist and expose no rejected value; retain only disposition, rejection code, and safe timestamps. |
| `ReadRepository` expansion silently invalidates broad test fakes | High | Low | List and update existing fakes explicitly; compile the full repository under `mise run validate`. |

## Trade-offs Made

| Chose | Over | Because |
|---|---|---|
| Enrich `observations` | A duplicate `entity_state_history` table | Observations already have the required cardinality, ordering, metadata, transaction, and retention; enrichment avoids a second insert and contradictory foreign keys. |
| Directly modify migration 00001 | Add migration 00002 and backfill an anchor | Hearth has no deployments, past values cannot be reconstructed, and project policy prefers direct changes without compatibility work. |
| `state-updates` default | Misnamed `state-changes` or `all` | Both applied and unchanged Observations advance canonical State evidence, while rejected diagnostics remain opt-in. |
| Current-page observation-sequence chart | Elapsed-time or unbounded chart | It preserves canonical receive order and bounded page size without time filters or a chart-specific query; it deliberately cannot show State durations. |
| Three indexed static query families | One generic filter-switch query | Extra index maintenance and query definitions prevent sparse filters from scanning unrelated history on the shared SQLite connection. |
| Non-snapshot keyset pagination | Snapshot tokens or retention pins | Concurrent inserts and pruning have explicit semantics without long-lived transactions or additional retention machinery. |
| Agent-browser acceptance | A new frontend test framework | It verifies rendered behavior and interaction without adding a dependency; pure backend contracts remain covered by Go tests. |
| Observation retention | Indefinite State-history retention | It reuses an established bounded lifecycle with one retention setting and no new prune path, at the cost of not being a long-term analytics store. |

## Open Questions

No unresolved product or architecture questions remain. Persistence location, default filter semantics, and dashboard verification were confirmed during refinement.

---
Phase: IMPLEMENTED | Delivery: PR #53

## Implementation evidence

- Projection integration tests cover normalized (not raw) JSON, every disposition, duplicate no-op behavior, unknown Entity rejections, and forced downstream State-write failure rolling back observation, State, and Command effects. Retention tests cover the current anchor, supersession, pruning during pagination, the exclusive `observed_at` boundary, Adapter/source-time independence, changed-policy pruning of persisted rows, and startup pruning expired rows while retaining the current-State anchor.
- Query-plan tests load the actual six named queries from `dbqueries/observations.sql`. Small unchanged-heavy and rejected-heavy histories retain sparse/absent matching and pagination assertions. First and continuation pages require the matching indexed search and no temporary ordering B-tree.
- Implementation review explicitly removed the 100,000-row scale fixtures and concurrent latency test because their ongoing validation cost was not justified. Correctness, query-plan, and race-enabled repository validation remain; scale performance is not an acceptance gate.
- Service, cursor, HTTP, and runtime OpenAPI tests cover validation, scoped canonical cursors, mutable ownership, filter traversal, omission rules, and empty/404/error distinctions.
- Agent-browser verified normal SDK registration/projection through local NATS, hearthd, and Vite for all four types, all filters, pagination, latest refresh from first/older pages, route/server resets, and empty/constant/single-point/rejected-only cases. Local-only defensive mocks covered unsupported types, malformed values and static bounds, support narrowing, regressing timestamps, loading/errors, and delayed obsolete requests after filter/Entity changes. Screenshots and console evidence are retained locally in `.data/evidence/` (not production fixtures). Empty-continuation UI used a low cursor with a real empty response; actual pruning remains covered by SQLite integration tests. Nanosecond caption ordering has an additional direct reversed-input check.
- Review corrections: accepted invalid-ID fixtures now supply valid State JSON to preserve their original constraint oracle; dashboard scope remounts prevent previous-scope data from appearing under new controls. No dependency, compatibility migration, or separate assumptions file was added.
