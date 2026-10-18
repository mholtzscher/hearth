# Adapter health projection over NATS

**Status:** Draft, awaiting approval alongside [Adapter-defined functions](adapter-functions.md).
This is the health-projection contract for that spec's prerequisite Adapters
module work. It does not authorize implementation.
This PR contains specifications only. Implementation will follow in separate PRs
under the parent spec's dependency order and acceptance gates.

## 1. Decision and changed guarantees

Adapters owns Adapter runtime/health writes. Devices owns Entity availability
reports, its local health projection, effective availability, and Entity
availability history. Adapters never writes Devices tables or calls Devices with
a SQL transaction. Devices consumes Core-originated health snapshots over durable
NATS JetStream and commits its own projection independently.

The user selected these semantics:

- Current Entity availability reads use only Devices-owned projection data.
  There is no fallback or immediate overlay from authoritative Adapter health.
  Availability can temporarily remain available after Adapter health is unhealthy.
- Entity availability history records effective changes when Devices applies
  them. Obsolete revisions are skipped. It is not a complete source-time mirror
  of every Adapter health transition.
- Command admission still checks authoritative Adapter health/runtime in its own
  existing transaction. Projection lag cannot override that check.

This deliberately replaces the current same-transaction health/availability
fan-out. It is not a behavior-preserving refactor. Public response shapes and
existing Adapter SDK wire contracts remain compatible, but current availability
and new history entries have the explicitly changed consistency semantics above.

No shared unit of work, transaction-in-context, database trigger, or exported
`*sql.Tx` projection interface is introduced.

## 2. Data flow and ownership

```text
Core Adapters transaction
  Runtime/current health + Adapter history, when changed
  Immutable current health snapshot and revision
  Health-update outbox record
  Commit
      |
Adapters-owned relay -> JetStream -> Devices-owned durable consumer
                                      Devices transaction
                                        Projected Adapter health/revision
                                        Eligible report invalidation
                                        Effective Entity availability/history
                                      Commit, then acknowledge
```

The external Adapter process continues to send the existing claim, heartbeat,
release, and Entity availability requests. No new SDK health method is required.
Health updates are published by Core's Adapters module, not by vendor processes.

Devices and notifications retain their existing transaction-local read access to
authoritative Adapter/runtime rows for control, report admission, and fencing.
NATS removes cross-module writes and shared transactions, not every read
dependency. Entity availability GET/list responses must not use those canonical
rows to hide projection lag.

## 3. Revision and immutable snapshots

Use an Adapter-scoped monotonic signed 64-bit revision, starting at one. It
continues across runtime replacement and Core restart. It is an ordering token,
not an identity or authorization credential.

Advance the revision for initial runtime claim and each Adapter health transition
already recorded under the current status/reason rules. Source-only heartbeat
evidence refreshes do not advance it or publish updates. A new runtime's initial
claim always establishes a new revision even if the status/reason matches the
previous runtime. Idempotent claim/release retries create no new revision.

```go
// internal/modules/adapters/health.go
type HealthRevision int64
type HealthUpdateID string // ahu_<UUIDv7>, one notification/publication identity

type HealthSnapshot struct {
    AdapterID        string
    RuntimeID        *RuntimeID
    Revision         HealthRevision
    Status           AdapterHealthStatus
    Source           string // core or adapter
    Reason           *HealthReason
    SourceObservedAt *time.Time
    ChangedAt        time.Time
    EvidenceAt       time.Time
}
```

The current snapshot is immutable for one `(adapter_id, revision)`, including
times and evidence fields. Store that snapshot separately from the mutable
current-health evidence, whose heartbeat timestamps may keep advancing. Otherwise
a recovery resend could give one revision two different meanings.

