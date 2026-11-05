# Test database operations at scale

The developer-only `dbload` command runs real repository operations against fresh,
synthetic SQLite fixtures. It never accepts a production database path. Fixtures
use Core migrations and `db.Open`: WAL, foreign keys, the production busy timeout,
and one connection. Concurrent callers therefore include production pool queueing.

See [Database scale results](database-scale-results.md) for the measured campaign
and its confirmed conversation-summary query improvement.

## Build and run

```sh
mise run dbload-build

# A new directory is required. The default matrix includes 1x/10x/100x/1000x
# independent dimensions and one explicitly defined combined case.
mise run dbload -- explore -out .data/dbload/study

# Render only retained measurements. No workload runs during report generation.
mise run dbload -- report -inputs .data/dbload/study \
  -out .data/dbload/study/report.html
```

Open the HTML directly. It includes embedded data, a query-by-scale matrix,
selectable growth curves, before/after intervals, exact values, query coverage,
row counts, environment, missing cases, and artifact paths. It needs no network,
chart library, or running Core. Dark styling follows the Hearth workspace.

An operation failure or resource cutoff is evidence, not a reason to retry until
green. Inspect `results.json`, per-block `*.observations.jsonl`, and the notes.
The command returns an error for failed/incomplete execution while preserving
whatever evidence it completed. A completed short exploration is not a statistical
confirmation or a product capacity certificate.

## Run one dimension or operation

```sh
mise run dbload -- seed -dimension history -scale 1000 \
  -out .data/dbload/history-1000

mise run dbload -- run -fixture .data/dbload/history-1000/manifest.json \
  -out .data/dbload/history-read -samples 20 -blocks 3 \
  -cases devices.history-window.ascending.1000

mise run dbload -- catalog > .data/dbload/query-catalog.json
```

Use the names in `results.json` for `-cases`. `run` defaults to all implemented
operations. `-concurrency` accepts 1, 4, or 16 closed-loop callers. First/deep pages,
selective/sparse filters, history windows, inventory, Commands, Functions,
Notifications, Automations, agent conversations, dashboards, Observation writes,
and maintenance batches have separate operation identifiers.

The catalog inventories every named generated query plus the handwritten coherent
State snapshot. A completed case records only its declared query branches as
timed coverage. Inventory, timed query coverage, populated-table coverage, and
operation correctness are different claims. Uncovered statements remain visible.

### Dimensions and relationships

| Dimension | Growth | Fixed |
| --- | --- | --- |
| `population` | Independent household resource subgraphs | Per-owner fan-out and evidence depth |
| `history` | Retained evidence per owner | Inventory and definition shape |
| `fanout -relation devices` | Devices per Adapter | Entities per Device and evidence depth |
| `fanout -relation messages` | Messages per conversation | Conversation population and other relationships |
| `fanout -relation automations` | Automation population/matching fan-out | Step bounds and other relationships |
| `combined -scale 10` | Population10 × Devices/Adapter10 × history10 | Domain bounds and payload shapes |

The shared 1x household starts with two Adapters, ten Devices per Adapter, and four
Entities per Device. `manifest.json` is authoritative for actual counts, target
identities, evidence depth, fixture hashes, and limits. Scaling a parent and its
children is not silently labeled a linear multiplier. Domain-limited relationships
do not invent illegal registrations, Step trees, or active runtimes.

## Profile separately from timing

```sh
mise run dbload -- run -fixture .data/dbload/history-1000/manifest.json \
  -out .data/dbload/history-profile -cases devices.history-window.ascending.1000 \
  -samples 200 -blocks 1 -profile -variant diagnostic

mise exec -- go tool pprof -top .data/dbload/bin/dbload \
  .data/dbload/history-profile/devices.history-window.ascending.1000-0.cpu.pprof
```

Profiles include CPU and post-run heap data. `*-plans.json` contains exact generated
SQL, representative bind values, query-plan details, and explicit gaps where bind
values were not captured. Generated SQL is read from the binary's recorded Git
revision for a clean build, so a frozen baseline is not assigned candidate SQL.
Diagnostic runs must not be mixed into benchmark comparisons. Profile overhead,
fixture preparation, migration, warmup, and output checks are outside individual
operation timers but inside the campaign execution budget.

## Compare a measured fix

Freeze and commit the suite before changing production queries. Build and retain
a baseline executable, then change only the measured query/index/repository work.
Build the candidate with the same Go toolchain, build flags, and dependencies.
Never hand-edit generated files or rewrite old migrations for a benchmark.

