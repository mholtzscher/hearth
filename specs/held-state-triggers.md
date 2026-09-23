# Held-State Triggers

Status: Approved simplified scope; ready for task breakdown. Domain language: [CONTEXT.md](../CONTEXT.md).

## Purpose and scope

A household author can start an Automation when a light has reported on for 60 seconds, or when a sensor has reported above a threshold for a configured duration. No second report is needed at the deadline while Core is running. This release adds one `held_state` Trigger kind to existing Automation definitions and history. It adds no route, Device Fact family, consumer, config flag, or pending-hold dashboard. Duration Conditions, Delay Steps, and clock schedules remain separate work.

One SQLite row per `(automation_id, trigger_id)` tracks the latest processed Observation receive order and an idle, pending, or consumed hold. The existing Device Fact consumer updates that row in its admission transaction. An app-owned worker scans due holds every second, checks current accepted State, and commits one Run or Skip with the existing busy and Condition rules. It does not scan Observation history. This reduces storage, worker logic, and restart handling, with the limitations stated below.

### Definition example

```json
{
  "name": "Light left on",
  "enabled": true,
  "triggers": [{
    "id": "light_on_60s", "kind": "held_state", "entity_id": "ent_<canonical-id>",
    "comparisons": [{"value_pointer": "", "operator": "eq", "operand": true}],
    "for_seconds": 60
  }],
  "steps": [{"id": "notify", "entity_id": "ent_<canonical-id>",
    "operation": "<supported-operation>", "parameters": {}}]
}
```

The placeholders stand for valid canonical IDs and a supported Operation. Keep the strict v1 definition schema ID; older definitions remain valid. `comparisons` requires 1 to 8 existing `ObservationComparison` values, all of which must match. `value_pointer` addresses the State value itself; the empty pointer selects a scalar. A missing or incompatible selected value does not match; malformed stored State is a read error. `for_seconds` is an integer from 1 to 2,592,000 (30 days). There is no disposition selection or previous-value predicate: both accepted `applied` and `unchanged` reports participate. Save-time validation requires an existing stateful Entity and validates pointers against current State if present; absent State, disablement, or unavailability does not prevent saving.

## Hold and admission behavior

1. For each accepted Observation Fact and each enabled held Trigger on its Entity, load the stored `observations.receive_order`. Ignore that Trigger's Fact when its order is at or below `last_receive_order`. Otherwise advance that cursor in the same SQLite transaction as ordinary Fact admission. Rejected Observations produce no Fact; availability, enablement, and adapter health do not end a hold.
2. A nonmatching accepted value makes the row idle and clears its start and deadline. A matching value changes an idle row to pending only if the Fact is at most 30 seconds old and its Core `emitted_at` is later than both the current definition's `updated_at` and this process's startup instant. Use Core `emitted_at` as `started_at` and set `due_at = started_at + for_seconds`. Matching reports on pending or consumed rows leave their phase and start unchanged. Stale nonmatching Facts still clear a hold when their receive order advances; stale matching Facts cannot start one. An eligible matching report after a nonmatch starts a fresh hold even when its disposition is `unchanged`.
3. At or after `due_at`, re-read the current definition, hold phase, and `entity_states` row in the same SQLite transaction as the outcome. If the definition/revision changed or State is absent or fails the predicate, make the row idle without a Skip and advance `last_receive_order` to at least the latest State receive order, when present. If State matches, check the existing one-running-Run guard, then evaluate Conditions once using the service's coherent pre-read snapshot. Commit a Run or one of the existing `automation_busy`, `conditions_false`, or `conditions_unknown` Skips, and mark the row consumed atomically. An expired hold does not get a `stale_fact` Skip. A consumed row cannot fire again until a later accepted nonmatching Observation returns it to idle.
4. Every successful definition replacement, including a Step-only edit or enablement change, removes that Automation's hold rows within the revision transaction. Deletion also removes them. An already admitted Run keeps its immutable snapshot. Existing manual and immediate Fact admission remain independent; if the same Observation also matches an immediate Trigger, it can produce an immediate outcome while updating a hold. The ordinary single-running-Run guard settles any race at expiry.
5. At startup, interrupt active Runs as today and reset pending hold rows to idle, clearing their start/deadline while retaining `last_receive_order`; leave consumed rows consumed. The service passes the Core startup instant as the Fact start cutoff, so buffered pre-startup Facts cannot start replacement holds. A post-startup accepted matching Observation starts a full new duration, even if it reports `unchanged`. A consumed hold still needs a nonmatching Observation to re-arm. No downtime catch-up or Run replay occurs.