Snapshots include full state, not deltas. Devices may apply a higher revision
without receiving every intermediate one. `(adapter_id, revision)` plus the
normalized snapshot body is the semantic duplicate key. A fresh recovery
notification can have a different `ahu_` message ID for the same snapshot.
Fingerprint the validated snapshot with lowercase hexadecimal SHA-256 over
deterministic JSON field encoding, canonical UTC timestamps, and preserved
integer revision. Exclude message ID, envelope enqueue time, correlation,
causation, and tracing fields. Re-encoding whitespace
or using a fresh publication identity cannot turn a duplicate into a conflict.

Reason/status rules remain the existing ones. Healthy omits a reason and requires
a runtime ID. Unknown and unhealthy require a bounded namespaced reason; a
missing runtime ID is permitted only for non-healthy baseline evidence. Validate
existing slug/runtime syntax and UTC timestamps. Revisions are integers from one
through `9223372036854775807`; overflow rejects the mutation, never wraps.

`ChangedAt` records the original Core health change time; `EvidenceAt` and
`SourceObservedAt` are captured for this snapshot. They do not change on resend.
Entity availability based on projected health reflects this snapshot, not every
subsequent unchanged heartbeat. The Adapter management API still returns latest
authoritative evidence.

## 4. NATS contract

Use a separate internal contract, not the Device Facts namespace or stream:

```text
Subject: hearth.v1.core.adapter-health.<adapter_id>
Schema:  urn:hearth:schema:adapter-health-update:v1
File:    contracts/v1/adapter-health-update.schema.json
```

Add `ahu_` identity validation to `contracts/v1/common.schema.json` and register
the new schema in `contracts/v1/embed.go`. `natswire` owns subject construction,
parsing, and trace/envelope mechanics without importing either domain.

Strict envelope data shape:

```json
{
  "adapter_id": "zigbee-main",
  "runtime_id": "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "revision": 12,
  "status": "unhealthy",
  "source": "adapter",
  "reason_code": "adapter.hearth-adapter-zigbee2mqtt.bridge_offline",
  "source_observed_at": "2026-10-18T12:00:00Z",
  "changed_at": "2026-10-18T12:00:01Z",
  "evidence_at": "2026-10-18T12:00:01Z"
}
```

`runtime_id` is required but nullable for non-healthy baselines.
`source_observed_at` is optional and non-null when supplied. `reason_code` is
absent for healthy and required otherwise. Unknown properties reject. The
complete encoded message is at most 16 KiB. Match subject and payload Adapter.
The consumer also requires envelope ID and `Nats-Msg-Id` agreement. Broker access
remains trusted; this contract adds no signatures or new authorization boundary.
Messages contain bounded health metadata, never credentials or raw vendor data.

The standard envelope ID is the notification's `ahu_` ID and is also
`Nats-Msg-Id`. Envelope `emitted_at` is this notification's enqueue time.
Correlation comes from the runtime mutation, or is Core-generated for startup,
supervisor, and reconnect resynchronization. Causation is optional and, when
present, identifies the triggering existing request envelope. Store correlation,
causation, enqueue time, and W3C trace inputs with the outbox row. Retry that row
with identical identity, subject, payload, and headers, including after restart.

### Stream and consumer

| Setting | Contract |
| --- | --- |
| Stream | `HEARTH_ADAPTER_HEALTH_V1` |
| Subject filter | `hearth.v1.core.adapter-health.*` |
| Storage/retention | FileStorage, WorkQueuePolicy |
| Age | No automatic age expiry, `MaxAge = 0` |
| Capacity | One GiB, `DiscardNew`; no per-subject cap |
| Message size | 16 KiB |
| Duplicate window | Two minutes; consumers never depend on this for correctness |
| Durable consumer | `hearthd-device-availability-v1` |
| Delivery | DeliverAll, ReplayInstant, explicit acknowledgement |
| Pending/ack policy | One maximum pending acknowledgement, 30-second AckWait, unlimited delivery attempts |
| Filter | The complete stream subject filter |