```sh
# Save the executable from the frozen harness revision before making a fix.
cp .data/dbload/bin/dbload .data/dbload/baseline

# After implementing and validating the measured candidate:
mise run dbload-build
mise run dbload -- compare -baseline .data/dbload/baseline \
  -candidate .data/dbload/bin/dbload \
  -fixture .data/dbload/history-1000/manifest.json \
  -out .data/dbload/comparison-1 -slot 1 \
  -cases devices.history-window.ascending.1000

mise run dbload -- report \
  -inputs .data/dbload/study,.data/dbload/comparison-1 \
  -out .data/dbload/study/report.html
```

`compare` writes its hashed registration before collecting candidate data. Each
side starts from the same closed fixture image. Before pairing, `prepare` migrates
one image per variant and verifies unchanged logical data. The baseline image
must match an empty database migrated by the frozen baseline binary. Blocks clone
the prepared image, warm equally, and actually rerun in each pair. Mutating cases receive fresh images;
destructive maintenance uses one genuine batch per block rather than timing
repeated empty deletes. Those one-shot cases cannot satisfy 200-sample confirmation
and remain descriptive unless a future approved design restores each attempt.

Confirmation requires clean committed binaries with matching Go toolchain, build
flags, and dependency graph. Registrations bind block evidence to their family;
campaign slots cannot be reused under the same owned resource root. Reports retain
per-input and per-worker provenance and reject incompatible suite revisions,
logical fixtures, and environment settings. Distinct SQL hashes remain distinct
coverage records. `history-1` and `fanout-*-1` explicitly alias the shared baseline;
the growth-dimension selector never connects different dimensions into one curve.

Confirmation requires 20 complete AB/BA pairs and exactly 200 attempts per
case/side/block, with no unexpected failures, matching work, and unchanged outputs.
The experimental unit is a block, not a request. The primary estimate is the
geometric mean of paired block-p95 ratios, with a paired-block 95% bootstrap
interval and a seeded two-sided sign-randomization test. Holm adjustment keeps
missing/invalid registered cells in the family. Three candidate-family slots each
receive alpha `0.05/3`; at least a 10% reduction is required to keep a performance
change. Partial sampling cannot produce an early significant win.

Significance, practical size, and absolute gate status are separate. Provisional
concurrency-1 thresholds are 250 ms for bounded reads and 1000 ms for writes or
maintenance batches. Confirmed gate pass/fail uses a block-bootstrap interval;
unbounded scans and 4/16-caller contention are descriptive. Non-significance is
not equivalence. The report labels wide/partial comparisons inconclusive.

## Budgets and safety

The default campaign allows two hours of actual dataset/profile/measurement
execution, not two hours of coding. It initially reserves 25 minutes for seeds,
35 for exploration, 10 for profiles, 40 for confirmation, and 10 for final evidence.
`budget.json` persists charged execution. Use `-resource-root <owned campaign>` for
later seed/profile/compare commands so they share its remaining budget and disk
accounting. Confirmation child workers are charged by their parent, not twice.

Peak owned storage is limited to 8 GiB with a 4 GiB host-free reserve. Keep SQLite
temporary files and comparison binaries inside the owned campaign. Disposable
images are deleted after evidence extraction, while raw observations, manifests,
and profiles remain. Recreate a removed fixture with the recorded seed/dimension.
Workers use a 4 GiB Go heap soft limit; monitoring stops an owned worker above
8 GiB RSS. These are sampled guards, not OS-enforced quotas.

The 10-second operation cutoff is a suite safety limit, not a product deadline.
Errors, timeouts, resource-limited cells, background-load concerns, and unrun
checks stay visible. No live Core, model credential, broker, real device, host cache
flush, connection-pool tuning, or new software installation is required.

Host load is sampled at block boundaries and periodically during timed work.
Load above the logical CPU count invalidates the measurement even when every
operation succeeds. Repository correctness tests permit that status only with all
attempts completed and zero errors/timeouts; benchmark analysis still rejects it.

## Validate changes

```sh
GO_PACKAGES='./internal/dbload ./internal/cmd/dbload' mise run test
GO_PACKAGES='./internal/dbload' mise run test -- -tags=integration -count=1
unset GO_PACKAGES
mise run validate
```

These correctness gates are separate from performance evidence. They do not
establish a speedup, and a fast benchmark cannot replace them.
