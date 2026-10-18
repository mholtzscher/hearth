# Adapters module ownership

Adapters owns configured Adapter runtime identity, lease and current-health
writes, Adapter health history, and management reads. Vendor implementations
remain in `internal/adapters`; the SDK remains independent of Core modules.

| Package | Responsibility |
| --- | --- |
| `adapters` | Domain values, pure validation, runtime use cases, management reads, publication-store port |
| `adapters/sqlite` | Adapter-only transactions, immutable health heads, ordered publication outbox, own queries and generated rows |
| `adapters/nats` | Session claim/heartbeat/release ingress, health stream, single publication relay |
| `adapters/api` | Existing Adapter HTTP/MCP operations, resource templates, models, and cursors |

A material runtime health mutation commits its authoritative evidence, history,
next immutable revision, and outbox notification in one transaction. Unchanged
heartbeat evidence refreshes neither advance revisions nor publish updates.
Startup initializes missing legacy heads and queues current snapshots. Reconnect
resumes pending outbox publication and durable consumption without queuing new
snapshots. Restart Core to repair current projection state after delivery-state loss.

The relay retries oldest-first and deletes only after a PubAck for the health
stream. Poison evidence stays durable and faults delivery. Transient failures
retry with bounded backoff. Its lifecycle uses `lifecycle.WorkerHandle`; drain
retains one publisher and callers join actual completion before dependencies close.

Adapters never writes Devices tables and never imports Devices. Devices imports
Adapter-owned domain values, consumes health snapshots through its own durable,
and owns projection and effective availability transactions. Both SQLite
repositories are constructed independently from the shared database handle. Narrow
authoritative reads for control and fencing remain in their consuming modules.

Application assembly owns the shared NATS connection, startup, the existing sole
lease-expiry supervisor, public readiness, and shutdown. Derived-delivery failures
do not gate the supervisor's original base readiness checks. No readiness check
promises an empty backlog or current projection.

Queries in `sqlite/dbqueries` and shared migrations generate `sqlite/dbsqlc`
through root `sqlc.yaml`. Tests belong beside their domain, real SQLite, or real
NATS boundary. Run the repository mise tasks; do not edit generated code by hand.

See the [operator guide](../../../docs/adapter-health-projection.md) for lag,
retention, recovery, upgrade, and rollback semantics.
