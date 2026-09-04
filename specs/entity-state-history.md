# Entity State History — Implementation Spec

**Status:** Draft, awaiting approval
**Type:** Feature (backend persistence + HTTP read + dashboard view)
**Effort:** L (approximately 1–2 focused days, 75% confidence)
**Date:** 2026-09-04

## Problem

SQLite stores no state history today. `entity_states` holds one row per Entity with the
current canonical State. `observation_receipts` holds per-Observation metadata
(disposition, rejection code, timestamps) but no value, and rows expire after
`ObservationReceiptRetention` (192h) except the receipt backing current State. The
7-day JetStream observation stream holds raw values but has no HTTP read path.

`EntityDetailPage` already shows command and availability history. State history needs
a third section, and no endpoint exists to feed it. A prior spec deliberately excluded
historical values (`resource-discovery-and-command-history.md`), so this spec
revisits that scope for state values only.

## Decision

Persist one history row per projected, non-duplicate Observation in the same SQLite
transaction that already inserts the receipt, and expose it as:

```text
GET /v1/entities/{entity_id}/state/history
```

The dashboard adds a "State history" section to `EntityDetailPage` with a small
dependency-free SVG chart plus a filterable, paginated table.

### What gets recorded

`persistObservationState` writes the row before its existing early return and state
upsert, so rejected Observations are recorded too:

| disposition | `value_json` | `rejection_code` |
|---|---|---|
| `applied`, `unchanged` | normalized canonical value | NULL |
| `rejected` | NULL (never became State) | set |

Duplicates write neither receipt nor history row. The first projection recorded them.

### Retention follows the observation stream

History rows share the receipt lifecycle, not the indefinite health-transition
retention:

- `entity_state_history.receive_order` is the primary key and references
  `observation_receipts(receive_order) ON DELETE CASCADE`.
- The existing `DeleteExpiredObservationReceipts` prune path in
  `internal/app/hearthd/run.go` deletes history through the cascade. The row backing
  current State survives because its receipt is preserved by the existing
  `NOT EXISTS (entity_states ...)` guard. FK enforcement is already on
  (`_pragma=foreign_keys(1)` in `internal/platform/db/db.go`), so no new prune
  loop, config, or background path is needed.
- `expires_at` copies `ProjectObservationParams.ReceiptExpiresAt`
  (`observed_at` plus 192h), which covers the 7-day JetStream redelivery window.

### Migration

New immutable Goose migration `00002_state_history.sql`
(`internal/platform/db/migrations/` holds only `00001_initial.sql` today):

```sql
-- +goose Up
CREATE TABLE entity_state_history (
    receive_order       INTEGER PRIMARY KEY REFERENCES observation_receipts(receive_order) ON DELETE CASCADE,
    entity_id           TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    observation_id      TEXT NOT NULL UNIQUE CHECK (substr(observation_id, 1, 4) = 'obs_'),
    value_json          TEXT CHECK (value_json IS NULL OR json_valid(value_json)),
    disposition         TEXT NOT NULL CHECK (disposition IN ('applied', 'unchanged', 'rejected')),
    rejection_code      TEXT CHECK (
        rejection_code IS NULL OR rejection_code IN (
            'unknown_entity', 'wrong_adapter', 'entity_disabled',
            'invalid_value', 'stale_runtime'
        )
    ),
    adapter_received_at TEXT NOT NULL,
    source_updated_at   TEXT,
    observed_at         TEXT NOT NULL,
    expires_at          TEXT NOT NULL,
    CHECK (
        (disposition = 'rejected' AND rejection_code IS NOT NULL AND value_json IS NULL)
        OR (disposition <> 'rejected' AND rejection_code IS NULL AND value_json IS NOT NULL)
    )
);

CREATE INDEX entity_state_history_entity_idx
    ON entity_state_history(entity_id, receive_order DESC);

-- Backfill one anchor row per Entity from current State so charts never start empty.
INSERT INTO entity_state_history (
    receive_order, entity_id, observation_id, value_json, disposition,
    rejection_code, adapter_received_at, source_updated_at, observed_at, expires_at
)
SELECT r.receive_order, s.entity_id, s.observation_id, s.value_json, 'applied',
    NULL, s.adapter_received_at, s.source_updated_at, s.observed_at,
    datetime(s.observed_at, '+192 hours')
FROM entity_states AS s
JOIN observation_receipts AS r ON r.observation_id = s.observation_id;

-- +goose Down
DROP INDEX entity_state_history_entity_idx;
DROP TABLE entity_state_history;
```

Past values were never persisted and cannot be reconstructed, so history starts at
this deployment plus the backfilled anchor row.

