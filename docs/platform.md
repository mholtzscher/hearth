# Platform lifecycle and NATS guide

Use this map when adding or reviewing admission, workers, or NATS endpoints.
Compare the existing primitive and a module consumer before introducing local
bookkeeping. Source comments define the full API contracts; this guide explains
which owner to start from.

## Choose the owner

| Need | Shared owner | Module example |
| --- | --- | --- |
| Admit operations and join their work during shutdown | [`lifecycle.AdmissionGroup`](../internal/platform/lifecycle/admission_group.go) | [Notification sends](../internal/modules/notifications/service.go) |
| Cancel and join a process-owned worker | [`lifecycle.WorkerHandle`](../internal/platform/lifecycle/worker_handle.go) | [History pruning](../internal/app/hearthd/history_pruning.go) |
| Serve plain Core NATS request/reply | [`nats.RequestReplyServer`](../internal/platform/nats/requestreply.go) | [Devices session endpoints](../internal/modules/devices/nats/session.go), [notification registration](../internal/modules/notifications/nats/registration.go) |
| Manage a provisioned JetStream consumer | [`nats.Consumer`](../internal/platform/nats/managed_consumer.go) | [Entity Event ingestion](../internal/modules/devices/nats/entity_event.go) |
| Encode Hearth envelopes, parse subjects, or propagate tracing | [`natswire`](../internal/contracts/v1/natswire) | The module NATS transports above |

## Joining and cancellation are different

**AdmissionGroup** closes admission and waits for reservations and their tracked
children. Waiting does not cancel admitted work. Close admission before waiting
so an idle observation cannot race new work. Modules decide which operations may
enter, their deadlines, and whether a public gate must close before an internal
gate. A mutex protecting those extra policy decisions can still be necessary.

**WorkerHandle** owns a worker's cancellation and completion. `Stop(ctx)` requests
cancellation and joins; `Wait(ctx)` only joins. A worker must join its own children
before returning. Scheduling, retry, and fault decisions stay with its caller.

**RequestReplyServer** owns subscription activation, callback tracking, envelope
validation, trace propagation, and reply publication. `Drain()` starts asynchronous
subscription draining; it does not join callbacks. `Close(ctx)` closes admission,
unsubscribes, and joins callbacks already admitted, including after connection
loss. Close does not guarantee completion of queued, unadmitted messages.

**Consumer** manages a JetStream consume context and joins callbacks plus its
termination hook. `Stop()` requests stopping; `Drain(ctx)` requests draining and
joins. Its `Active()` value reports lifecycle activity, not broker health.
Modules provide message handlers, acknowledgement decisions, and failure hooks.

For each primitive, a wait timeout ends the wait rather than proving work has
exited. Application shutdown keeps dependencies alive until their users have
joined; see [Core shutdown](../internal/app/hearthd/shutdown.go).

## Keep policy with its owner

- Module transports select schemas, validate routes, map domain outcomes, and
  decide operation deadlines. The request/reply server supplies the common
  transport mechanism.
- JetStream provisioning, acknowledgement/redelivery decisions, outbox retries,
  and readiness consequences belong to the owning module and application.
- Operation persistence failures follow [ADR 0026](adr/0026-scope-persistence-failures-to-operations.md).
  Sharing lifecycle mechanics does not turn them into module-wide fault gates.
- The public Adapter SDK owns its Session and Command-serving lifecycle in
  [`sdk/adapter`](../sdk/adapter). A Core server is not interchangeable with a
  concurrent SDK handler merely because both use plain NATS subscriptions.

When existing semantics differ, record that difference in the review before
deciding whether to extend the shared owner or keep a module-specific operation.