WorkQueue retention is intentional. This is v1 internal delivery to one Devices
projection, not an externally supported fan-out feed. Acknowledged messages can
leave the broker because Adapter history and immutable heads remain in SQLite.
Unacknowledged messages do not silently age out; a full stream rejects new
publication, leaving the producer's outbox intact. A future second independent
consumer requires an explicit retention/contract decision, not an overlapping
consumer added to this work queue.

Provision and validate these resources before starting runtime transports and
public serving. Reject incompatible existing resources. Use the existing shared
Core NATS connection, JetStream context, `platformnats.Consumer`, and
`lifecycle.WorkerHandle`; no separate connection or generic event framework.
Adapters' NATS package owns the stream configuration. Devices' NATS package owns
its durable's configuration, recovery, and acknowledgement policy. Application
assembly passes the stream identity to Devices' provisioner; Devices does not
import the Adapters NATS package, and the producer does not own consumer policy.

## 5. Atomic enqueue and relay

In one Adapters-owned SQLite transaction:

1. Apply runtime/current-health changes and append Adapter history when required.
2. For a material transition, advance the health head and persist its immutable
   snapshot.
3. Mint one notification ID and append its full snapshot and publication metadata
   to the health outbox.
4. Commit, then optionally wake the relay with a nonblocking hint.

An ID-mint, snapshot, history, outbox, or commit failure rolls back only this
Adapters operation. It never changes Devices data. An unchanged heartbeat updates
its normal evidence/lease without generating health-update traffic.

The relay publishes pending records oldest-first and deletes a row only after a
PubAck naming `HEARTH_ADAPTER_HEALTH_V1`. Lost acknowledgements or delete failures
reuse the same row. Publication is at-least-once. Broker deduplication is bounded;
Devices must handle duplicates even after the broker window expires.

Transient publish/storage faults retain work and retry with bounded backoff from
100 milliseconds to five seconds. There is no hot loop. A deterministic corrupt
outbox row remains stored, logs a safe poison code, and fails relay readiness;
do not drop it to make readiness green. Do not turn an earlier operation-scoped
write error into a remembered module-wide admission fault.

### Recovery snapshot synchronization

On every Core startup, before runtime transports open, Adapters ensures each
existing Adapter has a health head and queues one fresh notification of its
current immutable head. A legacy Adapter with no head receives revision one from
its current persisted health, without inventing an Adapter history transition.
Normal claims of new Adapters create their first head in the claim transaction.

On Core NATS reconnect, a nonblocking wake asks the same relay worker to enqueue
fresh notifications for current heads again. Coalesce reconnect wakes. The
callback does not write SQLite or publish messages. Each resynchronization record
gets a new notification ID but the same revision/body. Retries of that record
reuse its ID. This avoids a broker duplicate window suppressing a needed current
snapshot after queue/consumer recovery.

Repeated snapshots are not new health transitions and must not advance revision,
alter Adapter history, or duplicate Entity history. They are safe projection
repair, not a retry of any function or external device action. Existing older
outbox rows remain and keep their own order.

This recovers current projection state after Core restart or reconnect without
requiring every original notification to survive. It does not reconstruct skipped
Entity history. Manual queue deletion while everything remains connected requires
an operator Core restart to initiate this resynchronization; it is not a supported
way to clear work. Restore SQLite and broker state consistently, or recreate this
internal stream/durable before starting a restored older database. Do not mix
unconsumed higher revisions from a newer database with a rolled-back database.

The relay's repository port is Adapters-owned and domain-only:

```go
// internal/modules/adapters/health_updates.go
type PendingHealthUpdate struct {
    ID            HealthUpdateID
    Snapshot      HealthSnapshot
    EnqueuedAt    time.Time
    CorrelationID string
    CausationID   *string
    TraceParent   string
    TraceState    string
}
type HealthPublicationStore interface {
    QueueCurrentHealthSnapshots(context.Context, time.Time) error
    OldestPendingHealthUpdate(context.Context) (PendingHealthUpdate, bool, error)
    DeleteHealthUpdate(context.Context, HealthUpdateID) error
}
```

