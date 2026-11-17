# Architecture

**Status:** Evolving; only constraints backed by accepted decisions are stated here.

Hearth serves one trusted household. It is intended to replace Home Assistant
in operation, but may depend on specialist protocol services. The disposable
Home Assistant migration Adapter has been removed; native companions connect
directly to their upstream systems. `hearthd` is the Core executable, while
Hearth is the product name and `hearth-` names companion processes. The planned
management CLI is `hearth`. The [glossary](../../GLOSSARY.md) defines the
domain language used here.

## Accepted constraints

### Ownership and boundaries

Core owns the household model and durable evidence. Stateless Adapter processes
translate external systems into Hearth's language-neutral contracts. Vendor
payloads, credentials, connections, and device-specific policies stay outside
Core. Canonical Device and Entity models must not depend on Home Assistant or
upstream protocol vocabulary. Adapter Functions may have opaque Adapter-defined
schemas and payloads; Core validates their structure without branching on
Function names or vendor fields.

The `adapters` module owns runtime identity, canonical Adapter health, Session
ingress, Function catalogs and Invocation evidence. `devices` owns Devices,
Entities, Bindings, State, Commands, Entity Events, availability and Device
Facts. Devices may depend on Adapter domain contracts, not the reverse.
`automations` owns definitions, Runs, admission and execution evidence.
`notifications` owns destinations and immediate Notification Attempts.
`dashboards` owns validated view definitions. The Agent owns conversation
history and turns. Each module owns its persistence and domain policy.

Modules use independent SQLite transactions and generated query types against
shared migrations; neither transactions nor generated rows cross module APIs.
Narrow transaction-local reads of Adapter runtime and health support fencing and
control without transferring ownership. Assembly constructs repositories and
owns shared NATS, readiness, supervision, and process lifecycle. Shared transport
mechanics do not decide module-specific failure policy. See the
[platform guide](../reference/platform.md), [service input contracts](../reference/service-input-contracts.md),
and [Automation ownership](../../internal/modules/automations/README.md).

Go, NATS, SQLite, Echo and Huma form the Core implementation stack; the
[module manifest](../../go.mod) lists its dependencies. Wire contracts remain
language-neutral. JSON Schemas and Entity-type manifests are
authoritative for support, State, parameters, operation deadlines and outcome
rules. The build derives the Core catalog, scalar State fields, and typed Go SDK
facades from those definitions; concrete Entity types are not handwritten Go.
See [Entity types](../../entitytypes/) and [wire schemas](../../contracts/v1/).

### Identity and Adapter Sessions

A Device groups independently addressable Entities. Hearth assigns stable
canonical IDs; names and external IDs are metadata or mappings, not identity.
An Adapter-scoped stable Binding key preserves identity when an external ID
changes. Ambiguous conflicts require reconciliation. Registration commits the
Binding, Device, submitted Entities and mappings atomically before returning IDs.
It is additive: omitted Entities stay unchanged. Entity type and established
unit interpretation cannot change on re-registration. A quantity kind can become
known once, but cannot then change or disappear. Invalid interpretation rejects
the whole batch without changing State or history. See
[registration](../../internal/modules/devices/sqlite/registration.go) and
[units](../reference/units.md).

Each Adapter instance has one active runtime. The SDK retries a claim with the
same generated runtime ID; Core persists it idempotently. Graceful release or
supervisor-owned lease expiry permits takeover, but a new process cannot evict an
active runtime. Only the supervisor expires leases, so an active runtime may
renew an elapsed lease before expiry commits. Runtime-scoped subjects fence
subsequent writes and Commands. The runtime ID is neither the stable Adapter
identity nor an authentication credential. Fencing terminates the SDK Session.
The SDK can read only its Adapter's persisted mappings in non-snapshot keyset
pages, not other Adapter metadata or State.

The SDK owns Session claim, heartbeat, transient registration retry and release.
It provides typed registration, reporting and Command handling without owning
durable state, a local outbox, or availability and mapping caches. A successful
one-shot Command acceptance grants the only capability that can publish
Command-linked Observations. Its deadline cannot exceed the Command deadline,
and returning from the handler does not cancel it. Handler calls can overlap,
including on one Entity; Adapters must be concurrency-safe and may serialize
when their upstream requires it.

Version 1 uses versioned schema-validated envelopes, runtime-scoped subjects,
UTC timestamps and W3C trace context. Core and the SDK validate the same embedded
schemas. The runtime-scoped route cutover is incompatible with the former
stable-slug-only routes: Core, contracts, SDK, first-party Adapters and NATS
resources must be deployed together; mixed versions are unsupported.

### Observation and State