## Public HTTP contract

`GET /v1/entities/{entity_id}/state/history`

- **Operation ID:** `list-entity-state-history`, **tag:** `Entities`,
  **summary:** `List an Entity's State history`.
- **Errors:** HTTP 400 for invalid IDs, cursors, or disposition filters; HTTP 422 for
  structural validation; HTTP 404 for an unknown Entity (`entity not found`);
  HTTP 500 for internal failures. Existing routes are unchanged.

Query parameters:

| Query | Required | Contract |
|---|---|---|
| `limit` | no | Integer; default 50; minimum 1; maximum 200. |
| `cursor` | no | Opaque endpoint-specific continuation token from the preceding page. |
| `disposition` | no | `state-changes` (default: `applied` + `unchanged`), `all`, `applied`, `unchanged`, or `rejected`. |

Response items use:

```go
type StateHistoryBody struct {
    ObservationID     string  `json:"observation_id"`
    Value             any      `json:"value,omitempty"` // normalized State value; nil for rejected rows
    Disposition       string  `json:"disposition"`
    RejectionCode     *string `json:"rejection_code,omitempty"`
    AdapterReceivedAt string  `json:"adapter_received_at"`
    SourceUpdatedAt   *string `json:"source_updated_at,omitempty"`
    ObservedAt        string  `json:"observed_at"`
}

type StateHistoryCollectionBody struct {
    Items      []StateHistoryBody `json:"items"`
    NextCursor *string            `json:"next_cursor,omitempty"`
}
```

Timestamps are UTC RFC3339Nano. Items order by `receive_order DESC`. Later pages
select `receive_order < cursor.receive_order` under the same Entity and filter. The
repository fetches `limit + 1` rows and transport derives `next_cursor` from the final
item when `HasMore` is true, the same convention as command and health history. An
existing Entity without history returns HTTP 200 with `items: []`.

Cursors are versioned base64url JSON owned by `internal/modules/devices/api` through a
new `stateCursor{Version, Resource: "entity_state", ParentID, ReceiveOrder,
Disposition}`. Each cursor works only for its endpoint, parent Entity, and filter.
Filter or parent mismatch, bad encoding, version, fields, or `receive_order < 1`
returns HTTP 400.

## Domain contract

Owner: `internal/modules/devices/model.go` (new types only; existing types untouched).

```go
type StateHistoryEntry struct {
    ObservationID     ObservationID
    Value             Value // nil for rejected rows
    Disposition       ObservationDisposition
    Rejection         *ObservationRejection
    AdapterReceivedAt time.Time
    SourceUpdatedAt   *time.Time
    ObservedAt        time.Time
    ReceiveOrder      int64
}

type StateDispositionFilter string

const (
    StateFilterChanges   StateDispositionFilter = "state-changes"
    StateFilterAll       StateDispositionFilter = "all"
    StateFilterApplied   StateDispositionFilter = "applied"
    StateFilterUnchanged StateDispositionFilter = "unchanged"
    StateFilterRejected  StateDispositionFilter = "rejected"
)

type ListStateHistoryParams struct {
    EntityID           EntityID
    Disposition        StateDispositionFilter
    BeforeReceiveOrder *int64
    Limit              int
}
```

### Service use case

New file `internal/modules/devices/state_history.go`:

```go
func (service *Service) ListStateHistory(context.Context, ListStateHistoryParams) (Page[StateHistoryEntry], error)
```

The service rejects bad Entity IDs, limits outside 1–200, unknown filters (empty
means `state-changes`), and `BeforeReceiveOrder` below 1. It calls
`stores.Reads.GetEntity` first so an unknown Entity returns `ErrEntityNotFound`, the
same convention as `ListEntityCommands`. It returns owned copies and performs no
writes or NATS publication.

### Repository seam

Writing stays inside the existing `ObservationRepository.ProjectObservation`
transaction (one extra insert; no interface change). Add the read to `ReadRepository`:

```go
ListStateHistory(context.Context, ListStateHistoryParams) (Page[StateHistoryEntry], error)
```

The SQLite implementation owns `limit + 1` querying, the disposition predicate,
`HasMore`, truncation, context propagation, and error mapping; sqlc types stay inside
the adapter. New sqlc queries in `internal/modules/devices/dbqueries/state.sql`:

- `ListStateHistoryFirstPage` / `ListStateHistoryAfter`: entity-constrained,
  `receive_order DESC`, with a `disposition IN (...)` predicate bound to the filter.
- `InsertStateHistory`: single-row insert of the classified projection.

## HTTP consumer