`QueueCurrentHealthSnapshots` also initializes missing legacy heads in the same
Adapters-owned transaction. Its read/queue decisions must use that transaction's
current head values. Concurrent source mutations and reconnect wakes cannot
overwrite a newer head or restamp a revision's body. No Devices reference or SQL
transaction is part of the port.

## 6. Devices projection and report generations

The NATS consumer validates the envelope and maps to a Devices-owned plain input.
No SQL type crosses this boundary:

```go
// internal/modules/devices/adapter_health.go
type AdapterHealthUpdate struct {
    MessageID string
    Snapshot  adapters.HealthSnapshot
}
type HealthProjectionDisposition string // applied, duplicate, obsolete
type HealthProjectionResult struct {
    Disposition HealthProjectionDisposition
}
func (s *Service) ApplyAdapterHealth(context.Context, AdapterHealthUpdate) (HealthProjectionResult, error)

// Consumer-owned repository seam. Its implementation owns its transaction.
type HealthProjectionRepository interface {
    ApplyAdapterHealth(context.Context, AdapterHealthUpdate, time.Time) (HealthProjectionResult, error)
}
```

`devices/sqlite` atomically performs:

1. Load its last projected revision and normalized snapshot fingerprint.
2. A lower revision is obsolete. Equal revision/equal snapshot is a duplicate,
   even with a different notification ID. Both are acknowledged without mutation.
   Equal revision/different snapshot is a permanent `health_revision_conflict`.
3. For a higher revision, update the Devices-owned Adapter projection. Gaps are
   allowed because the snapshot is complete; never block waiting for a missing
   intermediate revision.
4. Invalidate explicit Entity report rows with an older health revision. Preserve
   reports from a newer revision, including fresh reports accepted after recovery
   while an older unhealthy update was still delayed.
5. Recompute effective availability for currently owned Entities using only this
   local projection and Devices-owned reports. Update effective current rows and
   append history only when effective status or reason changes. Preserve `since`
   for equivalent status/reason; source-only evidence can refresh without a new
   history transition.
6. Commit projection, invalidation, effective rows, and history together, then Ack.

Process owned Entities in lexical Entity-ID order for deterministic fan-out
within this Devices transaction. This creates no ordering guarantee with other
modules' commits. A failed commit leaves the projected revision unchanged and the
message unacknowledged. Redelivery cannot repeat already-committed history.

Invalid wire/route/identity input or a permanent revision conflict is terminated
with safe diagnostics and no projection write, so it cannot hold the durable's
only pending slot. Transient storage failure is negatively acknowledged after
one second for redelivery. Do not acknowledge success before SQLite commits or
create a global admission latch from a failed message.
Validate freely constructed domain updates again at the repository entry point.
Distinguish deterministic corrupt projection/report evidence from transient
storage failure; a classified permanent corruption is terminated with safe
diagnostics and no partial commit rather than endlessly blocking other Adapters.
Do not misclassify an ordinary write/commit failure as corrupt evidence.

### Fresh Entity reports

Entity availability report admission retains its existing active-runtime and
authoritative-healthy checks. In that same Devices-owned transaction, read the
Adapter's current health-head revision and stamp the accepted report with it.
This is a read of Adapters data, not a cross-module write. A report from before
health loss can never become eligible under a later healthy revision.

The SDK request format does not change. Existing request receipts remain
idempotent on their original request/fingerprint and do not restamp old reports
when retried after recovery. A genuinely fresh report uses a new request identity,
as today.

Report admission updates its report, recomputes effective availability using the
local projected health, and records only the resulting effective transition in
the same Devices transaction. A report from a revision ahead of the projection
is retained but is not yet eligible. A delayed older health message must not
delete it. Once the matching healthy snapshot is projected, it can become
available without another resend.

### Effective availability rules

