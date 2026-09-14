# Core worker lifecycle handle

## Goal and scope

Extract single-worker cancellation and joining from the Device Fact relay and
Core health supervisor. Keep scheduling, polling, wake hints, retries, readiness,
and fault classification with their current owners. No adapter changes, worker
restarts, panic recovery, or generic supervisor framework.

## Platform contract

```go
func StartWorker(ctx context.Context, work func(context.Context) error) *WorkerHandle
func (worker *WorkerHandle) Stop(ctx context.Context) error
func (worker *WorkerHandle) Wait(ctx context.Context) error
func (worker *WorkerHandle) Closed() <-chan struct{}
```

- StartWorker invokes work exactly once in a goroutine with a child context.
  Parent values, deadlines, and cancellation propagate. Context and callback
  must be non-nil; zero-value handles and copying are unsupported.
- Stop cancels that child context before waiting, even if its own wait context
  is already canceled. Concurrent and repeated calls are safe.
- Wait only joins. Canceling its context does not cancel the worker.
- A canceled wait does not abandon tracking. Closed closes only after the worker
  returns, including its deferred cleanup and release of its child context.
- Wait returns the terminal error unchanged, including context cancellation if
  the worker returns it. The caller decides whether cancellation is normal.
  If completion and wait cancellation are both ready, either result may win.
- The worker must join any goroutines it starts. Panics are not recovered.
  Cancellation is cooperative; a worker that ignores it can outlive Stop's wait
  budget. Its dependencies must remain available until Closed closes.

AdmissionGroup remains separate: admission draining preserves admitted work;
WorkerHandle.Stop requests cancellation of a long-lived worker.

## Caller integration

### Device Fact relay

Replace the stop channel, completion channel, cancel function, and stop-once
bookkeeping with WorkerHandle. The publication loop uses its supplied context
for I/O, idle waits, and retry waits. Normal shutdown returns nil; poison returns
the original permanent error after its existing fault recording and diagnostic.

Drain still clears readiness, cancels and joins the worker within its budget,
then synchronously publishes the remaining outbox. A failed join must never
start this second publication phase. The existing fault latch remains because
poison can also be discovered in the synchronous phase after the worker exits.
Drain calls remain caller-serialized; this extraction does not add concurrency
support for the synchronous publication phase.

### Health supervisor

Keep the initial readiness check synchronous before returning from construction.
The worker owns only subsequent ticks. Stop remains an unbounded cancellation
and join. Recovery grace, lease expiry, and diagnostic suppression stay local.
Core shutdown order and public caller signatures do not change.

## Layout and deliverables

```text
internal/platform/lifecycle/
  worker_handle.go              new: single-worker lifecycle [D1]
  worker_handle_test.go         new: primitive concurrency contracts [D1]
internal/modules/devices/nats/
  device_fact_relay.go          modify: worker ownership, preserve drain policy [D2]
  device_fact_relay_test.go     modify: blocked-publication regression [D2]
internal/app/hearthd/
  health_supervisor.go          modify: worker ownership, preserve initial check [D3]
  health_supervisor_test.go     modify: stop during readiness callback [D3]
```

D2 and D3 depend on D1. No domain types, wire contracts, or persistence changes.

## Acceptance and verification

- D1: deterministic concurrency tests protect exactly-once start, child-context
  propagation, cancellation-before-join, timeout without loss of tracking,
  cleanup-before-completion, unchanged terminal errors, and concurrent callers.
- D2: a blocked publication survives an expired join without a second publisher;
  a later drain publishes its retained row. Existing ordering, poison, retry,
  wake, durable-restart, and cancellation regressions remain green.
- D3: initial readiness is synchronous; Stop cancels a subsequent blocked check
  but does not return before its cleanup finishes or run lease expiry afterward.
- Run `mise run validate`, inspect the diff, and check whitespace. No new test
  dependencies are needed; primitive concurrency tests use testing/synctest.

## Risks

- Starting a goroutine before assigning the handle can race self-reference.
  Workers receive their context as an argument and never read their handle.
- A wait error and a worker error share an error return. The relay distinguishes
  its permanent poison class from wait errors; the handle never classifies or
  suppresses errors on behalf of callers.