The receive-order cursor prevents a duplicate or older Fact from reopening an idle or consumed hold. Rows remain after cancellation and admission for that reason. The expiry transaction prevents two workers, a retry, or a concurrent manual admission from creating two outcomes for the same hold. Conditions retain their existing ADR 0022 semantics: the service reads a coherent State snapshot before the admission transaction, so that snapshot is not atomic with device ingestion. The held Entity's current State check is inside the transaction.

## Implementation contracts

### Types, definition, and history

```diff
diff --git a/internal/modules/automations/model.go b/internal/modules/automations/model.go
@@
 const (
   TriggerKindObservation TriggerKind = "observation"
   TriggerKindEntityEvent TriggerKind = "entity_event"
+  TriggerKindHeldState TriggerKind = "held_state"
 )
+type HeldStateTrigger struct {
+  EntityID devices.EntityID
+  Comparisons []ObservationComparison // 1..8, all required
+  ForSeconds int64 // 1..2592000
+}
 type Trigger struct {
   ID TriggerID
   Kind TriggerKind
   Observation *ObservationTrigger
   EntityEvent *EntityEventTrigger
+  HeldState *HeldStateTrigger // exactly one family payload
 }
@@
 const (
   RunSourceDeviceFact RunSource = "device_fact"
   RunSourceManual RunSource = "manual"
+  RunSourceHeldState RunSource = "held_state"
 )
+type HeldStateEvidence struct {
+  TriggerID TriggerID
+  StartedAt time.Time
+  DueAt time.Time
+}
 type Run struct {
   // existing fields
+  HeldState *HeldStateEvidence // iff Source is held_state; Fact is nil
 }
 type Skip struct {
   // existing fields
+  HeldState *HeldStateEvidence // iff Source is held_state; Fact is nil
 }
 type HistorySummary struct {
   // existing fields
+  HeldState *HeldStateEvidence // iff Source is held_state
 }
```

The new Trigger encodes as `{"id", "kind":"held_state", "entity_id", "comparisons", "for_seconds"}` with no other family fields. Update the embedded `automation-definition.schema.json`, codec, normalization, `ValidateTrigger`, `Trigger.EntityID`, and reference validation. Reuse `MatchObservationComparison` and existing pointer rules; guard the duration conversion. `MatchTriggers` returns no immediate Fact match for held Triggers. Run snapshots contain the full definition and exactly one matched Trigger ID for held admission; Skips contain exactly one matched Trigger snapshot. History carries the Automation revision, source, Trigger ID, start and deadline, but no copied Observation value or Fact summary.

In `00007_automation_held_state.sql`, rebuild the SQLite `automation_history` table so `run_source` and `skip_source` admit `held_state`. Add nullable `hold_trigger_id`, `hold_started_at`, and `hold_due_at`, all present exactly for held rows. Such rows have no Fact columns. Preserve the old device-fact/manual constraints, previous migration columns, old rows, `automation_run_steps` foreign keys, and the history page, active-Run, and fact-outcome indexes. Verify `PRAGMA foreign_key_check` before completing the FK-safe rebuild. Update `sqlite/dbqueries/automations.sql`, mappings, and generated `dbsqlc`.

Existing HTTP create/replace/get and MCP definition discovery accept/return the new Trigger. Existing history list/detail expose `source:"held_state"`, the `held_state` object with `trigger_id`, `started_at`, `due_at`, and omit `fact` using the current optional-field convention. Old history JSON shapes stay unchanged. Update the API/MCP DTO enums and `web/src/api/types.ts`; there is no pending-hold endpoint or dashboard workflow.

### Persistence and service boundary