| Devices-owned evidence | Effective availability |
| --- | --- |
| No projected Adapter health | Unknown, `hearth.awaiting_adapter_projection` |
| Projected Adapter unhealthy | Unavailable with the projected Adapter reason |
| Projected Adapter unknown | Unknown with the projected Adapter reason |
| Projected healthy and report runtime/revision matches | Report's available/unavailable value and reason |
| Projected healthy but report is ahead | Unknown, `hearth.awaiting_adapter_projection` |
| Projected healthy without an eligible report | Unknown, `hearth.awaiting_entity_report` |

Initial Entity registration creates its availability baseline from Devices'
local projection, never canonical Adapter health. Current GET/list reads use
Devices' effective current rows with an unknown fallback if projection evidence
has not arrived. They do not join `adapter_instances` for availability. Retain
all canonical fencing reads for Registration, Observations, Entity Events,
enablement, and control; changing a read model must not weaken those boundaries.

`entity_read_projection`, `entity_mapping.go`, and report/registration history
logic must be updated together. Existing command admission in `commands.go`
continues to read authoritative `adapter_instances` inside its own transaction.
Preserve its exact current eligibility rules, including active-runtime and
unhealthy checks. Do not accidentally route it through the eventual availability
projection or tighten unknown-health behavior as an unrelated change. Notification
and Function routing keep their respective existing canonical eligibility rules.
Recreate the view from its latest pre-feature definition, including name override
columns added by later migrations, not the original `00001` definition. Change
only its availability input/mapping. State projection and numeric fidelity remain
unchanged.

## 7. Current reads and applied-order history

The selected projection-only contract permits temporarily contradictory APIs:
`GET /v1/adapters/{id}` can show unhealthy while Entity availability still shows
available. A Command can therefore be rejected as `adapter_unhealthy` even when
the displayed Entity availability has not caught up. Document this explicitly.
There is no bounded catch-up latency guarantee or claim that readiness means
all projections are current.

For new Entity effective transitions, `observed_at` is Devices' Core application
time. Its existing receive-order sequence remains the tie-free ordering and
cursor position. Preserve the optional upstream `source_observed_at` only as
diagnostic evidence. Persist the source health revision/notification metadata
privately when needed for diagnosis, not as an alternate public ordering axis.

An obsolete message creates no current-state reversal or history entry. A jump
from revision 4 to 7 does not fabricate transitions for 5 and 6, nor attach them
retroactively to current ownership. Adapter health history remains the complete
authoritative record of committed Adapter health transitions. Entity availability
history describes applied effective changes for Entities owned when applied.

Keep existing HTTP/MCP paths, response fields, and cursor scope/encoding. Old
history remains untouched and keeps its original meaning. The operator guide
must identify the upgrade boundary; do not relabel old timestamps as processing
times or backfill a fictional applied timeline. Entity report processing also
records effective, rather than raw unmasked, availability transitions.

## 8. Persistence and migration

Use the next additive migration, currently
`00015_adapter_health_projection.sql`. Function tables follow in `00016` if those
numbers remain next at implementation time. Recheck numbering then.

| Table/change | Owner and required contents |
| --- | --- |
| `adapter_health_heads` | Adapters. Adapter ID primary key, current positive revision, immutable snapshot JSON and source tracing metadata. One retained head per Adapter. |
| `adapter_health_outbox` | Adapters. Monotonic enqueue order, unique `ahu_` notification ID, Adapter/revision, complete immutable snapshot, enqueue/correlation/causation/trace fields. No deletion before correct PubAck. |
| `device_adapter_health_projection` | Devices. Adapter ID primary key, last revision, snapshot JSON/fingerprint, last notification ID, and Core application time. |
| `entity_availability_current.health_revision` | Devices. Nonnegative integer revision stamped at report admission; existing runtime association remains. |
| `entity_availability_effective` | Devices. Entity ID primary key, effective status/source/reason, since/evidence/source times, and latest history position. This row contains no authoritative Adapter state. |
| Entity `health_transitions` metadata | Devices. Nullable source health revision/notification ID for new health-driven history; existing rows remain unchanged. |
| `entity_read_projection` view | Recreate against Devices-owned effective availability, not canonical Adapter health. Preserve its other Entity/State/name columns. |

