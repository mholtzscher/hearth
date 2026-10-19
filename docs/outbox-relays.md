# Durable outbox relays

Device Facts and Adapter health use one
[`outbox.Relay`](../internal/platform/outbox/relay.go) implementation with separate
instances for their existing JetStream streams. Each instance has one worker and
its own delivery fault. Failure in one stream does not block the other publisher.

## Ownership

Devices still owns `device_facts_outbox`. Adapters still owns
`adapter_health_outbox`. Each source transaction commits its evidence and pending
row atomically. This consolidation adds no shared table, database migration, or
cross-module transaction.

The module source supplies oldest-first reads, deterministic message preparation,
and idempotent deletion. It classifies permanent read failures and logs safe
domain diagnostics. Schemas, identities, subjects, payloads, trace headers, stream
provisioning, and consumer recovery remain module-owned.

The platform relay owns the publication loop, wake hints, polling, acknowledgement
checks, retry delay, current delivery fault, and shutdown coordination. It reuses
`lifecycle.WorkerHandle` for cancellation and completion.

Modules pass their existing JetStream client directly to `outbox.Start`.
The relay calls `PublishMsg` through its narrow `Publisher` interface, so modules
do not supply publication callbacks or open another connection.

## Delivery contract

- Read at most 64 pending rows in durable enqueue order. No database transaction
  or query cursor remains open while publishing.
- Poll every five seconds after discovering an empty outbox. Post-commit wake
  hints are nonblocking and may be lost. The durable row is authoritative.
- Bound each read, publish, and delete to five seconds through its context.
- Delete a row only after a non-nil PubAck names the expected stream. A duplicate
  PubAck is success. Missing or wrong-stream acknowledgements retain the row.
- Retry transient failures after one second, starting at the oldest pending row.
  Hints do not bypass this backoff. Every attempt reuses the stored identity and
  deterministically mapped subject, headers, and payload bytes.
- A permanent read error may include a valid older prefix. Publish that prefix
  before stopping on poison. Mapping failures are also permanent. Preserve the
  poison row and every successor; never skip, rewrite, or discard them.

Publication remains at-least-once. A lost PubAck or failed deletion can republish
the same row. Broker deduplication lasts only for the stream's duplicate window.

## Readiness and shutdown

Both relays remain active while retrying, but expose a current `Fault()` that
fails public readiness. A successful publication and deletion, or a successful
empty read, clears a recoverable fault. Readiness never requires an empty backlog.
Poison stops the affected worker and remains a fault until operator repair and
restart.

Relay delivery state is not a canonical Adapter lease-expiry dependency.
Application assembly passes the supervisor source readiness without either
relay's activity or fault state. Existing database, connection, resource, and
consumer checks still apply. A derived delivery fault cannot suppress canonical
lease expiry.

`Drain(ctx)` withdraws readiness and wakes an idle or backing-off worker. The
original worker finishes its in-flight operation and publishes until empty; no
caller starts a replacement publisher. Concurrent drains join the same worker.
If a drain deadline expires, cancellation takes effect but pending work remains
durable. Application shutdown then joins with `Stop` before closing NATS or
SQLite. A canceled relay cannot restart publication through a later drain.

Health producers and their relay still drain before the health consumer. Device
ingestion still drains while the Device Fact relay publishes, before that relay
drains. Unpublished rows survive restart with their existing identities.