```sql
CREATE TABLE automation_holds (
  automation_id TEXT NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL CHECK (revision >= 1),
  trigger_id TEXT NOT NULL,
  last_receive_order INTEGER NOT NULL CHECK (last_receive_order > 0),
  phase TEXT NOT NULL CHECK (phase IN ('idle','pending','consumed')),
  started_at TEXT,
  due_at TEXT,
  PRIMARY KEY (automation_id, trigger_id),
  CHECK ((phase = 'pending' AND started_at IS NOT NULL AND due_at IS NOT NULL)
      OR (phase <> 'pending' AND started_at IS NULL AND due_at IS NULL))
);
CREATE INDEX automation_holds_due_idx
  ON automation_holds(due_at, automation_id, trigger_id) WHERE phase = 'pending';
```

The first processed accepted Observation for an enabled held Trigger creates the row. A nonmatching first report creates an idle row and its receive-order watermark. The row stores no value; definition and current State supply predicate and evidence. Definition replacement/deletion removes rows transactionally. There is no separate Fact receipt table or Observation-history checkpoint. The held admission repository reads the device-owned `observations` identity/order on Fact processing and `entity_states` on expiry through the shared SQLite transaction. These reads do not change device tables or the Devices module's ownership of ingestion and State projection.

```diff
diff --git a/internal/modules/automations/repository.go b/internal/modules/automations/repository.go
@@
 type Repository interface {
   DefinitionRepository
-  AdmitDeviceFact(context.Context, DeviceFact, devices.EntityStateSnapshot, time.Time) (AdmissionResult, error)
+  AdmitDeviceFact(context.Context, DeviceFact, devices.EntityStateSnapshot,
+    time.Time, time.Time) (AdmissionResult, error) // admission time, Core startup instant
+  ListDueHeldStates(context.Context, time.Time, int) ([]HeldStateCandidate, error)
+  AdmitDueHeldStates(context.Context, devices.EntityStateSnapshot,
+    time.Time, int) (AdmissionResult, int, error) // outcomes, processed rows, error
+  ResetPendingHeldStates(context.Context) error
 }
```

`HeldStateCandidate` carries Automation ID and revision for the service to collect current Condition Entity IDs; the repository rechecks both inside admission. `ListDueHeldStates` returns at most 100 rows ordered by `(due_at, automation_id, trigger_id)`. The service takes one Condition snapshot via `AutomationDevices.GetEntityStateSnapshot` for the candidate union and retries stale revision or missing snapshot coverage within the existing two-second admission timeout. `AdmitDueHeldStates` reads current definition, hold row, and State in one transaction, commits the Run/Skip plus `consumed` state, and returns committed Runs for the existing executor. Its processed-row count includes cancellations so the worker knows whether to scan another batch. It treats a missing Entity/State as cancellation, but propagates storage or State-decoding errors. The Fact consumer keeps its two-second bound and Naks transient transaction failures. All timestamps use fixed-width UTC encoding.

### Worker and lifecycle

`internal/app/hearthd/held_state_scheduler.go` owns a single one-second ticker and calls the service for due batches until fewer than 100 rows are processed, then waits for the next tick. A due hold never fires before its deadline. Under idle SQLite and an active worker it enters an admission transaction within about one second; backlog and contention can delay it. Use an injected clock/tick source for deterministic tests. Start the `lifecycle.WorkerHandle` after `ResetPendingHeldStates` and before inbound Fact producers. Shutdown joins it before draining Automation workers or closing SQLite. Unexpected worker termination or database scan failure closes automatic admission, fails readiness, and returns a staged runtime error from `serveHTTP` via the worker's `Closed()`/`Wait()` methods. Readiness does not require an empty backlog.

## Deliverables and acceptance