Required SQL shape, with timestamp/identifier/body validation also enforced at
the independent module entry points:

```sql
CREATE TABLE adapter_health_heads (
    adapter_id TEXT PRIMARY KEY REFERENCES adapter_instances(adapter_id),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)
        AND json_type(snapshot_json) = 'object'),
    traceparent TEXT NOT NULL DEFAULT '',
    tracestate TEXT NOT NULL DEFAULT ''
);
CREATE TABLE adapter_health_outbox (
    enqueue_order INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id TEXT NOT NULL UNIQUE,
    adapter_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)
        AND json_type(snapshot_json) = 'object'),
    enqueued_at TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    traceparent TEXT NOT NULL DEFAULT '',
    tracestate TEXT NOT NULL DEFAULT ''
);
CREATE TABLE device_adapter_health_projection (
    adapter_id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json)
        AND json_type(snapshot_json) = 'object'),
    snapshot_fingerprint TEXT NOT NULL,
    last_message_id TEXT NOT NULL,
    applied_at TEXT NOT NULL
);
ALTER TABLE entity_availability_current ADD COLUMN health_revision
    INTEGER NOT NULL DEFAULT 0 CHECK (health_revision >= 0);
ALTER TABLE health_transitions ADD COLUMN source_health_revision
    INTEGER CHECK (source_health_revision >= 1);
ALTER TABLE health_transitions ADD COLUMN source_health_update_id TEXT;
CREATE TABLE entity_availability_effective (
    entity_id TEXT PRIMARY KEY REFERENCES entities(id),
    status TEXT NOT NULL CHECK (status IN ('unknown','available','unavailable')),
    source TEXT NOT NULL CHECK (source IN ('core','entity_report','adapter_health')),
    reason_code TEXT,
    current_since TEXT NOT NULL,
    evidence_at TEXT NOT NULL,
    source_observed_at TEXT,
    latest_transition_receive_order INTEGER REFERENCES health_transitions(receive_order)
);
```

The pending outbox must not reference a mutable head revision by foreign key;
older pending publications retain their complete original snapshot. The Devices
projection has no foreign key to Adapters' mutable head. Equal revision/body
checks, body size limits, namespaced reasons, and health-derived history metadata
consistency are module-boundary responsibilities, not assumptions supplied by a
caller. Keep canonical timestamp formatting and schema-backed decode on reads.

Use private SQLite DTOs and module-owned queries, not generated rows across a
module interface. Heads/outbox belong in Adapters queries; projected/effective
state/report generations/history belong in Devices queries. Keep both sqlc
targets against the existing shared migration directory. No module writes the
other module's tables at runtime.

Migration does not rewrite or clear existing health history. Backfill existing
explicit reports to revision one only when their runtime matches the current
healthy Adapter runtime; leave other reports at zero. The startup bootstrap
establishes revision-one heads for legacy Adapters and publishes them. Before the
first snapshot is projected, current availability may be unknown. Matching legacy
reports survive that baseline so an otherwise healthy household does not require
an SDK resend solely because of this upgrade.

Do not seed a second ongoing canonical-health overlay or invent historical
projection events during migration. Projection bootstrap initializes effective
rows and reuses an equivalent latest retained Entity history transition where
appropriate; it does not duplicate that entry merely to initialize the cache.
Existing history cursors remain usable because retained rows and receive orders
are unchanged.

Health history and current heads/projections remain retained indefinitely under
existing health retention policy. Unpublished outbox records are executable
publication evidence and are never age-pruned. Acknowledged broker messages can
be removed by WorkQueue retention. Prune no active publication work via the new
Function invocation retention task.

