# Devices module ownership

`devices` is one product module. Its packages separate delivery and persistence
from domain behavior; Commands, Observations, Adapter health, and Entity
availability are not independent product modules.

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

## Atomic operations

Keep these operations transactional when moving or changing code:

- Registration reconciles the Binding, Device, submitted Entities, external
  mappings, and initial availability together.
- Observation projection records disposition, advances canonical State, satisfies
  a matching linked Command, and queues the accepted Observation's Device Fact
  together.
- Entity Event recording establishes identity/disposition and queues an accepted
  event's Device Fact together, without changing State or Commands.
- Adapter runtime and health writes coordinate fencing, lease evidence, current
  health, availability invalidation, and effective availability transitions.
- Entity availability batches and their retry receipts commit atomically.
- Command creation reads current ownership and control eligibility in its write
  transaction before recording the attempt.

Pure policy functions may be called by persistence, but transaction-dependent
rules must use values loaded inside that transaction, not preliminary Service
reads. Post-commit notifications wake Command waiters and the Device Fact relay;
they do not replace durable records or publish inside the transaction.

## Tests and generation

Domain unit tests stay with domain behavior. SQLite integration tests exercise
real migrated databases beside the persistence implementation. HTTP and NATS
tests stay with their transports and use the public persistence package when an
integration test needs SQLite.

`sqlite/dbqueries` is the source for `sqlite/dbsqlc`; root `sqlc.yaml` owns the
generation configuration. Run `mise run validate` after changes to regenerate,
format, tidy, and check the repository. Do not edit generated query code by hand.
