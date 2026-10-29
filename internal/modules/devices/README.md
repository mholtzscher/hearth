# Devices module ownership

`devices` is one product module. Its packages separate delivery and persistence
from domain behavior. It owns Commands, Observations, Entity Events, and Entity
availability. The [Adapters module](../adapters/README.md) owns runtime and health.

## Package boundaries

| Package | Owns |
| --- | --- |
| `devices` | Domain types, Entity-type catalog, use cases, persistence interfaces, Command admission and in-memory outcome waits |
| `devices/api` | HTTP operations, request/response models, cursors, and error mapping |
| `devices/nats` | Device wire payloads and mapping, request/reply, durable consumption policy, and Device Fact publication |
| `devices/sqlite` | Transaction coordination, persistence reads/writes, SQL row mapping, query sources, and generated query code |

The HTTP, NATS, and SQLite packages depend on `devices`. The domain package does
not import them. Application assembly constructs the SQLite repository, Command
sender, and Service and owns process lifecycle. No transport depends on another
transport or selects its own persistence implementation.

Within the domain and SQLite packages, files are grouped by concern. A filename
split does not create a separate service, repository, or transaction boundary.
Shared SQL conversions remain inside SQLite; pure domain decisions operate on
domain values rather than generated query rows.

SQLite row decoders and column encoders follow the
[mapping standards](../../../docs/sqlite-mapping-standards.md). Resource mapping
files own conversion; operation files retain SQL and transaction-dependent checks.

## Atomic operations

Keep these operations transactional when moving or changing code:

- Registration reconciles the Binding, Device, submitted Entities, external
  mappings, and initial availability together.
- Observation projection records disposition, advances canonical State, satisfies
  a matching linked Command, and queues the accepted Observation's Device Fact
  together.
- Entity Event recording establishes identity/disposition and queues an accepted
  event's Device Fact together, without changing State or Commands.
- Adapter health delivery applies the local revisioned projection, invalidates
  older reports, and commits effective availability and applied-order history.
- Entity availability batches, authoritative report-generation reads, effective
  availability, and retry receipts commit atomically. Future-generation reports
  wait for the local projection without being discarded by older delivery.
- Command creation reads current ownership and control eligibility in its write
  transaction before recording the attempt.
- Metadata PATCH loads current naming and enablement and commits all requested
  changes together. Adapter enablement uses the same Entity mutation workflow,
  retaining runtime fencing before Entity lookup and ownership checks.

Database `name` columns retain the latest Adapter name. Read queries derive the
display name from nullable household `name_override` metadata. Registration never
writes overrides. Reset follows the latest Adapter name; same-text saves pin an
explicit override. No-op metadata edits preserve timestamps and emit no Device
Facts. Migration 8 downgrade discards overrides. Back up the database before
downgrading.

Pure policy functions may be called by persistence, but transaction-dependent
rules must use values loaded inside that transaction, not preliminary Service
reads. Post-commit notifications wake Command waiters and the Device Fact relay;
they do not replace durable records or publish inside the transaction.

Current availability reads use only Devices-owned effective rows. They may lag
behind authoritative Adapter health. Commands and report admission retain their
transaction-local authoritative fencing and health checks. No transaction or
generated query row crosses the Adapters boundary. See the
[health projection operator guide](../../../docs/adapter-health-projection.md).

## Tests and generation

Domain unit tests stay with domain behavior. SQLite integration tests exercise
real migrated databases beside the persistence implementation. HTTP and NATS
tests stay with their transports and use the public persistence package when an
integration test needs SQLite.

`sqlite/dbqueries` is the source for `sqlite/dbsqlc`; root `sqlc.yaml` owns the
generation configuration. Run `mise run validate` after changes to regenerate,
format, tidy, and check the repository. Do not edit generated query code by hand.