An Observation is an Adapter's value report, not proof of physical truth.
Core records each first-seen report and its disposition transactionally.
Accepted reports advance State in Core receive order, even when the value is
unchanged; diagnostic source times do not determine ordering. Current support
validates incoming values, but equality does not reinterpret stored State under
possibly narrowed support. Rejected history retains a code, not the raw value.
A never-observed Entity has null State. The Observation backing current State
remains retained until superseded.

Observation ingress uses a bounded, file-backed JetStream stream and a durable
consumer. A committed applied, unchanged, duplicate or rejected report is
acknowledged after the transaction. Permanent wire-invalid input is diagnosed and
acknowledged; infrastructure failures remain eligible for redelivery. Duplicate
identities do not project State twice. Raw wire-invalid input remains in the
bounded Observation stream until retention eviction. Core-offline
protection requires broker acknowledgement, a running Adapter, available
JetStream and remaining stream capacity. It does not guarantee recovery of
unpublished input. The [stream configuration](../../internal/modules/devices/nats/jetstream.go)
owns the retention and consumer settings. The SQLite Observation retention
floor exceeds the stream's age limit so retained redeliveries remain
deduplicated; the current-State anchor survives longer when necessary. See
[history pruning](../../internal/modules/devices/history_pruning.go).

### Entity Event durable history

An Entity Event reports a named occurrence, not State or a Command outcome.
Acceptance means the report passed processing-time checks, not that it proves
physical truth or causation. Core records accepted and rejected reports without
changing State, Commands, Bindings, enablement, health or availability. Event
identities survive publication retries; broker acknowledgement is not Core
acceptance. First-recorded evidence wins on duplicate or conflicting input.
A redelivered event ID identifies the same occurrence; a new ID identifies a new
occurrence even when its name repeats.
The independent Event stream retains reports for seven days or one GiB,
whichever limit is reached first. SQLite retains Event history and duplicate
protection for 30 days from Core record time. History includes accepted and
rejected reports; rejections for unknown Entities have no browse route. See
the [Event retention policy](../../internal/modules/devices/entity_events.go)
and [stream configuration](../../internal/modules/devices/nats/entity_event_resources.go).

History exposes the reported name, disposition, rejection code, and diagnostic
timestamps, never Adapter/runtime/correlation IDs, fingerprint, receive order,
or raw envelopes.

Observation and Entity Event consumers deliberately have different failure
policies. In particular, an Observation projection error can block later
reports, whereas an uninterpretable Event-source descriptor terminates that
report without a history row. Other Event storage failures are negatively
acknowledged for delayed redelivery. Non-event types reject an Event before
decoding support, even if that support is malformed. See
[ingress disposition contracts](../reference/ingress-disposition-contracts.md)
and the [Event implementation](../../internal/modules/devices/entity_events.go).

### Device Fact delivery and recovery

The Devices module atomically queues a Device Fact with each first-seen accepted
Observation or Entity Event. Rejections, duplicates, identity conflicts and
Command status transitions create no Fact. A linked Observation that satisfies
a Command still creates its own Observation Fact. The outbox relay publishes
in enqueue order with stable identity and deletes only after the expected broker
acknowledgement. Publication is at-least-once, and broker deduplication is
bounded to two hours, so consumers must be idempotent. The Fact stream retains
seven days or one GiB, whichever limit is reached first, and evicts oldest
messages. A consumer that falls behind can permanently miss Facts. There is no
global Fact order. Consumers own their recovery: a named durable resumes its
acknowledgement floor, while a new `DeliverNew` consumer sees only future Facts.
Pending outbox rows survive Core restart with their existing identities; a poison
row remains pending and fails readiness. Command history, not Observation Facts,
is authoritative for Command lifecycle. See [durable outbox relays](outbox-relays.md) and
[ADR 0020](../adr/0020-durable-device-facts-via-outbox.md).

Facts are unsigned and carry no security guarantee. Anyone with broker access
can read State values, forge Facts, or publish into or purge the Fact stream.
The intended future Core-only publishing policy is not an existing access
control. HTTP cursors similarly identify positions, not authority.

### Canonical read projections

Core stores canonical values and rollups; household units are a read-time
projection and never alter writes, raw audit history or Facts. Detail and
aggregate reads return canonical and display values from the same evidence.
Forecasts are snapshots, not current measurements, and do not enter the scalar
history catalog. See [read units](../reference/units.md),
[history queries](../reference/history-queries.md), and
[State changes](../reference/state-changes.md).
The original State-history schema changed migration 00001 and required
recreating development databases; later additive migrations have their own
upgrade contracts.

### Health, availability and enablement

Adapter health combines runtime and configured-external-system evidence.
Devices independently projects health snapshots and Entity availability. There
is no cross-module write or shared transaction. An availability report is
eligible only against its matching healthy projection; a report ahead of that
projection is retained until it becomes eligible. Consequently an Entity can
still appear available while authoritative health is already unhealthy.
Commands use authoritative health, not projection freshness. Snapshot duplicates
and gaps do not invent history transitions. See
[Adapter health projection](adapter-health-projection.md).

