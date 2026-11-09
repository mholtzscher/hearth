# State changes

Core records each change of a catalogued yes/no or text State field.
`POST /v1/entities/{entity_id}/state/changes` returns those changes as a
held-value timeline or, for a yes/no field, as bucketed time-true totals. It is
read-only. Numeric fields use [State history summaries](history-queries.md).

## What Core records

The `state_changes` table holds one row per change: Entity, field path,
`observed_at`, `receive_order` and the field's compact JSON value. An **applied**
Observation records each boolean or string field of its Entity type whose value
differs from the Entity's previous State, or every such field when there was no
previous State. Unchanged, duplicate and rejected reports record nothing. An
absent optional field is recorded as JSON `null`, a known value rather than
unknown evidence. Rows commit in the Observation's projection transaction.

Changes are detected in receive order, the order that sets canonical State
([ADR 0010](adr/0010-order-state-by-core-receive-order.md)). Queries order them by
`(observed_at, receive_order)`, the time axis numeric rollups use. Core assigns
`observed_at` on receipt, so the two orders agree except across clock
adjustments, which are not corrected.

Migration 31 creates the table and backfills it by replaying retained accepted
(applied and unchanged) Observations per Entity in receive order. The first
retained accepted report of an Entity records every field, because its earlier
State is unknown; later reports record only changes, as live ingest does. Seeding
from an unchanged report covers an Entity whose applied report retention already
removed. The
migration freezes its field list; a Devices test fails when the catalog gains a
yes/no or text field that no backfill covers.

## Retention

The history prune pass deletes changes observed before the Observation cutoff,
except each field's newest change at or before the cutoff. That seed row keeps
the value held at the start of any retained range known. It adds one row per
Entity field. Nothing reconstructs changes older than retained Observations.

## Requests

```json
{
  "range": {"from": "2026-11-07T14:40:00Z", "to": "2026-11-08T14:40:00Z"},
  "select_field": "",
  "result": {"kind": "timeline"}
}
```

`select_field` is required: `""` selects the root and a path such as `"/mode"`
selects a field in the Entity type's generated field catalog. Only boolean and
string fields are accepted. Ranges are half-open `[from, to)` UTC instants with
`from < to` and `to` no later than query evaluation; offsets are normalized.

A summary sets `"result": {"kind": "summary", "bucket": {"duration": "1h",
"alignment": "range_start"}}`. It requires a yes/no field and a bucket that is a
whole number of minutes. A timeline takes no bucket.

## Responses

Every response carries `kind`, `entity_id`, `select_field`, `range`,
`time_axis: "observed_at"` and `coverage: {"basis": "retained_changes",
"completeness": "unverified", "seeded": bool}`. `seeded` is true when a change
before `from` set the opening value.

A `timeline` has contiguous `intervals` covering the range. Each is
`{"kind": "value", "from", "to", "value"}` or, before the first known value when
nothing is seeded, `{"kind": "unknown", "from", "to"}`. Equal neighbors are
coalesced, and a change at an interval's start instant replaces it.

A `summary` has `bucket` (normalized, for example `1h0m0s`) and one entry per
bucket, the last clipped at `to`:

| Field | Meaning |
| --- | --- |
| `seconds_true` | Time the field held `true` inside the bucket |
| `transitions_into_true` | Changes into `true` from a known `false` or `null` inside the bucket; a change from unknown is not counted |
| `known_seconds` | Time with a known value inside the bucket |

## Limits and errors

A query reads at most 100,000 changes inside its range, allows at most 1,000
buckets, and returns at most 4 MiB. Requests follow the numeric history
operation: `Content-Type: application/json`, at most 1 MiB, and a five-second
body-read deadline. Problems use Huma's format and a machine-readable `code`.

| Status | Code | Condition |
| --- | --- | --- |
| 400 | `invalid_query` | Malformed JSON or shape, a summary without a bucket, a timeline with one, bad duration text or Entity ID |
| 404 | `entity_not_found` | Unknown Entity |
| 415 | `invalid_query` | Unsupported media type |
| 422 | `invalid_query` | Invalid range or bucket, or a field that records no changes |
| 422 | `unsupported_source` | Stateless Entity |
| 422 | `value_type` | A summary of a text field |
| 422 | `query_limit` | Too many changes or buckets, or oversized output |
| 503 | `query_canceled` | Caller-owned cancellation |
| 500 | `internal_error` | Unexpected service, persistence, or encoding failure |

## Ingest cost

The `devices.observation.append` dbload case alternates `true` and `false`, so
every attempt records one change. Five alternating standalone `dbload run`
blocks per side (200 attempts, concurrency 1, each binary seeding its own
`history-1000` fixture) compared baseline `8a6b9c4` with candidate `a5ee85a`.
All 2,000 attempts completed without errors. Median block p50 was 3.20 ms
(3.18–3.20) before and 3.23 ms (3.21–3.43) after; p95 medians were 5.67 ms and
4.64 ms with overlapping, noisy ranges. These local warm-cache results show no
cost visible at this resolution; they are not a deployment forecast. The
candidate's migration backfilled 479,880 changes from 1,440,004 Observations.
The [measurement record](media/state-changes/measurements.json) lists every block.
