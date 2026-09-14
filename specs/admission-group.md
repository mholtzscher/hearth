# AdmissionGroup

## Goal and scope

Own in-process admission and work tracking once, replacing duplicated lifecycle
bookkeeping in Commands and Automation Runs. Preserve domain admission rules,
Command deadlines, caller-cancellation behavior, no-replay policy, and the
application's shutdown ordering. No new dependency or River integration.

## Types and interface

Package `internal/platform/lifecycle` provides pointer-owned types:

```go
func NewAdmissionGroup() *AdmissionGroup
func (g *AdmissionGroup) TryAcquire() (*Reservation, bool)
func (g *AdmissionGroup) AdmissionOpen() bool
func (g *AdmissionGroup) CloseAdmission()
func (g *AdmissionGroup) Wait(ctx context.Context) error

func (r *Reservation) Go(work func())
func (r *Reservation) Release()
```

- A new group is open and idle. Acquire atomically checks admission and tracks a
  reservation before validation or an admission transaction starts.
- Closing admission is permanent, idempotent, and does not join or cancel work.
- A live reservation may launch tracked children even after admission closes.
  Registration precedes launch, so transaction-to-worker handoff has no idle gap.
- Release is idempotent and releases only the reservation, not its children.
  Launching after release is programmer misuse and panics. Worker panics are not
  recovered; child tracking is released with a defer.
- Wait observes the current idle transition without changing work. Close
  admission first for a reliable final join. While admission is open, Wait does
  not promise to track future admissions after an idle transition.
- Wait returns nil on idle or the context error on cancellation. If both are
  ready, either result is possible. A canceled wait does not cancel workers.
- Methods are concurrency-safe. Groups require construction; reservations require
  acquisition. Zero-value use and copying reservations/groups are unsupported.

No queues, retries, persistence, cancellation policy, worker limits, fault
recovery, or domain-specific decisions belong in this primitive.

## Integration and ownership

```text
internal/
  platform/lifecycle/
    admission_group.go          new: admission and tracking primitive
    admission_group_test.go     new: deterministic concurrency contracts
  modules/devices/
    service.go                 modify: own Command AdmissionGroup and lifecycle wrappers
    command_lifecycle.go       remove: consolidate lifecycle methods in service.go
    command.go                 modify: explicitly transfer Reservation ownership
    command_logging.go         modify: document the reservation release boundary
    command_lifecycle_test.go  modify: assert behavior rather than counters
  modules/automations/
    service.go                 modify: own Automation AdmissionGroup, lifecycle wrappers, and domain fault logging
    lifecycle.go               remove: consolidate lifecycle methods in service.go
    admission.go               modify: reserve, commit, launch children, release
    execution.go               modify: remove duplicate worker release
    admission*_test.go         modify/new: admission handoff and logging regressions
```

Commands transfer one reservation into their existing detached worker and release
it before potentially blocking creation diagnostics. They do not use `Go`, since
the diagnostic tail deliberately outlives lifecycle tracking. Pre-transfer errors
release the reservation before logging.

Automations reserve before persistence, launch every committed Run through `Go`,
then release the parent before admission diagnostics. Error paths defer release.
Domain busy checks, stop-before-next-Step, and executor fault logging remain in
Automations. Public admission/readiness/wait interfaces remain unchanged.

## Deliverables and acceptance

1. **D1 — primitive** (S), platform files; no dependencies. Tests establish
   close/refusal, reservation-to-child handoff, fan-out, idempotence, and wait
   cancellation without affecting work.
2. **D2 — both integrations** (M), module files; depends on D1. Existing lifecycle,
   interrupted-run, readiness, and shutdown-order tests pass. Command creation
   logging stays outside tracking; in-flight admission and all committed Runs
   remain joined during drain.
3. **D3 — verification** (S), associated tests; depends on D1/D2. Run
   `mise run validate`, inspect generated/formatting changes, and review the diff.

Principal risks are premature reservation release (lost join across commit and
launch) and excessive reservation lifetime (diagnostics block shutdown). Tests
must exercise both boundaries, not inspect private counters.
