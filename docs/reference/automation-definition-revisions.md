# Automation definition storage and upgrades

Automation history keeps its revision numbering. Migration 23 preserves stored v2
literal definitions without inventing historical value evidence. SQLite stores a
full definition once per retained `(automation_id, revision)`. The current
Automation head and Runs reference that immutable artifact.
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

Migration 23 does not repeat these resets. It preserves post-22 heads and retained
revisions, including revisions referenced only by history after replacement or
head deletion. It keeps existing literal definition JSON byte-for-byte
and labels existing Run values `legacy_unavailable`. Core validates documents
against a frozen v2 schema inside the migration before changing rows. See
[expression upgrade guidance](automation-expressions.md#unified-history-and-upgrade)
for bounds, the separate unmerged-PR database lineage and backup rollback.

The opt-in [revision storage comparison fixture](../../internal/modules/automations/sqlite/definition_revisions_storage_integration_test.go)
models both layouts with synthetic data. It does not establish production
latency or capacity.
