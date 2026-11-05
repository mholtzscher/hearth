# Database scale results

## Conversation summaries: 95.5% lower query p95

The production conversation-summary query improved from **77.73 ms to 3.46 ms**
at 100x message fan-out: 20 conversations with 32,000 retained messages. These
are repository/query timings, not HTTP, browser, or model-response latency.

| Evidence | Result |
| --- | --- |
| Baseline / candidate | `761d461` / `42f63bc` |
| Confirmation | 20 alternating AB/BA pairs, 200 attempts per side per block |
| Completed work | 8,000 attempts, zero errors or timeouts |
| Baseline median block p95; range | 77.731 ms; 77.268–78.611 ms |
| Candidate median block p95; range | 3.460 ms; 3.340–3.701 ms |
| Geometric mean paired p95 ratio | 0.04456 |
| Paired-block bootstrap 95% interval | 0.04413–0.04504 |
| Holm-adjusted randomization p-value | 0.000020; registered alpha 0.01667 |
| Decision | Keep: exceeds the registered 10% minimum improvement |

The baseline joins every message before grouping by conversation. Latest-message
and first-user lookups then repeat across those joined messages. Separate CPU
profiles show SQLite VM execution and B-tree seeks dominating that work.

The candidate counts through the existing conversation index independently. It
performs latest-message and preview lookups once per conversation. It adds no
index or migration and changes no write path, pool setting, or cache policy.
Nullable fields, exact counts, preview selection, and activity ordering remain
unchanged. Real-SQLite tests and an independent source review cover those rules.

## Growth and displaced costs

Exploration uses three short blocks, with 20 attempts per block unless a cutoff
prevents completion. These results are descriptive, not additional confirmations.

| Message fan-out | Baseline block p95, ms | Candidate block p95, ms |
| --- | --- | --- |
| 1x, 320 messages | 0.769, 0.825, 0.784 | 0.216, 0.228, 0.255 |
| 10x, 3,200 messages | 7.004, 7.009, 6.896 | Not run |
| 100x, 32,000 messages | 76.436, 76.981, 77.063 | See registered comparison above |
| 1000x, 320,000 messages | 944.537, 865.346, 870.038 | 33.507, 31.971, 31.437 |

Separate 1000x diagnostic captures used 20 baseline and 200 candidate operations
to collect useful CPU samples. Their timings are excluded from these tables and
the statistical comparison. The candidate still scans the message index to count
messages; the query is not constant-time.

Five alternating fresh blocks per side checked unrelated write and maintenance
costs on the same 100x fixture. All completed without errors. Median block p95:

| Operation | Baseline, ms | Candidate, ms |
| --- | --- | --- |
| Observation append | 3.522 | 3.519 |
| Entity Event prune batch | 6.018 | 6.075 |
| Notification prune batch | 5.632 | 5.611 |

Those checks are descriptive, not equivalence tests. Maintenance runs one genuine
batch per fresh block rather than repeatedly deleting an already-empty table.

## Coverage and limits

- All 32 application tables contain validated, linked synthetic records.
- All 183 production SQL statements are inventoried. Exploration timed 45;
  138 remain explicitly uncovered. The 45 workloads do not imply full SQL coverage.
- Independent population, history, and relationship fan-out sweeps cover
  1x/10x/100x/1000x, plus a bounded combined case. The largest datasets contain
  1,440,004 observations. Shared 1x aliases do not add statistical replications.
- The corrected exploration retained 5,756 complete blocks, 1,117 resource-limited
  blocks, six incomplete blocks, and six failed blocks. History pruning exceeded
  its 10-second safety cutoff; some contention cases reached run-budget cutoffs.
  These are not passes or evidence that the remaining bottlenecks are fixed.
- The provisional interactive threshold does not apply to this unbounded list.
  Neither the suite nor this result establishes full-household capacity.
- An initial run found a disk-monitor race with normal disposable-file deletion.
  The monitor was corrected and the baseline re-established. Initial-run timings
  are retained separately and are not merged into the reported comparison.
- Total experiment execution across both budget ledgers is under 65 minutes of
  the approved two hours. Implementation and correctness checks are excluded.

## Inspect or reproduce the evidence

The delivered offline report is `.data/dbload/study-v2/report.html`. It embeds the
recorded evidence and includes dimension-filtered growth curves, before/after
intervals, exact tables, failed/missing cells, query coverage, and provenance.
Recorded blocks are paginated; filtering searches all blocks.

Raw local evidence remains under `.data/dbload/study-v2/`:

- `results.json`: baseline scale exploration.
- `comparison-1/`: frozen registration, execution metadata, and all paired raw
  observation files.
- `profile-baseline/` and `profile-candidate/`: separate CPU/heap profiles and plans.
- `after-*/` and `cost-*/`: descriptive candidate growth and cost checks.
- `budget.json` and `report-inputs.txt`: charged execution and exact report inputs.

Regenerate the report without executing workloads:

```sh
mise run dbload -- report \
  -inputs "$(cat .data/dbload/study-v2/report-inputs.txt)" \
  -out .data/dbload/study-v2/report.html
```

See [Test database operations at scale](database-load-testing.md) for new runs,
fixture construction, safety limits, and the confirmation protocol. Local raw
artifacts are ignored, not committed or implicitly uploaded.
