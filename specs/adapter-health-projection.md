# Adapter health projection over NATS

**Status:** Approved for implementation by explicit request on 2026-10-18.
This contract covers the prerequisite Adapters health foundation, R1, H1, R2,
H2, and R3, in [Adapter-defined functions](adapter-functions.md). Function
deliverables remain separate and must wait for the foundation's acceptance gate.
Implementation is reviewed separately from the specifications-only proposal.

Implementation must satisfy [the coding standards](../CODING_STANDARDS.md) and
the parent spec's actual-file structural review gate. Section 6 names validation
entry points; section 9 compares the proposed lifecycle to existing machinery.

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
Compare canonical snapshot JSON bytes directly, without a separate digest or
fingerprint column. Canonical encoding uses the wire snapshot field names,
lexically sorted object keys, and no insignificant whitespace. Encode revision
as an exact signed 64-bit integer, never through `float64`. Format parsed times
as UTC RFC3339Nano, keep required nullable `runtime_id`, and omit optional fields
when absent under the schema rules. Exclude message ID, envelope enqueue time,
correlation, causation, and tracing fields.

The receiving repository validates and canonicalizes both the stored and incoming
snapshot, then reuses those bytes for comparison and persistence within that
operation. Object key order, whitespace, equivalent timestamp spellings, or a
fresh publication identity cannot turn a duplicate into a conflict. Invalid
stored snapshot data is corrupt evidence, not an empty snapshot or a duplicate.

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
Correlation comes from the runtime mutation, or is Core-generated for lease
expiry and startup synchronization. Causation is optional and, when
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

The module binds its repository and wire mapping to the shared
[`outbox.Relay`](../internal/platform/outbox/relay.go), following the
[outbox delivery contract](../docs/outbox-relays.md). Each relay reads ordered
batches of at most 64, polls every five seconds when empty, and bounds each read,
publish, and delete to five seconds. Transient publish/storage faults retain work
and retry after a fixed one-second delay. They fail public readiness until delivery
recovers without suspending canonical Adapter lease expiry. There is no hot loop.
A deterministic corrupt outbox row remains stored, logs a safe poison code, and
stops that relay after publishing any valid older prefix. Preserve the corrupt row
and its successors; do not drop it to make readiness green. Do not turn an earlier
operation-scoped write error into a remembered module-wide admission fault.

### Recovery snapshot synchronization

On every Core startup, before runtime transports open, Adapters ensures each
existing Adapter has a health head and queues one fresh notification of its
current immutable head. A legacy Adapter with no head receives revision one from
its current persisted health, without inventing an Adapter history transition.
Normal claims of new Adapters create their first head in the claim transaction.

Core NATS reconnect resumes pending outbox publication and durable consumption
without queuing fresh snapshots. The relay only publishes existing outbox work;
it has no reconnect synchronization callback or admission state. Each startup
resynchronization record gets a new notification ID but the same revision/body.
Retries of that record reuse its ID. This avoids a broker duplicate window
suppressing a needed current snapshot after queue/consumer recovery.

Repeated snapshots are not new health transitions and must not advance revision,
alter Adapter history, or duplicate Entity history. They are safe projection
repair, not a retry of any function or external device action. Existing older
outbox rows remain and keep their own order.

