# Adapters module ownership

Adapters owns configured Adapter runtime identity, lease and current-health
writes, Adapter health history, management reads, Function catalogs, and invocation
evidence. Vendor implementations
remain in `internal/adapters`; the SDK remains independent of Core modules.

| Package | Responsibility |
| --- | --- |
| `adapters` | Runtime service, separate Function service, domain values, pure preparation, repository ports |
| `adapters/sqlite` | Adapter-only transactions, health heads/outbox, immutable catalogs and invocation evidence, own queries and generated rows |
| `adapters/nats` | Session/catalog ingress, one-shot Function invocation, health stream and relay |
| `adapters/httpapi` | Adapter and Function HTTP operations, HTTP DTOs, and Huma registration |
| `adapters/mcpapi` | Independent Adapter and Function tools, MCP DTOs, plain JSON Schema contracts, and MCP error mapping |
| `adapters/internal/apicursor` | Scoped cursor codecs consumed by both transport packages |

Both transports call the module services directly. MCP uses the MCP SDK, not
Huma handlers or schema types, and advertises tools rather than resources.

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

`Service` owns runtime and management behavior. `FunctionService` owns catalog
registration, revision-pinned invocation, recovery, retention, and admission.
They have separate repository interfaces and lifetimes within one product module.
The SDK owns its equivalent public types without importing Core modules.
`contracts/functionschema` owns shared bounded dynamic-schema compilation and
normalization; `contracts/v1` owns strict wire envelopes.

Function admission commits a route/descriptor/argument snapshot before one NATS
request. Completion is a separate immutable write. Caller cancellation cannot
abandon committed work. `lifecycle.AdmissionGroup` joins invocations before NATS
and SQLite close; no durable execution worker or replay exists. Startup marks
unfinished evidence unknown. The shared history worker prunes terminal evidence
older than thirty days, without pruning runtime/health evidence or catalogs.

See [Adapter Functions](../../../docs/adapter-functions.md) for public contracts,
non-secret payload rules, SDK serving, upgrade, and rollback.

See the [operator guide](../../../docs/adapter-health-projection.md) for lag,
retention, recovery, upgrade, and rollback semantics.
