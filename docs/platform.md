# Platform lifecycle and NATS guide

Use this map when adding or reviewing admission, workers, or NATS endpoints.
Compare the existing primitive and a module consumer before introducing local
bookkeeping. Source comments define the full API contracts; this guide explains
which owner to start from.

## Choose the owner

| Need | Shared owner | Module example |
| --- | --- | --- |
| Admit operations and join their work during shutdown | [`lifecycle.AdmissionGroup`](../internal/platform/lifecycle/admission_group.go) | [Notification sends](../internal/modules/notifications/service.go), [Function invocations](../internal/modules/adapters/function_service.go) |
| Cancel and join a process-owned worker | [`lifecycle.WorkerHandle`](../internal/platform/lifecycle/worker_handle.go) | [History pruning](../internal/app/hearthd/history_pruning.go) |
| Serve plain Core NATS request/reply | [`nats.RequestReplyServer`](../internal/platform/nats/requestreply.go) | [Adapters session endpoints](../internal/modules/adapters/nats/session.go), [notification registration](../internal/modules/notifications/nats/registration.go) |
| Manage a provisioned JetStream consumer | [`nats.Consumer`](../internal/platform/nats/managed_consumer.go) | [Entity Event ingestion](../internal/modules/devices/nats/entity_event.go) |
| Publish durable module-owned outbox work | [`outbox.Relay`](../internal/platform/outbox/relay.go) | [Device Facts](../internal/modules/devices/nats/device_fact_relay.go), [Adapter health](../internal/modules/adapters/nats/health_relay.go) |
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

**Relay** publishes ordered batches and deletes rows only after the expected
stream acknowledges them. `Drain(ctx)` keeps publication in the original worker
until empty. A drain deadline cancels the worker but does not prove completion.
Readiness requires both `Active()` and a nil `Fault()`; transient faults clear
after delivery recovers. See [outbox relays](outbox-relays.md) for timing,
poison, and source-health isolation.

For each primitive, a wait timeout ends the wait rather than proving work has
exited. Application shutdown keeps dependencies alive until their users have
joined; see [Core shutdown](../internal/app/hearthd/shutdown.go).

## Keep policy with its owner

- Module transports select schemas, validate routes, map domain outcomes, and
  decide operation deadlines. The request/reply server supplies the common
  transport mechanism.
- Modules own JetStream provisioning, consumer acknowledgement/redelivery,
  outbox storage, deterministic row decoding and message mapping, and safe
  diagnostics. The platform relay owns publication acknowledgement checks,
  retry, polling, delivery faults, and cancellation/joining. Application assembly
  composes public readiness separately from canonical lease-expiry dependencies.
- Operation persistence failures follow [ADR 0026](adr/0026-scope-persistence-failures-to-operations.md).
  Sharing lifecycle mechanics does not turn them into module-wide fault gates.
- Adapters owns the health outbox relay and WorkQueue stream. Devices owns its
  durable consumer, projection, report generations, and effective availability.
  Public health-delivery readiness is composed separately from the existing
  supervisor's base readiness, so derived failure cannot suppress lease expiry.
  See [health projection](adapter-health-projection.md) for recovery and drain order.
- The public Adapter SDK owns its Session and Command-serving lifecycle in
  [`sdk/adapter`](../sdk/adapter). A Core server is not interchangeable with a
  concurrent SDK handler merely because both use plain NATS subscriptions.
- Function catalog registration uses the Core `RequestReplyServer`; invocation
  uses one runtime-pinned plain request without application retry. The SDK's
  [FunctionServer](../sdk/adapter/function_server.go) owns concurrent handlers and
  joins their actual returns, even after replying at a deadline. Its descriptor
  preparation and immutable Session catalog remain Function policy.
- Core joins Automation Runs after agent turns while reserved Function invocations
  remain admissible, then closes internal Function admission and joins Functions
  before dependency contexts close. The Zigbee2MQTT process instead closes Function admission and joins its
  handlers before canceling the runtime and Command server, so MQTT stays usable
  through graceful draining. A real connection failure may interrupt work.
  See [Adapter Functions](adapter-functions.md).

When existing semantics differ, record that difference in the review before
deciding whether to extend the shared owner or keep a module-specific operation.