Enablement is a separate, reversible Core decision. New Entities default to
enabled; registration may set only their initial value. Management and the owning
Adapter may set enablement idempotently, with the last committed change taking
effect. A disabled Entity
retains its identity, metadata, history and last State. Core rejects new Commands
and ordinary Observations; only a matching linked Observation for an active
pre-disable Command may still update State and satisfy that Command. Disabled
event sources reject Events without such an exception. Availability reports can
still be made while disabled.

An unhealthy owner causes a new Command to fail before dispatch. Unknown health
is attempted only with an active runtime route; an unavailable Entity with a
healthy owner is also attempted. A fresh Adapter rejection may report
`entity_unavailable` without changing availability. Disablement takes precedence.

### Commands and other effects

Commands are immediate, deadline-bounded attempts, not desired-state queues.
Core persists `requested` before dispatch and continues an admitted lifecycle
even if the HTTP caller disconnects. Commands to one Entity may overlap and
have independent outcomes. Core dispatches even if current State already matches.

For an observed Operation, acceptance is not satisfaction: a linked
post-dispatch Observation must meet the pinned catalog outcome policy. Core
commits satisfaction with State projection. A dispatched Operation instead
completes on durable acceptance without inventing State. State may change again
immediately afterward. Startup marks unfinished Commands interrupted; it never
replays them. An Adapter's evidence is not proof of a physical effect.

Function Invocation admission pins the runtime, catalog revision, arguments and
deadline before at most one application request. It does not reroute, retry or
replay. Discovery remains visible offline, while calls require an active
non-ended runtime rather than healthy upstream evidence. Startup marks unfinished
Invocations unknown. See [Adapter Functions](../reference/adapter-functions.md).

Notification Attempts are immediate durable evidence, not queued sends.
Notifications uses Adapter Sessions but owns its own admission and persistence.
An Automation Notification Step reserves one attempt and advances only after
provider acceptance is retained, not after confirmed delivery or readership.
See [notifications](../reference/notifications.md).

Persistence failures are operation-scoped. A failed admission write prevents
external work; an uncertain outcome never authorizes replay. Independent future
operations may proceed after their own writes succeed. A past failure must not
permanently close module admission or fail readiness until restart. See
[ADR 0026](../adr/0026-scope-persistence-failures-to-operations.md) and
[persistence error contracts](../reference/persistence-error-contracts.md).

### Automation and Agent

Automations consume Device Facts through their own durable consumer, rather
than reading Adapter input or Devices tables. On first creation the consumer
starts at the stream tail; later it resumes its acknowledgement floor. Matching
receipts and Run-or-Skip admission commit atomically, with idempotence per Fact
and Automation. Facts older than the admission freshness limit produce a
`stale_fact` Skip rather than a delayed household action. An Automation has at
most one active Run; manual and SubAutomation invocation can admit disabled
definitions. There is no execution retry or crash replay. See
[ADR 0021](../adr/0021-drive-automations-from-device-facts.md).

Runs execute immutable definition snapshots. Admission Conditions use coherent
current State and three-valued logic. Reached branches evaluate fresh State and
record their decisions before effects; an unknown branch result fails rather
than selecting a fallback. Earlier effects are not undone. Nested Automations
admit separate Runs and the parent waits for durable child success; busy targets
fail rather than queue. Delay waits hold the Run active and restart interrupts
them. Function Steps pin a declared-contract fingerprint, permitting a new
Session with the same contract but failing changed contracts without dispatch.
Notification and Function Steps advance only on their retained success evidence.
See [Conditions](../reference/automation-conditions.md),
[branching](../guides/automation-branching.md),
[reusable Automations](../guides/reusable-automations.md),
[delays](../guides/automation-delay-steps.md),
[Function Steps](../guides/automation-function-steps.md), and
[schedules](../reference/automation-schedules.md).

The Agent is required in Core and uses the shared MCP tool catalog in memory.
It presents validated view drafts; saving or replacing a dashboard requires
explicit browser action, with revision checks for replacement. The dashboards
module owns saved definitions, while Devices owns field interpretation and
history queries. Artifacts remain in committed tool results; unsupported stored
content remains recoverable without automatic rewriting. Eino owns Agent runtime
and repair, while provider continuation metadata and native call identity remain
with their providers. A conversation pins its provider; switching requires a new
conversation. See [dashboards](../reference/dashboards.md) and
[Agent skills and continuation](../reference/agent-skills.md).

### Transport, recovery and shutdown