| ID | Outcome | Owner paths | Depends on | Acceptance |
| --- | --- | --- | --- | --- |
| D1 | Trigger/definition contract and API mapping | `internal/modules/automations/{model.go,definition.go,automation-definition.schema.json,held_state.go,api/}`, `web/src/api/types.ts` | none | A1, A2 |
| D2 | Hold table and truthful history migration | `internal/platform/db/migrations/00007_automation_held_state.sql`, `internal/modules/automations/sqlite/{held_state.go,definitions.go,history_mapping.go,dbqueries/,dbsqlc/}` | D1 | A3, A4 |
| D3 | Fact updates, due admission, and Conditions | `internal/modules/automations/{admission.go,conditions_admission.go,repository.go,held_state.go}`, `internal/modules/automations/sqlite/{admission.go,held_state.go}` | D1, D2 | A4, A5, A6 |
| D4 | Startup reset, worker, readiness, shutdown | `internal/app/hearthd/{held_state_scheduler.go,run.go,shutdown.go,runtime_readiness.go}` | D2, D3 | A7, A8 |
| D5 | Docs and synthetic end-to-end validation | `docs/{automation-gap-analysis.md,automation-conditions.md}`, `CONTEXT.md`, relevant `*_test.go` | D1–D4 | A1–A9 |

- **A1:** Definition encode/decode, HTTP create/replace/get, and MCP schema discovery round-trip boolean and numeric predicates. Reject unknown/mixed family fields, invalid pointers, missing comparisons, and fractional, zero, or over-30-day duration; earlier definitions decode unchanged.
- **A2:** Save-time validation rejects missing or stateless Entities but permits unavailable, disabled, or never-observed stateful Entities. Immediate Observation/Entity Event Triggers and manual Runs retain their contracts.
- **A3:** Migrate seeded legacy Runs, Skips, Conditions, and Step rows without changing their decoded JSON; pass `foreign_key_check`. Held Run/Skip history has the correct source, Trigger, start/deadline, and definition snapshot or matched Trigger after definition deletion. Other sources retain their existing optional `fact` behavior.
- **A4:** On at `t=0` and `unchanged` on at `t=40s` produce one decision at or after `t=60s` without another report. Off at `t=40s` cancels without a Skip; later on starts anew. Values 26→27 preserve a `>25` hold; 26→24→26 resets it. Availability changes alone do not cancel. A false/unknown Condition or busy Automation produces exactly one existing Skip.
- **A5:** Fact redelivery and older out-of-order Facts do not rewind the receive-order cursor or duplicate a hold; concurrent due scans and manual admission produce at most one outcome per hold and at most one running Run per Automation. A Fact that also matches an immediate Trigger retains the immediate outcome.
- **A6:** Facts older than 30 seconds, committed before a definition revision, or committed before Core startup cannot start a hold. A newer nonmatching Fact can clear one. At expiry, currently absent/nonmatching State cancels with no Skip; a storage/decoding failure fails the worker. A brief off→on hidden by Fact backlog is explicitly outside the continuity guarantee.
- **A7:** Restart halfway through or after a deadline resets a pending hold; a subsequent post-startup matching `unchanged` report begins a full duration. Consumed holds stay consumed until a newer nonmatch. Definition replacement, including Step-only edits, disabling, and deletion clear rows transactionally; no retroactive start occurs.
- **A8:** A fake-clock/ticker test proves no early admission, roughly one-second idle processing, and no spin without due work. Worker failure drops readiness and stops automatic admission; shutdown joins it before SQLite closes.
- **A9:** An automated synthetic Observation scenario yields held history through the existing API and verifies one Run or Skip. Run `mise run validate` and review generated diffs; physical-device testing is optional.

## Accepted trade-offs

- The timer checks latest State and processed Facts, not every accepted Observation between start and expiry. If Fact delivery lags across off→on and the latest State is on, an old hold may fire despite the interruption. The original ordered-history verifier and hourly checkpoints would prevent this, but require more storage, concurrency, pruning, and recovery logic.
- Restart loses elapsed time on pending holds and missed deadlines. A new accepted matching report is needed to start a full hold; a device that stays on but goes silent will not fire after restart. This avoids the overdue-hold recovery phase and its freshness policy. Consumed holds remain consumed to avoid duplicate automatic action.
- History records the Trigger and scheduled hold window rather than copying starting/latest Observation values. This keeps the Run/Skip source truthful without expanding the history evidence model. A system clock jump can move UTC deadlines relative to physical time; the worker still requires `now >= due_at`.