This recovers current projection state after Core restart without
requiring every original notification to survive. It does not reconstruct skipped
Entity history. Manual delivery-state deletion while Core remains running requires
an operator Core restart to initiate this resynchronization, even after NATS
reconnects. Queue deletion is not a supported way to clear work. Restore SQLite
and broker state consistently, or recreate this internal stream/durable before
starting a restored older database. Do not mix
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
    ListPendingHealthUpdates(context.Context, int) ([]PendingHealthUpdate, error)
    DeleteHealthUpdate(context.Context, HealthUpdateID) error
}
```

`ListPendingHealthUpdates` requires a positive limit and returns oldest-first work.
A corrupt row returns the valid older prefix together with
`ErrCorruptHealthEvidence`; other read failures return no prefix. The shared relay
publishes that valid prefix before stopping on the corrupt row. An empty successful
read means no pending work, not corrupt evidence.

Application assembly calls the concrete repository's `QueueCurrentHealthSnapshots`
before starting the relay or opening runtime ingress. It also initializes missing
legacy heads in the same Adapters-owned transaction. Its read/queue decisions must
use that transaction's current head values. Concurrent source mutations cannot
overwrite a newer head or restamp a revision's body. No Devices reference or SQL
transaction is part of the relay port.

These are independently callable repository operations. Validate the supplied
UTC enqueue time in `QueueCurrentHealthSnapshots` and canonical `ahu_` identity
in `DeleteHealthUpdate`; a valid absent ID is an idempotent delete. Validate and
decode retained head/outbox evidence on reads rather than returning corrupt rows
as an empty pending set. The runtime write transaction builds and validates its
snapshot once, then reuses its canonical encoding for the head and outbox row.
Resynchronization uses the retained immutable body, not mutable heartbeat values.

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

### Validation responsibilities

| Entry point and accepted input | Structural preparation | Transactional or transport decision |
| --- | --- | --- |
| Devices NATS callback accepts bytes, subject, and headers | Bound message size; validate strict envelope/schema, UTC times, route/payload agreement, envelope/header identity, and trace metadata. Map once to `AdapterHealthUpdate`. | Terminate invalid wire input before a domain call; classify Ack/Term/Nak for valid domain input from the operation's result. |
| `Service.ApplyAdapterHealth` accepts freely constructed `AdapterHealthUpdate` | Validate message ID and snapshot IDs, revision range, status/source/reason combinations, and UTC times. Copy optional runtime/reason/time values and reuse the normalized update within the service call. | The service does not decide duplicate/obsolete status from a preliminary projection read. |
| Repository `ApplyAdapterHealth` accepts freely constructed update and application time | Independently enforce the same domain input rules and valid UTC application time. Prepare canonical snapshot bytes once for each input within this repository operation. Do not trust a transport or service caller's claim of validation. | Read the stored projection, reports, and current ownership in the write transaction; compare revisions and canonical snapshot bytes and apply all effects atomically. |
| Adapters runtime mutation accepts a freely constructed runtime write | Enforce the runtime input responsibilities in parent section 6.4, then validate the newly derived health snapshot before encoding it. | Check runtime/transition/lease rules, advance the head, and enqueue under the same Adapters transaction. |
| Health relay consumes `PendingHealthUpdate` returned by its store | Validate stored IDs/body/metadata and prepare one bounded envelope/header encoding for that publication attempt. A deterministic invalid row faults delivery and remains stored. | Delete only the corresponding notification after PubAck from the expected stream. Transient failures do not erase work. |

Use Adapters-owned pure validation for `HealthSnapshot`; Devices imports that
domain contract rather than defining an incompatible snapshot validator. Devices
owns validation/preparation of its message/application input and effective
availability policy. Share these pure rules across independent boundaries, but
do not normalize/encode again in each SQL helper within one operation. No
exported prepared type is needed solely to transfer this small freely callable
update. Canonical comparison bytes are prepared by the receiving boundary, never
trusted as a separate producer-supplied equality token.

Use a name such as `prepareAdapterHealthUpdate` for the private operation that
accepts arbitrary update values and returns owned normalized data. Transactional
application helpers consume that preparation; they do not each repeat its
normalization or accept unverified comparison bytes.

Each validator must have a production caller at its stated boundary. Verify
invalid freely constructed inputs through both service and concrete repository
entry points, not only through schema decoding. Moving a validator must preserve
its production enforcement and safe domain error mapping. Fixture assertions and
test-only conversions remain in `_test.go` files.

`devices/sqlite` atomically performs:

1. Load its last projected revision and validate/prepare the stored snapshot's
   canonical bytes.
2. A lower revision is obsolete. At equal revision, equal canonical snapshot
   bytes mean a duplicate, even with a different notification ID. Both are
   acknowledged without mutation. Equal revision/different canonical bytes is a
   permanent `health_revision_conflict`.
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
| `device_adapter_health_projection` | Devices. Adapter ID primary key, last revision, canonical snapshot JSON, last notification ID, and Core application time. |
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
health mutations cannot race relay drain. Drain the health relay for up to five
seconds, cancel/join the relay if needed, then drain/stop and join the health
consumer before SQLite/NATS close. Pending
outbox rows and unacknowledged messages remain for recovery. A wait timeout alone
does not establish worker completion. Preserve existing Device Fact, Observation,
Entity Event, Automation, and notification dependency ordering.

### Lifecycle and transport comparisons

Start from [the platform guide](../docs/platform.md) and preserve these concrete
contracts rather than copying only their field names:

| Proposed responsibility | Existing counterpart | Reuse and deliberate difference |
| --- | --- | --- |
| Single health publisher and pending-row drain | `outbox.Relay` in `internal/platform/outbox/relay.go`; `DeviceFactRelay` in `internal/modules/devices/nats/device_fact_relay.go`; `lifecycle.WorkerHandle` | Reuse the shared relay for ordered batches, nonblocking wake hints, polling, fixed one-second retry, bounded I/O, PubAck-before-delete, delivery faults, poison preservation, and a single publisher during drain. Startup assembly queues immutable health snapshots before starting the relay. Adapters retains its outbox, snapshot mapping, poison classification, diagnostics, and WorkQueue provisioning. Device Facts retains its own storage, retention, and fact DTOs. |
| Durable health consumer | `StartEntityEventConsumer` in `internal/modules/devices/nats/entity_event.go`; `platformnats.StartConsumer` | Reuse callback tracking, lifecycle activity, hooks, and joined shutdown. Devices owns health schema/route mapping, revision dispositions, and Ack/Term/delayed-Nak policy. Health differs in its full snapshots, report generations, and WorkQueue retention. |
| Extracted claim/heartbeat/release ingress | `internal/modules/devices/nats/session.go`; `platformnats.RequestReplyServer` | Move the runtime callbacks to Adapters while retaining activation, envelope, trace, and callback-join mechanics. Adapters owns runtime validation/health decisions. Nonblocking `Drain()` must be followed by `Close(ctx)` to join admitted callbacks. |
| Health expiry and derived readiness | `internal/app/hearthd/health_supervisor.go` and `runtime_readiness.go` | Keep the existing supervisor worker and base recovery grace. Application assembly adds separate public delivery checks; a derived failure cannot cancel or gate canonical expiry. |

Both relay instances use the shared five-second poll to rediscover pending rows
even if a post-commit wake hint is lost. Each read, publish/PubAck wait, and delete
has a five-second bound. Drain asks the original worker to publish until empty;
it never starts a replacement publisher. A drain deadline cancels the worker,
but application shutdown must still join it before closing its dependencies.

Give health-consumer callbacks a lifecycle context detached from early process
or worker-dependency cancellation, following existing device ingestion. Cancel
that context only after delivery has stopped and callbacks/hooks have joined.
`Consumer.Active()` reports lifecycle activity, not broker resource health.
`Consumer.Stop()` requests stopping without joining, and `Drain(ctx)` joins but
does not prove completion on timeout. First Stop/Drain wins; Stop cannot undo a
Drain already in progress. Readiness and cleanup must observe these distinctions.
The shared Consumer does not restart a terminated subscription. Reconnect resumes
an active consumer's durable work; it does not queue snapshots or restart a
terminated consumer. Report that lifecycle failure through derived readiness
and require Core restart rather than silently adding a retry supervisor.

The parent section 11 owns the implementation inventory, test locality,
generated-source review, and navigation checks for both specs. In particular,
review Adapters head/outbox queries and Devices projection/report/view queries as
separate write owners even though both sqlc targets use shared migrations.

Primary verification belongs at actual SQLite and NATS boundaries:

- Force an outbox insert failure and prove the Adapter mutation rolls back while
  Devices data is untouched. A later independent heartbeat can succeed.
- Submit invalid freely constructed updates directly to service/repository
  boundaries. Neither may write projection/history or depend on a wire validator
  having run. Verify that one normalized snapshot drives duplicate comparison
  and application without a caller-controlled equality token.
- Deliver equal-revision snapshots with different key order, whitespace,
  equivalent UTC timestamp spellings, and notification IDs. Canonical byte
  comparison must recognize duplicates without new history. Change a semantic
  snapshot field at the same revision and verify `health_revision_conflict` with
  no projection/history mutation. Corrupt stored JSON must not count as a duplicate.
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
- Lose a post-commit wake hint and prove periodic outbox rediscovery still
  publishes. Block a publication/callback during shutdown and prove no second
  publisher or dependency closure races the unfinished work.

Use the focused mise commands and acceptance mapping in the parent spec. Tests
must use real migrated SQLite, embedded NATS/JetStream, public service/transport
entry points, and deterministic barriers, not a stub that supplies the transaction
or delivery behavior being asserted. No live household or model API is needed.
The capacity test may use a small controlled stream with the same WorkQueue and
DiscardNew policy, while separate provisioning conformance tests pin production
limits. Do not add production test-only size knobs or write a GiB merely to
exercise rejection behavior.
