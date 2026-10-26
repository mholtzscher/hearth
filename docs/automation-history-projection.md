# Automation history summary projection measurements

These results describe the pre-revision-sharing layout at `10f85e4`. After #220,
the checked-in listing fixture stores one shared artifact for its 1,000 Runs,
so rerunning it is no longer a same-layout reproduction of these numbers. See
[revision storage measurements](automation-definition-revisions.md#storage-comparison)
for the separate storage comparison.

Issue [#215](https://github.com/mholtzscher/hearth/issues/215) limits collection
queries to the persisted summary contract. Detail reads retain their full query
and validation. No schema, index, API, or retention change is included.

## Workload and method

Measured on 2026-10-25 against baseline `10f85e4`, with the same benchmark source
on both sides. `BenchmarkHistoryListing` lives in
`internal/modules/automations/sqlite/history_listing_benchmark_integration_test.go`.

- Linux amd64, AMD Ryzen 9 3900X, 24 logical CPUs, Go 1.27.1, no race detector.
  Host load before measurement was 1.67, 0.86, 0.66. Other desktop processes
  remained running. Measurements ran serially, before full validation.
- A temporary file-backed SQLite database uses production migrations, WAL, and
  the production single-connection policy. Both sides use the same dependency
  versions, indexes, and driver defaults.
- Synthetic data consists of 1,000 succeeded manual Runs for one former
  Automation, with no current parent row. Each Run retains a valid definition
  with a 60 KiB Command parameter string and a not-configured Condition decision.
  All timestamps are equal. First pages start at ID 1,000; continuation pages
  start below ID 501.
- Each measured call executes `AutomationRepository.ListHistory`, including its
  transaction, limit+1 query, summary decoding, and domain mapping. The benchmark
  checks item count, continuation availability, first ID, and retained name on
  every call. Database creation and seeding are outside the timer.
- The generated query runs before timing for payload accounting, warming the
  measured pages. The operating-system page cache is not cleared. This is a
  sequential warm-read workload, not cold-disk or concurrent-client evidence.
- Five runs per side alternate baseline then projection. Each sub-benchmark uses
  `-benchtime=500ms`. All runs passed their result assertions.
- `db_payload_B/op` counts actual returned UTF-8 string bytes plus eight bytes
  per SQLite INTEGER, including the lookahead row. NULL contributes zero. This
  is logical database result payload, not file I/O, SQLite record encoding, or
  HTTP/MCP response bytes. It is collected outside timing from the actual
  generated SQL, not a second hand-maintained projection.

Run each side with its corresponding checkout:

```sh
GO_PACKAGES='./internal/modules/automations/sqlite' mise run test -- \
  -tags=integration -run '^$' -bench '^BenchmarkHistoryListing$' \
  -benchtime=500ms -count=1
```

## Results

The summary projection reduced allocations and repository-read latency in this
synthetic workload. It does not establish production or endpoint latency.

Medians of five runs:

| Page | Limit | Latency, baseline → projection, ms | Allocated B/op, baseline → projection | Allocs/op, baseline → projection | Database payload B/op, baseline → projection |
| --- | ---: | ---: | ---: | ---: | ---: |
| First | 50 | 1.991 → 1.211 | 3,523,761 → 160,627 | 1,594 → 1,070 | 3,164,703 → 9,231 |
| Continuation | 50 | 2.029 → 1.232 | 3,523,771 → 160,649 | 1,603 → 1,079 | 3,164,703 → 9,231 |
| First | 200 | 7.193 → 4.556 | 13,904,682 → 632,061 | 5,947 → 3,924 | 12,472,653 → 36,381 |
| Continuation | 200 | 7.379 → 4.731 | 13,904,813 → 632,233 | 5,956 → 3,933 | 12,472,653 → 36,381 |

Individual latency observations in alternating run order, in milliseconds:

| Page/limit | Baseline runs 1–5 | Projection runs 1–5 |
| --- | --- | --- |
| First/50 | 1.990698, 2.027092, 1.944790, 2.057575, 1.984554 | 1.204964, 1.210725, 1.206784, 1.218084, 1.210763 |
| Continuation/50 | 2.028971, 2.107279, 1.914448, 2.081298, 1.955703 | 1.225635, 1.241332, 1.231857, 1.229028, 1.252933 |
| First/200 | 7.488079, 7.097592, 7.193079, 7.339112, 7.017212 | 4.541521, 4.568186, 4.555561, 4.505839, 4.593325 |
| Continuation/200 | 7.701291, 7.184344, 7.530955, 7.230851, 7.379491 | 4.617514, 4.730514, 4.732283, 4.586273, 4.760895 |

Database payload bytes were identical across repetitions on each side. Allocated
bytes varied by at most 413 B/op within a case; allocation counts varied by at
most one allocation/op. The latency ranges do not overlap between implementations.

## Remaining cost and limitations

Separate two-second CPU-profile runs of the first 200-item page were excluded
from the results above. The baseline profile included memory copying, clearing,
and garbage collection. In the projection profile, the largest flat sample owner
was `internal/runtime/syscall/linux.Syscall6`. Restricting stacks to `ListHistory`
still showed SQLite `_readDbPage`, `_accessPayload`, and `_getOverflowPage` on
the read path. The projection avoids returning large documents to Go, but SQLite
still traverses record and overflow pages to reach summary columns. This explains
why the payload and allocation reduction does not produce a proportional latency
reduction. It is not a covering-index result or proof that table-page reads vanish.

To repeat the profile, add these flags to the focused benchmark command, then
inspect it with `mise exec -- go tool pprof -top history.test history.cpu`:

```sh
-bench '^BenchmarkHistoryListing/limit=200/after=false$' \
-benchtime=2s -cpuprofile=history.cpu -o history.test
```

The fixture does not represent a measured household distribution. It deliberately
stresses retained snapshots, not large Fact values, large Condition decisions,
transport serialization, or simultaneous pruning. Fact evidence remains in the
projection because HTTP summaries expose it. Correctness tests separately cover
both Fact families, Held-State and schedule provenance, Condition modes,
malformed detail documents, and existing pagination/pruning contracts. A physical
summary table and revision sharing remain outside this change.
