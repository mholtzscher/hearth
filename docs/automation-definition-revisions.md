# Automation definition storage and upgrades

After the development cutover described below, Automation history keeps its
existing public v2 JSON shapes and revision numbering. SQLite stores a full
definition once per retained `(automation_id,
revision)`. The current Automation head and Runs reference that immutable artifact.
Each Run retains its own cause, matched Trigger IDs, admission evaluation, and
Step evidence. Reads assemble the full snapshot and Condition decision from the
referenced revision, never from the current head or live source history.

Deleting an Automation removes its head and held-state rows, not historical
artifacts or active Runs. History maintenance removes unreferenced revisions in
bounded batches after pruning terminal history. Current heads and all retained
Runs, including active Runs, protect their artifacts. There is no new timer,
cache, replay, or cross-module retention dependency.

## Why Skips stay inline

Skips retain only their matched Trigger snapshots and admission
Condition decision. A revision used only by Skips therefore does not pin an
unrelated Step tree after replacement/deletion. This trades repeated compact
configuration for avoiding full-definition retention on Skip-only revisions.

## Upgrade and rollback

Migration `00021_automation_definition_revisions.sql` is an intentional,
destructive development cutover. It runs once at startup in one Goose transaction
and discards all existing Automation-owned data:

- Automation definitions.
- Run and Skip history, including Run-owned Command attempts, branch decisions,
  Delay, Notification, and Function evidence.
- Held-state rows, matched-Fact receipts, and schedule progress.

It leaves other modules' data and schema untouched, including Devices, Entities,
State and Command records, Adapter runtimes and Function Invocations, notification
destinations and attempts, and Agent conversations. Run-owned evidence is removed,
but independently retained Command, Notification, and Function records remain.
Foreign keys stay enabled; existing cascades clear Automation child rows when the
old parents are dropped. There are no definition audits, deduplication backfills,
or temporary child-data copies.

Export any definitions you want to keep, stop Core cleanly, and take a consistent
SQLite backup if rollback or old Automation history matters. A live database-file
copy without its WAL is not a consistent backup. Start the new binary and recreate
the desired Automations. Old Runs are discarded, not replayed. Later startups do
not repeat the reset once migration 21 is recorded as applied.

A failure rolls back this migration's reset and schema changes together. Earlier
pending migrations may already have committed. Removed bytes can remain in SQLite
free pages; no `VACUUM` or immediate file shrinkage is promised.

Migration `00022_unified_automation_step_executions.sql` performs another
intentional Automation-only reset under the same backup and recreation procedure.
It preserves the shared revision layout and history indexes, replaces the five
Run child-evidence tables with `automation_step_executions`, and clears all
Automation-owned records again. Later startups preserve new data once migration
22 is recorded as applied. Independently retained external records remain intact.

Down intentionally refuses conversion. To use the previous binary with this
deployment's data, stop Core and restore its matching pre-upgrade database backup.
That restore also loses records and edits made after the backup. There is no
dual-write compatibility path or automatic deployment reset outside these migrations.

## Storage comparison

Run the opt-in fixture through the project test task:

```sh
HEARTH_REVISION_STORAGE=1 GO_PACKAGES='./internal/modules/automations/sqlite' \
  mise run test -- -tags=integration \
  -run '^TestDefinitionRevisionStorageComparison$' -count=1 -v
```

Measured locally on Linux amd64, AMD Ryzen 9 3900X with 24 logical CPUs,
with Go 1.27.1 and no race instrumentation,
using the baseline schema through migration 20 and the new schema through 21.
Each side uses production SQLite connection policy, WAL, and the same dependencies.
Five independent file-backed databases per side alternate order. No operating
system cache flush is performed. Each sample writes 200 entries in individual
transactions, then reads and decodes all 200 details twice. Setup, head deletion,
artifact collection, and page accounting are outside the write timer.

Definitions contain one Command with a 32 KiB synthetic parameter string and one
admission Condition. Repeated Runs use one revision; frequent edits use one Run
per revision. Skip-only revisions each produce one inline busy Skip. Both sides
delete the head before retained-byte accounting. Shared artifacts with no Run
reference are collected then. This models one maintenance pass after the writes,
not collection after every edit.

The fixture executes storage SQL and production codecs, not the full admission
service. It excludes Trigger matching, State reads, child evidence writes,
execution workers, transport serialization, and concurrent database users. The
new repository uses an additional indexed revision lookup for Run detail; the
comparison uses a join to return the equivalent full configuration payload. Its
timings are not repository or endpoint latency claims.

Observed ranges over five samples, in milliseconds:

| Workload | Layout | Retained JSON bytes | Allocated database page bytes | Write | First detail scan | Warm detail scan |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 200 Runs, one revision | Inline | 6,711,000 | 7,462,912 | 692.4–713.6 | 115.3–119.9 | 115.3–121.8 |
| 200 Runs, one revision | Shared | 41,949 | 720,896 | 605.0–615.2 | 118.8–124.7 | 118.2–124.0 |
| 200 edits, one Run each | Inline | 6,711,292 | 7,462,912 | 706.9–730.2 | 113.0–116.5 | 114.3–118.3 |
| 200 edits, one Run each | Shared | 6,678,692 | 7,397,376 | 717.0–738.0 | 116.0–120.4 | 116.6–119.1 |
| 200 Skip-only edits | Inline | 72,000 | 819,200 | 676.4–689.9 | 4.1–4.7 | 4.1–5.0 |
| 200 Skip-only edits | Shared | 72,000 | 7,503,872 | 725.9–742.4 | 4.1–4.9 | 4.3–5.0 |

Retained JSON counts full definition artifacts, Condition decisions, and inline
Skip Trigger snapshots, not SQLite record/index overhead or public response
bytes. Allocated page bytes are `page_count * page_size` after WAL checkpoint,
including free pages. No vacuum runs. Skip-only shared artifacts are logically
collected but their pages remain allocated; the physical growth is a cost of
deferring collection to maintenance.

Repeated Runs remove repeated definition bytes and definition encoding on writes.
Frequent edits share little configuration and pay additional artifact writes and
foreign-key/index work. Every Run detail still decodes a full definition, and
compact Condition evidence must be assembled with that definition. These results
show substantial byte savings only when revisions are reused. They do not
establish a general read or write speedup, a production size saving, or a capacity
guarantee.