Down restores the old view/schema and drops new projection/publication data. It
cannot recreate the old same-transaction history semantics. Binary rollback
requires a consistent pre-feature database and broker strategy; a destructive
Down is not a lossless rollback.

## 9. Assembly, lifecycle, and checks

Construct Adapters and Devices repositories independently with the shared DB
handle. Neither accepts the other repository or a transaction projector. Expose
the Devices domain handler only to its health-update NATS consumer. The existing
health supervisor now invokes Adapters as the only lease-expiry authority.

At startup, provision stream/durable, bootstrap/queue heads, then start relay and
consumer before opening runtime/public transports. Reads may serve persisted
projection state while backlog catches up; do not gate readiness on an empty
outbox or a zero consumer backlog. Resource compatibility, worker/consumer
activity, and current dependency health do participate in readiness. A persistent
broker-capacity rejection is visible as current relay health, not lost work.

Do not feed these new derived-delivery checks into the health supervisor's
readiness argument. Preserve its existing base readiness checker and recovery
grace. Compose health relay/consumer/resource checks separately for public
`/readyz`. A stopped consumer, full health stream, or corrupt publication must
not prevent canonical lease expiry while the original source dependencies are
usable. Otherwise projection failure would prevent the source from declaring a
dead Adapter unhealthy. This requires explicit application wiring, not a second
supervisor or a remembered admission latch.

For shutdown, first drain existing admitted public work under the main spec.
Keep health ingress available while Adapter-dependent work needs it. Then stop
the health supervisor and close/join claim/heartbeat/release transports, so new
health mutations cannot race relay drain. Stop reconnect snapshot admission,
drain the health relay for up to five seconds, cancel/join the relay if needed,
then drain/stop and join the health consumer before SQLite/NATS close. Pending
outbox rows and unacknowledged messages remain for recovery. A wait timeout alone
does not establish worker completion. Preserve existing Device Fact, Observation,
Entity Event, Automation, and notification dependency ordering.

Primary verification belongs at actual SQLite and NATS boundaries:

- Force an outbox insert failure and prove the Adapter mutation rolls back while
  Devices data is untouched. A later independent heartbeat can succeed.
- Lose PubAck/delete evidence and restart the relay. One semantic revision is
  applied despite repeat publications and different snapshot-sync notification IDs.
- Fail the Devices projection transaction before Ack and prove replay creates
  exactly one effective/history update after commit succeeds.
- Pause the consumer after an unhealthy Adapter commit. Adapter API changes and
  Command admission rejects immediately; current Entity availability stays at
  its old projected value until delivery. This is an intentional behavior test.
- Make health delivery unhealthy while base dependencies stay usable. Advance
  the existing supervisor clock and prove lease expiry still commits and Command
  admission rejects; public readiness can fail without suspending expiry.
- Deliver revision 7 before 6, and deliver 6 after a fresh report stamped 7.
  Older input cannot erase that report or reverse the current projection/history.
- Exercise a report ahead of projection, unhealthy/healthy recovery, unchanged
  source evidence, runtime replacement, and a duplicate report receipt after
  revision advance.
- Restart Core with an active runtime, restore broker connectivity after missing
  delivery, and repeat snapshot synchronization. Current projection converges
  without revision advancement or synthetic historical transitions.
- Upgrade an existing DB. Preserve IDs, old history/cursors, and eligible legacy
  reports; observe the documented unknown interval before initial projection.
- Fill the health stream under a paused consumer. Rejected publication remains
  in the outbox and drains after capacity returns; no unconsumed update ages out.

Use the focused mise commands and acceptance mapping in the parent spec. Tests
must use real migrated SQLite, embedded NATS/JetStream, public service/transport
entry points, and deterministic barriers, not a stub that supplies the transaction
or delivery behavior being asserted. No live household or model API is needed.
The capacity test may use a small controlled stream with the same WorkQueue and
DiscardNew policy, while separate provisioning conformance tests pin production
limits. Do not add production test-only size knobs or write a GiB merely to
exercise rejection behavior.
