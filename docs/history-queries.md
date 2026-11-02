# State history queries

`POST /v1/entities/{entity_id}/state/history` queries one Entity's retained accepted reports. It is read-only; the Entity ID belongs in the path, never the body. The existing GET on the same path remains the receive-ordered audit view, including rejected reports.

```json
{
  "range": {"from": "2026-11-01T09:00:00Z", "to": "2026-11-01T10:00:00Z"},
  "select_expr": "sample.value.contact",
  "result": {
    "kind": "summary",
    "metrics": {
      "open_seconds": {"operation": "duration_where", "where_expr": "value == true", "unit": "seconds"},
      "openings": {"operation": "transitions_into", "where_expr": "value == true"}
    }
  }
}
```

Ranges are UTC RFC3339 timestamps, half-open `[from, to)`, with `from < to` and `to` no later than query evaluation. Ordering uses Core `observed_at` and receive order as a tie-breaker, not the upstream clock or a replay of when canonical State changed.

Omit `select_expr` to select `sample.value`; an empty expression is invalid. Selection can also access `sample.observation_id`, `sample.observed_at`, `sample.adapter_received_at`, and nullable `sample.source_updated_at`. Observation filters and temporal predicates see the selected `value` and must yield a boolean. Expressions cannot perform I/O or read other history or current State. Native missing-key behavior may yield JSON null; use membership tests when absence matters. Null remains a known value, not unknown evidence.

## Result kinds

- `observations`: optional `where_expr`, `order` (`ascending` by default or `descending`), `limit` (default 50, maximum 200), and `cursor`. Items retain Observation ID, timestamps, original applied/unchanged disposition and selected JSON value. `source_updated_at` is explicitly null when absent. The final page omits `next_cursor`; a filtered empty page can still have one. Cursors bind the normalized query and initial high-water mark; pruning can remove rows between pages, so this is not a durable snapshot.
- `timeline`: no additional fields. Chronological last-value-held intervals cover the range, coalescing equal selected values. A missing prior report produces an `unknown` interval with `reason: "no_prior_observation"`; a known JSON null is a `value` interval. At tied timestamps the last receive-ordered report determines the held value.
- `summary`: requires a nonempty `metrics` object, keyed by caller-chosen names. `min`, `max`, and `sample_mean` consume numeric reports, including unchanged ones, and yield null with no reports. `duration_where` requires `where_expr` and `unit: "seconds"`; `transitions_into` requires `where_expr` and counts established false-to-true transitions. An unknown-to-true change is not an established transition. Non-numeric input to numeric reductions fails rather than being skipped.

An optional summary `bucket`, for example `{"duration":"1h","alignment":"range_start"}`, partitions the range by a fixed positive Go duration and clips the final bucket. The response normalizes the duration using Go's duration format (`1h0m0s` for one hour). All buckets are emitted. Bucketed responses contain `bucket` and `buckets`, not overall `metrics`. Temporal bucket results contain duration coverage per bucket; numeric-only buckets omit it.

Every response includes the requested range, Entity ID, `time_axis: "observed_at"`, and coverage with `basis: "retained_observations"` and `completeness: "unverified"`. Timeline and temporal summaries additionally report seed availability, inferred known seconds and unknown seconds. These describe retained evidence, not physical truth, freshness, or complete collection or retention. Duration is null when no duration is known; zero means known duration had no matches.

## Limits and errors

Queries allow at most 16 metrics, 1,000 buckets, 10,000 source rows, 16 MiB source JSON, 100,000 expression evaluations and 4 MiB encoded results. Expression source is limited to 4,096 bytes, alongside the expression engine's node, value and allocation limits. Limit failures do not return partial aggregates. The HTTP framework also imposes its normal request body size limit.

Problems use Huma's problem format and a machine-readable `code`. Malformed JSON or request shape is `400 invalid_query`; invalid path IDs are also `400 invalid_query`, and invalid cursors are `400 invalid_cursor`. Unknown Entities are `404 entity_not_found`. Invalid domain queries, unsupported sources, query limits and expression failures are `422` with `invalid_query`, `unsupported_source`, `query_limit`, `expression_type`, `expression_limit`, `expression_value_invalid`, or `expression_evaluation`. Caller-owned cancellation is `503 query_canceled`; unexpected failures are `500 internal_error`. Framework errors such as `413` for oversized request bodies may omit a code. Error details never echo expressions or State values.
