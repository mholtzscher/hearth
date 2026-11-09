# Numeric State history summaries

`POST /v1/entities/{entity_id}/state/history` summarizes one catalogued numeric
State field from retained accepted reports. It reads one-minute rollup cells,
updated atomically when Core records each applied or unchanged Observation.
The operation is read-only. The existing GET on the same path remains the
receive-ordered audit view, including rejected reports. Yes/no and text fields
use [State changes](state-changes.md).

```json
{
  "range": {"from": "2026-11-01T00:00:00Z", "to": "2026-11-02T00:00:00Z"},
  "select_field": "",
  "result": {
    "kind": "summary",
    "metrics": {
      "low": {"operation": "min"},
      "high": {"operation": "max"},
      "mean": {"operation": "sample_mean"}
    },
    "bucket": {"duration": "5m", "alignment": "range_start"}
  }
}
```

## Selection and range

`select_field` is required: `""` selects a numeric root, and a path such as
`"/value"` selects a numeric field in the Entity type's generated field catalog.
Boolean, string, unknown, and stateless sources are not numeric sources.
Values stay in their canonical units. Household unit conversion belongs to the
dashboard, not to this query.

Ranges use UTC RFC3339 instants, with `from < to` and `to` no later than query
evaluation. Offsets are normalized to UTC. `from` must be on a whole-minute
boundary. The time axis is Core `observed_at`, not the upstream clock or the
time canonical State changed. Ordering within a cell does not affect its count.

**The final minute is included whole.** For an unaligned `to`, the query includes
every report in the one-minute cell containing `to`, including reports after
`to`. For example, a `to` of `10:00:20Z` includes reports before `10:01:00Z`.
An aligned `to` of `10:00:00Z` excludes the cell starting at `10:00:00Z`.
The response preserves the requested range and clips its last bucket's displayed
end at `to`; that end does not imply sub-minute sample precision.

## Metrics and buckets

`result.kind` must be `summary`. `metrics` is a nonempty object of caller-named
`min`, `max`, or `sample_mean` operations. Every accepted report counts,
including unchanged reports. `sample_mean` is an arithmetic sample mean, not a
time-weighted or circular mean. Empty buckets render every metric as JSON null.
No current State or prior report fills a bucket without samples.

Integer extrema retain integer JSON encoding. A shared Go accumulator keeps
exact rational sums for decoded integers and finite floating-point samples,
including sums beyond int64 and cancellation between large values. It rounds
only when returning the mean. Migration, ingest, and query merging use the same
accumulator; SQLite stores the sum as text and performs no numeric aggregation.

Omit `bucket` for overall `metrics`. With `bucket`, its `duration` is a positive
Go duration and a whole number of minutes, and `alignment` is `range_start`.
All buckets are emitted, including empty buckets. Durations are normalized in
the response (`5m` becomes `5m0s`); the response contains `bucket` and `buckets`
instead of overall `metrics`.

Every response includes Entity ID, range, `time_axis: "observed_at"`, and
`coverage: {"basis":"retained_observations","completeness":"unverified"}`.
Coverage describes retained evidence, not physical truth, freshness, or complete
collection.

## Null policy

`result.nulls` is `reject` (the default) or `gap`. A missing optional field and a
known JSON null both count as null samples. Null is never zero or a skipped sample.

| Reports in a bucket | `reject` | `gap` |
| --- | --- | --- |
| 40, 60 | min 40, max 60, mean 50 | Same |
| 40, null, 60 | `value_type` | Every metric is null |
| Null only | `value_type` | Every metric is null |
| No reports | Every metric is null | Same |
| A nonnumeric value | `value_type` | Still `value_type` |

For an unbucketed summary, the policy applies to the entire range. Failure
never returns partial aggregates. For an optional numeric-setting field, use
`"select_field":"/value"` and `"nulls":"gap"`.

## Retention and migration

Migration 30 backfills cells from existing retained reports. New reports update
cells in the same transaction as the Observation and pending Device Fact.
Duplicate and rejected reports never change a cell. No asynchronous projector
or catch-up process is involved.

The history prune pass uses the existing `observation_retention` window. It
deletes a cell only when the cell ends at or before the Observation cutoff.
The cell containing the cutoff stays whole, so it can include up to one minute
of samples already removed from raw history. Cells do not preserve old
current-State anchors beyond their cell's retention cutoff. Pruning does not
reconstruct missing evidence or promise a durable snapshot.

## Limits and errors

Queries allow at most 16 metrics, 1,000 buckets, and 4 MiB of encoded output.
Request bodies require `Content-Type: application/json`, allow at most 1 MiB,
and have a five-second body-read deadline. There are no expression evaluations,
raw row reads, cursors, or expression/source limits on this endpoint.

Problems use Huma's format and a machine-readable `code`:

| Status | Code | Condition |
| --- | --- | --- |
| 400 | `invalid_query` | Malformed JSON, request shape, duration text, or Entity ID |
| 404 | `entity_not_found` | Unknown Entity |
| 415 | `invalid_query` | Unsupported media type |
| 422 | `invalid_query` | Invalid domain range, bucket, metric name, or numeric field |
| 422 | `unsupported_source` | Stateless Entity |
| 422 | `value_type` | Null under `reject`, or a nonnumeric retained sample |
| 422 | `query_limit` | Too many buckets or oversized output |
| 503 | `query_canceled` | Caller-owned cancellation |
| 500 | `internal_error` | Unexpected service, persistence, or encoding failure |

Framework errors, such as `408` for slow bodies and `413` for oversized bodies,
may omit a code. Error details never echo State or request contents.

## Compatibility

The POST operation no longer accepts `select_expr`, observation pages, timelines,
`duration_where`, `transitions_into`, filters, or cursors. Those request shapes
return `400 invalid_query`. Numeric requests now require `select_field` and
minute-aligned starts; `expression_type` is now `value_type`.
The GET audit endpoint and Automation expressions are unchanged.