Extend the `devicesapi` consumer interface in `internal/modules/devices/api/register.go`
with `ListStateHistory`, register `GET /entities/{entity_id}/state/history` on the
`/v1` group, and map domain entries with a `stateHistoryBody` mapper.

## Dashboard

New "State history" section on `EntityDetailPage`, below "Availability history":

- **Filter.** A segmented control (`State changes` default, `All`, `Applied`,
  `Unchanged`, `Rejected`) maps to the `disposition` query param. Changing it resets
  pagination.
- **Chart.** A dependency-free SVG step chart covers the current page's `observed_at`
  range, oldest to newest. Power renders as 0/1 steps; brightness, color-temp, and
  temperature render as stepped lines with min/max axis labels. Temperature converts
  milli-Celsius to °C for display. Rejected rows carry no value, so the chart skips
  them and says so in its caption.
- **Table.** Columns are Value (per-type formatted, raw JSON in the title tooltip),
  Disposition chip, Observation ID (mono, truncated), Observed at, and Adapter
  received at. It reuses the `Collection<T>`, cursor pagination, `useBaseUrlVersion`
  reset, and `EmptyRow` patterns from `CommandHistory` and `AvailabilityHistory`.
- New `StateHistoryEntry` type in `web/src/api/types.ts`; no new npm dependencies.

## Project layout

```text
specs/entity-state-history.md                        # this spec
docs/architecture.md                                  # modify: state-history retention + endpoint
internal/platform/db/migrations/00002_state_history.sql  # new
internal/modules/devices/dbqueries/state.sql         # modify: history queries
internal/modules/devices/dbsqlc/                      # regenerate
internal/modules/devices/model.go                    # modify: history domain types
internal/modules/devices/repository.go               # modify: ReadRepository seam
internal/modules/devices/sqlite_observations.go      # modify: insert history row in projection tx
internal/modules/devices/sqlite_reads.go             # modify: row mapping + list
internal/modules/devices/state_history.go            # new: service use case
internal/modules/devices/api/state_history.go        # new: endpoint
internal/modules/devices/api/pagination.go           # modify: state cursor codec
internal/modules/devices/api/register.go             # modify: route + consumer interface
internal/modules/devices/api/types.go                # modify: history DTOs
web/src/api/types.ts                                 # modify: StateHistoryEntry
web/src/pages/EntityDetailPage.tsx                   # modify: State history section + chart
```

## Verification strategy

| Layer | Required coverage |
|---|---|
| Cursor unit | Round trips per filter; version/scope/parent/filter mismatch; malformed input. |
| Service unit | ID/limit/filter validation; unknown Entity; owned copies; read-only. |
| Repository integration | Ordering; `limit + 1`; each filter predicate; rejected rows carry NULL value; backfill anchor; receipt-prune cascade deletes history but preserves current-State row; indexes. |
| Projection | Applied/unchanged/rejected each write exactly one history row in-tx; duplicates write none; normalized value stored. |
| API unit | Route metadata; query defaults/bounds; filter validation; cursor round trip; DTO mapping (value omitted for rejected); 404 unknown Entity; empty array. |
| Application integration | New operation present in runtime OpenAPI; existing operations unchanged. |
| Dashboard | `tsc --noEmit` + `vite build`; per-type value formatting; filter resets cursor; chart excludes rejected rows. |
| Generation/migration | sqlc reproducibility; empty + upgraded DB migration; down migration restores schema. |

## Acceptance criteria

- [ ] Each non-duplicate projection writes exactly one history row in the same transaction
      as its receipt; duplicates write none.
- [ ] `GET /v1/entities/{entity_id}/state/history` matches the specified method, path,
      operation metadata, DTO, ordering, filter, cursor, and error contracts, including
      404 for an unknown Entity and `items: []` for an Entity without history.
- [ ] Receipt expiry cascades history deletion; the current-State row survives.
- [ ] Migration applies to empty and current databases, backfills one anchor row per
      Entity, and reverses cleanly; sqlc output is regenerated from source.
- [ ] Dashboard shows a chart plus filterable, paginated history table on the entity page
      with no new npm dependencies.
- [ ] `docs/architecture.md` records the accepted behavior.
- [ ] Gate passes: `mise run validate`.

## Scope boundaries

- No history mutation, backfill beyond the current-State anchor, or replay: history is
  an append-only audit of projections.
- No additional filters (time ranges), sorts, totals, or cross-entity history queries.
- No retention configuration: history strictly follows receipt retention.
- No new NATS subjects, JetStream consumers, or SDK changes; adapters are untouched.
- Authentication and network exposure are unchanged (trusted loopback default).
