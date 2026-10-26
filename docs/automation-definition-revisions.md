# Automation definition storage and upgrades

Automation history keeps its existing public v2 JSON documents and public revision
numbers. SQLite stores a full definition once per retained `(automation_id,
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

Both old and new Skips retain only their matched Trigger snapshots and admission
Condition decision. A revision used only by Skips therefore does not pin an
unrelated Step tree after replacement/deletion. This trades repeated compact
configuration for avoiding full-definition retention on Skip-only revisions.
Legacy Skips never contained a complete definition; migration does not invent one.

## Upgrade and rollback

Migration `00021_automation_definition_revisions.sql` runs at startup as one Goose
transaction. It audits current definitions and retained Run snapshots, shares
agreeing artifacts without renumbering, and removes per-Run definition/Condition
configuration. All Command, branch, Delay, Notification, and Function child
evidence and held-state rows are copied through the schema rebuild with foreign
keys enabled. Legacy Skips remain inline and unchanged.

The migration stops on conflicting documents under one public revision or on
missing/mismatched Run Condition configuration. The diagnostic identifies the
Automation/revision or Run; inspect the original database and restore a known-good
backup or explicitly repair the evidence before retrying. Do not resolve conflicts
by choosing the current definition for old history. Failed migration leaves the
pre-migration schema/data intact for that migration; earlier pending migrations
may already have committed.

The audit ignores object-member order but preserves array order, exact numeric
tokens, and the difference between absent members and JSON null. It rejects
duplicate object keys in definitions and Run decisions because path-based JSON
comparison cannot distinguish their occurrences safely. Those ambiguous documents
need explicit repair before upgrading; migration never chooses one interpretation.

Before upgrading:

1. Stop Core cleanly and make a consistent SQLite backup. If copying files, close
   all connections and preserve any WAL alongside the database; a lone live
   database-file copy is not a backup.
2. Keep the old binary and matching backup together. Allow maintenance downtime
   for the audit and table rebuild; migration scans retained configuration.
3. Provide free disk for replacement history/head tables, temporary copies of
   child evidence, shared artifacts, and SQLite WAL/journal growth, in addition
   to the backup. Peak space depends on existing payload/evidence size; a smaller
   post-migration logical payload does not imply an in-place or low-space upgrade.
4. Start the new binary and verify history. Removed bytes can remain in SQLite
   free pages; migration does not run `VACUUM` or promise immediate file shrinkage.

Binary rollback requires stopping Core and restoring the pre-upgrade backup with
its matching binary. This migration intentionally has no destructive Down path
and no permanent dual-write compatibility layer. Restoring a backup loses any
new records or definition edits made after that backup; preserve the upgraded
database separately if those records need investigation.

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
| 200 Runs, one revision | Inline | 6,711,000 | 7,462,912 | 690.5–705.0 | 114.9–118.2 | 116.0–118.5 |
| 200 Runs, one revision | Shared | 41,949 | 720,896 | 604.4–625.0 | 120.3–125.2 | 115.9–122.0 |
| 200 edits, one Run each | Inline | 6,711,292 | 7,462,912 | 708.0–725.3 | 116.1–131.0 | 117.4–123.5 |
| 200 edits, one Run each | Shared | 6,678,692 | 7,397,376 | 723.1–737.3 | 115.1–121.8 | 120.0–121.8 |
| 200 Skip-only edits | Inline | 72,000 | 819,200 | 678.2–700.3 | 4.5–4.7 | 4.2–4.6 |
| 200 Skip-only edits | Shared | 72,000 | 7,503,872 | 737.1–826.1 | 4.3–4.8 | 4.4–6.5 |

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
