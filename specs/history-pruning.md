# Module-owned history pruning

Status: Implemented · Effort: M

## Decision

Modules own history retention policy and deletion behavior. `hearthd` owns one scheduler, worker lifecycle, and failure logging. Replace the current hourly-only pass with **one startup pass, followed by hourly passes**.

Today `internal/app/hearthd/run.go` calls two devices operations and an automation helper; cutoff calculation and failure logging are split across layers. This change standardizes that boundary without introducing a general maintenance framework.

## Contract

Define the consumer-owned interface and task descriptor in `internal/app/hearthd/history_pruning.go`:

```go
// HistoryPruner performs one history retention pass using a fixed sweep time.
type HistoryPruner interface {
    PruneHistory(ctx context.Context, now time.Time) error
}

type historyPruneTask struct {
    name   string
    pruner HistoryPruner
}
```

Register the devices and automations services explicitly in app assembly. Neither module imports the app package.

- Inject `observationRetention time.Duration` into the devices service and `historyRetention time.Duration` into the automations service through their existing construction patterns. Keep existing configuration names, defaults, and validation constraints; modules enforce retention safety constraints. No new configuration settings.
- Devices implements `PruneHistory`: attempt observation and Entity Event pruning using the same UTC sweep time, retaining existing cutoff semantics, current-State anchors, fixed Entity Event retention, and bounded batches. Return aggregated errors if both operations fail; one failure must not suppress the other.
- Automations changes its public method directly:

```diff
-func (service *Service) PruneHistory(ctx context.Context, cutoff time.Time, batch int) (int64, error)
+func (service *Service) PruneHistory(ctx context.Context, now time.Time) error
```

  Compute the cutoff internally from injected retention, retain the existing batch size of 500, and preserve active Runs and matched-Fact receipts. Update all callers and tests; no compatibility wrapper. Counts and batch overrides are not part of the shared contract.
- Entity Event and Automation pruning retain bounded transactions; observation pruning retains its existing single-statement delete without SQL/schema changes. A pass does not promise bounded total duration. Existing context cancellation must interrupt ongoing work.

## Scheduling and failures

- Start the worker once required dependencies and module startup recovery are ready. Run the startup sweep in the worker, not as a readiness prerequisite.
- After the startup pass completes, start the hourly ticker. Reset the interval after each later pass completes: each subsequent pass starts one hour after the previous completion. Process tasks sequentially in devices-then-automations order with one UTC timestamp per pass; never overlap passes or queue missed ticks.
- Check cancellation before each task. Cancel and join the worker before closing SQLite, using existing lifecycle machinery.
- Continue to the next module after a pruning failure. Retry on the next hourly pass; pruning failures never fail readiness.
- Remove module-owned pruning failure logs. The app logs once per failed module pass with static structured event/error codes and the module name. Preserve the automation failure codes; use `core.devices_history_prune_failed` / `devices_history_prune_failed` for the combined devices pass. Do not log raw upstream errors or payloads. Normal shutdown cancellation is not a pruning failure.

## Deliverables

```text
internal/
├── app/hearthd/
│   ├── history_pruning.go       # new: interface, task list execution, scheduler
│   ├── history_pruning_test.go  # new: scheduler and failure-isolation tests
│   └── run.go                  # modify: configuration injection and worker wiring
└── modules/
    ├── devices/                # modify: constructor, PruneHistory, colocated tests
    └── automations/            # modify: constructor, history.go, callers and tests
specs/automations.md            # modify: align retention contract
docs/architecture.md            # modify: ownership and startup-sweep semantics
```

| ID | Outcome | Dependencies | Acceptance |
| --- | --- | --- | --- |
| D1 | Module methods, injected policy, and module tests (M) | None | A1–A2 |
| D2 | Shared scheduler, assembly, and lifecycle tests (M) | D1 | A3–A5 |
| D3 | Update affected docs/comments and validate (S) | D1–D2 | A6 |

## Acceptance

- **A1:** Devices tests preserve strict cutoff behavior, current-State anchors, and batching; both prune operations are attempted when one fails.
- **A2:** Automation tests preserve cutoff behavior, batching, active Runs, and matched-Fact receipts through the new signature.
- **A3:** Scheduler tests prove exactly one startup pass, later hourly execution, a shared timestamp, deterministic task order, and no overlapping passes. Use controlled ticks rather than hour-long waits.
- **A4:** A failed module does not suppress another module; failure logging occurs once per failed module pass without raw error text, and readiness is unaffected.
- **A5:** Cancellation prevents subsequent tasks, interrupts active pruning, and worker shutdown completes before database closure.
- **A6:** Update all obsolete no-startup-sweep statements and call sites; `mise run validate` passes and the resulting diff is reviewed.

## Risks and non-goals

Startup pruning adds database contention: retain existing deletion strategies, sequential execution, and readiness independence. Centralized logging can duplicate or expose diagnostics: remove old module failure logs and test safe structured output.

No schema, HTTP, wire, retention-window, or eligibility changes. No independent module timers, dynamic registration, generic `RunMaintenance`, new scheduler framework, vacuum/checkpoint work, or compatibility shims.