HTTP and MCP map their own contracts over shared domain services; neither
transport imports the other. Echo and Huma serve HTTP and runtime OpenAPI. MCP
is stateless Streamable HTTP on the same listener, with no separate process,
port or authentication. It validates advertised input schemas before handlers,
owns its result and error projections, and shares its catalog with the Agent.
See [MCP errors](../reference/mcp-errors.md) and the
[operation-local convention](../../CODING_STANDARDS.md#keep-transport-operations-in-operation-local-files).
HTTP uses Huma Problem Details; the
[Command HTTP operation](../../internal/modules/devices/httpapi/execute_entity_command.go) owns
its Command error statuses. Protocol boundaries preserve domain error identities
and safe fallbacks rather than sharing a universal transport policy.

HTTP is unauthenticated. Examples bind to loopback; non-loopback binding is an
explicit trusted-network operator choice. Untrusted exposure needs an
authentication and deployment decision. Each process owns non-secret
configuration; provider credentials stay in the companion's secret source or,
for the Agent, in typed model configuration loaded at startup, not serializable
Core configuration.

Readiness checks infrastructure, resources, consumers, schedulers, admission
and relay delivery health; it does not guarantee backlog exhaustion, projection
freshness or per-report progress. A relay fault fails public readiness but does
not suppress canonical lease expiry. Pending outbox rows survive restart.

Shutdown closes public admission and joins dependent work before withdrawing
resources. Agent turns are canceled and joined; Automation Runs join while their
reserved internal Function and Notification paths remain available. Core then
joins those effects and Commands, drains runtime ingress and health projection,
and drains Event and Observation ingestion while the Fact relay still publishes.
The relay drains before NATS and SQLite close. A timed-out wait is not evidence
that a worker exited. Unprocessed input remains broker-owned only within stream
limits. See [platform lifecycle](../reference/platform.md) and
[outbox shutdown](outbox-relays.md#readiness-and-shutdown).

### External system boundaries

First-party companions are stateless and use the SDK; their upstream systems
and specialist brokers remain operator-managed. Their detailed supported
capabilities, configuration, evidence rules and limitations belong in their
guides, not in Core's Entity model.

- [Zigbee2MQTT](../guides/zigbee2mqtt.md) uses MQTT separately from NATS.
  Health requires explicit availability, a reconciled inventory and proven
  non-optimistic global behavior. Commands serialize per Device; observed
  outcomes require fresh matching reports. Multi-property State must come from
  complete same-message evidence, not assumed freshness of cached siblings.
  The v1 MQTT boundary is unauthenticated plain TCP on a trusted network.
  The [capability guide](../guides/zigbee2mqtt-capabilities.md) owns the mapping.
- [Ecowitt](../guides/ecowitt.md) uses a private MQTT connection and secret
  PASSKEY for one configured station. Slot identities survive hardware
  replacement. Invalid measurements do not suppress valid siblings or become
  zero; fresh reports are needed to restore availability. Secrets and raw
  reports must not enter diagnostics.
- [Z-Wave JS](../guides/zwavejs.md) uses the operator-managed server and
  controller, not network management in Hearth. Node-slot identity can be
  reused by replacement hardware. Sleeping actuators are excluded because
  upstream could defer writes past the deadline. Fresh correlated polls, not
  potentially optimistic update echoes, provide linked evidence. A timed-out
  write may still have a physical effect and closes its connection generation.
- [ESPHome](../guides/esphome.md) defaults to encrypted Native API connections;
  plaintext requires explicit permission and encryption never falls back to it.
  A socket write permits acceptance, and a matching pushed or same-generation
  cached value can supply evidence. Neither proves a physical effect. Cover
  Stop can miss its deadline while broker acknowledgement is stalled and is not
  an emergency-stop guarantee. Ratgdo position and remote lockout do not prove
  physical closure or security. Physical multicast enrollment and long-running
  behavior remain unverified. See also [thermostats](../guides/esphome-thermostat.md).
- [August](../guides/august.md) is monitoring-only. Control remains blocked;
  separately authorized direct-response and timestamp checks on the intended
  firmware, implementation and a supervised trial are prerequisites. Cached State
  or HTTP success alone cannot establish availability recovery. Native Go framing
  and live push semantics remain unverified.
- [Levoit](../guides/levoit.md) is an explicit exception to measured outcome
  evidence: after cloud acknowledgement it publishes requested control values
  as linked optimistic Observations, without a confirmation read. They enter
  State, history and Automations and later polls can overwrite them with stale
  snapshots. It does not synthesize sensor readings or retry mutations. This
  exception does not apply to other Adapters.
- [Pirate Weather](../guides/pirateweather.md) uses periodic acquisition for
  health, availability and State; on-demand forecast Functions do not change
  State. Missing measurements do not become zero and forecasts are not current
  readings.
- [Discord](../guides/discord.md) owns its webhook credential and one bounded
  POST per invocation. Its health describes local readiness, not a provider
  probe.

## Undecided

Production supervision and deployment topology, native Adapter scope, and
later roadmap technology choices remain proposals until reviewed.
