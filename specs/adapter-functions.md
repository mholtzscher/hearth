# Adapter-defined functions

**Status:** Draft, awaiting approval. This document does not authorize implementation.
**Delivery:** This PR contains specifications only. Implementation will follow in
separate PRs, with the health foundation verified before Function work begins.
**Planning effort:** XL. Approximately 3 to 5 weeks of focused implementation and
verification, with low-to-medium confidence. This replaces the earlier estimates
because it now includes extracting Adapter ownership and replacing synchronous
availability fan-out with a durable NATS projection. Projection migration,
recovery, schema validation, and lifecycle integration are the largest uncertainties.

The linked [Adapter health projection contract](adapter-health-projection.md)
defines the health wire, storage, consistency, and recovery details. Review both
documents as one proposal. The function invocation contract remains in this file.

Implementation must satisfy [the coding standards](../CODING_STANDARDS.md).
The validation responsibilities in section 6.4 and lifecycle comparisons in
section 10 apply to the proposed files, not only to this documentation review.

## 1. Problem and agreed scope

Adapters need to expose capabilities that do not belong to an Entity type, such as
reloading their configuration, refreshing upstream data, running diagnostics, or
using a configured outbound connection. Adding an SDK method, Core endpoint, or
synthetic Entity for each such capability makes Adapter development depend on
Core releases.

Add **Adapter Functions**. An Adapter registers descriptions and input/output
schemas, implements the handlers, and owns their meaning and effects. Core owns
discovery, structural validation, runtime-scoped routing, bounded invocation,
and durable invocation evidence. New function names and parameter shapes require
no Core or SDK change within the supported contract.

The following decisions were settled during planning:

- Functions use bounded synchronous execution with durable evidence. There is no
  automatic retry or replay.
- MCP exposes fixed discovery and invocation tools, not a changing tool per function.
- Validated non-secret arguments and successful results remain in invocation
  history for thirty days.
- The first implementation includes Zigbee2MQTT `refresh_entity`.
- First establish an `adapters` product module and durable NATS health delivery
  to Devices. Preserve existing SDK/API shapes and control eligibility, but
  deliberately change Entity availability to an asynchronous Devices projection.
  Add functions after that foundation passes its acceptance gate. Do not create
  an independent `adapterfunctions` module or leave Adapter-wide ownership in Devices.
- Current availability reads use only the Devices projection and may lag behind
  authoritative Adapter health. There is no canonical-health read overlay.
- New Entity availability history is applied-order history, with obsolete
  revisions skipped rather than a reconstructed source-time timeline. Adapter
  health history remains authoritative for all committed health transitions.
- Configuration ownership, validation, modification, persistence, and reload
  behavior belong entirely to the Adapter that registers such a function.
  Core has no special `reload_config` interpretation or configuration editor.

### Non-goals

- Background function jobs, streaming results, progress events, cancellation
  APIs, or recovery/replay of function execution after restart. Durable health
  publication/projection retries are a different, explicitly included protocol.
- Automation Function Steps or new Device Fact families.
- A browser function editor, invocation form, or history page.
- A new authentication system, permission framework, outbound proxy, or arbitrary
  shell execution facility.
- Replacing Entity Operations, Commands, or the notifications module.
- Hot modification of a Session's function catalog. Changing its catalog requires
  a new Session/runtime in this version.
- Implementing config reload, config modification, or generic outbound networking.
  The only new function-specific behavior in this scope is the selected refresh.
- Automatically deriving schemas from Go types or adding a generic plugin framework.
- Renaming or moving the vendor implementations in `internal/adapters`, changing
  existing SDK APIs or wire contracts during extraction, splitting the shared
  database, or building a generic event bus/transaction framework. The health
  projection adds a new Core-only wire contract without changing SDK health input.

## 2. Existing architecture and integration points

The closest precedent is notifications, not Entity Commands:

| Existing owner | Evidence and reuse |
| --- | --- |
| `sdk/adapter/session.go`, `lifecycle.go` | One active fenced runtime, heartbeat, release, and Session-owned transport cleanup |
| `sdk/adapter/notifications.go` | Activate a non-Entity subscription before registering its route; serve concurrent bounded handlers and join their actual returns |
| `internal/modules/notifications/service.go` | Persist before external work, detach admitted work from caller cancellation, retain uncertain outcomes, and never replay |
| `internal/modules/notifications/sqlite/dbqueries/notifications.sql` | Transactionally read runtime tables, currently Devices-owned, without modifying their state |
| `internal/platform/nats/requestreply.go` | Registration-server envelope validation, callback admission, tracing, and joining |
| `internal/platform/lifecycle/admission_group.go` | Track admitted invocations and join them before withdrawing NATS or SQLite |
| `internal/modules/agent/mcp_tools.go` | The built-in agent lists tools once and records raw tool arguments and results |
| `internal/adapters/zigbee2mqtt/reconcile.go` | Existing per-Entity `GetProperties` and `publishGet` behavior |
| `internal/app/hearthd/history_pruning.go` | One shared startup/hourly pruning worker with module-owned retention |

The current Devices module owns Adapter runtime identity, health, and the
lease-expiry write path. The revised design first moves that ownership into
`internal/modules/adapters`, then adds function catalogs, invocation policy, and
history there. Notifications already demonstrates why Adapter lifecycle is not
Device-specific. A notification-only Adapter needs a Session without registering
any Device or Entity.

Devices retains Entity availability policy and its projection/history. Adapters
commits only its own state and health-update outbox. Devices applies the published
snapshots in its own transaction. This explicitly replaces same-transaction
fan-out, without SQL transaction types crossing module APIs or Adapters writing
Devices data.

### Alternatives

| Approach | Benefit | Cost | Decision |
| --- | --- | --- | --- |
| Add individual SDK methods and endpoints | Fully bespoke contracts | Every Adapter feature requires Core and SDK changes | Reject |
| Ephemeral generic RPC | Small implementation | Loses authoritative evidence around external effects | Reject |
| Schema-described functions with durable synchronous invocation | Extensible business behavior with bounded execution and diagnostic evidence | New validation, catalog, and history contracts | Select |
| Jobs and dynamically generated MCP tools | Direct per-function tool schemas and long-running work | Job recovery, catalog synchronization, and more lifecycle states | Defer |

The ownership alternatives were reconsidered during review:

| Ownership | Benefit | Cost | Decision |
| --- | --- | --- | --- |
| Add functions to Devices | Smallest immediate diff | Continues to put notification-only and management-only Adapter lifecycle under Devices | Reject for the revised scope |
| Separate adapterfunctions module | Isolates new function behavior | Keeps common Adapter lifecycle in another product module and adds another routing consumer | Replace |
| Extract adapters, then add functions there | Gives Adapter-wide behavior one owner reused by Devices and notifications | Requires an explicit replacement for the coupled health/availability writes | Select |

The selected replacement is NATS outbox/JetStream delivery to a Devices-owned
projection. Shared SQL transaction ports, Adapters writing Devices tables, a
cross-module unit of work, and database triggers were rejected. The cost is
explicit eventual consistency and new projection/recovery machinery.

## 3. Domain language and ownership

Proposed glossary additions, to be applied with the approved implementation:

**Adapter Function:** An Adapter-defined named capability outside the canonical
Entity Operation model. The Adapter defines its accepted arguments, result, and
behavior. A function is not an Entity Operation, Command, or MCP Tool.

**Function catalog:** The complete set of functions one Adapter runtime registers.
Its revision identifies one immutable catalog, including each function's schemas,
description, and execution deadline.

**Function Invocation:** One recorded attempt to execute one Adapter Function
against a selected runtime and catalog revision. Its record is evidence, not
queued or replayable work.

Core validates the advertised contract but does not interpret function-specific
fields or implement vendor policy. For example, a `reload_config` handler decides
which Adapter-owned source to read, how to validate it, and whether it can apply
the change. Core merely invokes the registered handler and records its result.

The current architecture rule that Core contains no vendor-specific payloads
needs a narrow clarification in `docs/architecture.md`. Canonical Device/Entity
contracts remain vendor-neutral; Adapter Functions may carry opaque,
Adapter-defined schemas and payloads. Core must not branch on their function names
or vendor-specific fields.

### 3.1 Target ownership and dependency direction

| Owner | Responsibilities |
| --- | --- |
| `adapters` | Adapter identity/runtime/health, immutable health heads and publication outbox/relay, management reads, and later function catalogs/invocations |
| `devices` | Bindings, Device/Entity metadata, local Adapter health consumer/projection, Entity availability reports/effective state/history, State, Observations, Entity Events, and Commands |
| `notifications` | Destinations, provider-send attempts, and notification history |
| `internal/app/hearthd` | Construct independent services/repositories, provision health delivery, run the existing health supervisor, and coordinate startup/drain |
| `internal/adapters` | Existing vendor-specific Adapter implementations, unchanged by the ownership extraction |

Domain imports may point from Devices to Adapters for Adapter-owned runtime and
health types. The Adapters domain must not import Devices or notifications.
Transport/persistence packages depend on their own domain. Neither module imports
the other's generated query types. This boundary follows product ownership, not
the fact that both processes use NATS.

Keep `adapters.Service` for runtime/health and existing Adapter management reads.
Add a separate `adapters.FunctionService` in the same flat module for the new
bounded invocation lifecycle. Separate service lifetimes do not create another
product module. Do not enlarge one repository interface to make every runtime
consumer depend on function history.

Move existing Adapter-owned types and behavior from `devices/health.go`,
`health_evaluation.go`, `health_reads.go`, `ids.go`, and `repository.go`. In
particular, `RuntimeID` and its parser become `adapters.RuntimeID` and
`adapters.ParseRuntimeID`. Move Adapter health enums, runtime evidence, instance
reads, claim/heartbeat/release/expiry parameters, and Adapter-specific error
identities. Update internal consumers rather than keeping permanent forwarding
methods or type aliases in Devices.

Keep Entity availability types, reason/transition values, validation, and history
reads in Devices. The existing `HealthReason` and `HealthTransition` value shapes
may remain there for Entity history, while Adapters owns equivalent values for
Adapter history. Their equal transport shapes do not require a shared health
product module or a dependency from Adapters back to Devices.

### 3.2 Durable health delivery and Devices-owned projection

The former design exposed `*sql.Tx` across module boundaries. That proposal is
withdrawn. The authoritative replacement is the linked
[health projection spec](adapter-health-projection.md).

```text
Adapters transaction                         Devices transaction
  Runtime/health/history                     Projected health/revision
  Immutable revisioned health head           Eligible report invalidation
  Health-update outbox                       Effective availability/history
  Commit                                    Commit, then Ack
       |                                         ^
       +-- relay --> NATS JetStream --> consumer -+
```

Adapters writes only Adapter-owned rows, including its health head/outbox and
Adapter-kind history. Devices writes only its projection, reports, effective
availability, and Entity-kind history. Both keep their existing shared SQLite
database, but never pass transactions or generated query types to each other.
Module APIs accept domain data, not SQL types. No generic unit of work or
transaction-in-context is introduced.

Use full health snapshots with a monotonic Adapter-scoped revision. Atomically
enqueue a snapshot with each material Adapter transition; source-only heartbeat
refreshes do not produce traffic. The relay retains outbox work until correct
broker acknowledgement. Devices records projection/revision and its availability
effects in one separate transaction before acknowledgement. Duplicate/older
snapshots do not reapply history or erase reports from a newer revision. Revision
gaps are allowed, with no fabricated intermediate Entity transitions.
At equal revision, compare canonical snapshot JSON bytes directly; store no
separate snapshot digest.

The health contract is a separate Core-only subject/schema and WorkQueue stream
for the single Devices durable consumer. It does not extend Device Facts or add
an SDK health API. Work is retained without age expiry; a full bounded stream
rejects publication rather than silently discarding unconsumed updates. Startup
republishes immutable current heads using fresh notification identities, safely
repairing current projection state without replaying any external action. NATS
reconnect resumes pending outbox publication and durable consumption without
queuing fresh snapshots. Lost delivery state requires Core restart to initiate
synchronization. The companion spec defines the exact resource settings.

The selected read/history behavior is explicit:

- Current Entity availability GET/list reads use Devices-owned effective rows.
  A read can still say available after authoritative Adapter health is unhealthy
  until the consumer applies the update. No canonical-health overlay is allowed.
- Commands retain their existing transaction-local authoritative runtime/health
  gate. A lagging available projection cannot authorize dispatch through a known
  unhealthy Adapter. Do not change existing unknown-health eligibility.
- Entity report admission still checks the owning runtime and authoritative
  healthy status. Stamp reports with the current health revision inside the same
  Devices transaction. Future-generation reports are retained until the matching
  health snapshot arrives; older messages cannot delete them.
- New Entity availability history records effective changes when Devices applies
  them, ordered by its existing receive order. It is not a complete source-time
  reconstruction. Adapter history remains the complete canonical health history.
- Registration baselines and ordinary availability reads use the local health
  projection, not direct canonical health. Existing canonical fencing reads for
  Registration, Observations, Entity Events, enablement, control, and notification
  eligibility stay with their current operation owners.

Keep the five-second SDK heartbeat, fifteen-second lease, one-second supervisor
poll, and fifteen-second recovery grace. The health supervisor remains the only
lease-expiry authority. Claim/release retries remain idempotent. Adapter status/
reason transition semantics are unchanged; only their Devices-side projection is
asynchronous.

Split `devices/sqlite/dbqueries/health.sql` by write ownership. Runtime/Adapter
management/head/outbox queries belong to Adapters. Report receipts, projected
health, effective availability, fan-out, and Entity history belong to Devices.
Keep both generated query packages against the shared migration directory.
Narrow cross-module read queries for authoritative admission are deliberate;
neither module imports the other's generated query package.

This foundation now requires an additive projection migration, report revision
backfill, health bootstrap, new broker resources, and lifecycle work. Existing
retained history is not rewritten. Before the first baseline snapshot arrives,
Entity availability may be unknown. See the companion migration/rollback
contract rather than treating extraction as a schema-free file move.

### 3.3 Runtime/service and transport cutover

The extracted runtime service preserves existing signatures after substituting
Adapter-owned types and defining its own small page type:

```go
// internal/modules/adapters/service.go and repository.go
type Stores struct {
    Runtimes RuntimeRepository
    Reads    ReadRepository
}
type Dependencies struct {
    Logger *slog.Logger
    Now    func() time.Time
}
func NewService(stores Stores, dependencies Dependencies) *Service
func (s *Service) ClaimAdapterRuntime(context.Context, ClaimAdapterRuntimeParams) error
func (s *Service) RecordAdapterHeartbeat(context.Context, AdapterHeartbeat) (HeartbeatResult, error)
func (s *Service) ReleaseAdapterRuntime(context.Context, string, RuntimeID) error
func (s *Service) ExpireAdapterLeases(context.Context, time.Time) error
func (s *Service) ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
func (s *Service) GetAdapter(context.Context, string) (AdapterInstance, error)
func (s *Service) ListAdapterHealthHistory(context.Context, ListAdapterHealthParams) (Page[HealthTransition], error)

type RuntimeRepository interface {
    ClaimAdapterRuntime(context.Context, ClaimRuntimeWrite) error
    RecordAdapterHeartbeat(context.Context, HeartbeatWrite) (HeartbeatResult, error)
    ReleaseAdapterRuntime(context.Context, ReleaseRuntimeWrite) error
    ExpireAdapterLeases(context.Context, ExpireLeasesWrite) error
}
type ReadRepository interface {
    GetAdapter(context.Context, string) (AdapterInstance, error)
    ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
    ListAdapterHealthHistory(context.Context, ListAdapterHealthParams) (Page[HealthTransition], error)
}
type Page[T any] struct { Items []T; HasMore bool }
```

These are moves of the current parameter/result definitions and validation
rules, not redesigned contracts. Adapter read pagination/error behavior stays
unchanged. `devices.Service` loses runtime and Adapter management methods. Its
stores retain a consumer-defined `AdapterReader` returning
`adapters.AdapterInstance`, supplied by `adapters.Service`, for preliminary
Registration and owned-mapping checks. Remove the embedded Adapter reader from
Registration/owned-mapping persistence interfaces and give Entity availability
history its own Devices-owned store capability. The relevant Devices methods use
those separate dependencies. Their actual persistence operations keep all
existing transaction-local eligibility checks.

The Devices-side dependency change is explicit:

```go
// internal/modules/devices/repository.go
type AdapterReader interface {
    GetAdapter(context.Context, string) (adapters.AdapterInstance, error)
}
type AvailabilityHistoryRepository interface {
    ListEntityAvailabilityHistory(context.Context, ListEntityAvailabilityParams) (Page[HealthTransition], error)
}
// HealthProjectionRepository and its input/result are defined in the companion spec.
```

```diff
 // internal/modules/devices/service.go
 type Stores struct {
     Registration  RegistrationRepository
     OwnedMappings OwnedMappingRepository
-    Runtimes      RuntimeRepository
-    Adapters      AdapterRepository
+    AdapterReads  AdapterReader
+    AvailabilityHistory AvailabilityHistoryRepository
+    HealthProjection HealthProjectionRepository
     Availability  AvailabilityRepository
@@
 }
```

Remove the old `RuntimeRepository` and `AdapterRepository` from Devices and the
`AdapterReader` embeddings from its Registration/owned-mapping persistence
interfaces. `checkRegistrationRuntime` and `ListOwnedMappings` call
`stores.AdapterReads.GetAdapter`; Entity availability history calls
`stores.AvailabilityHistory`. The shared SQLite eligibility reads stay with their
existing operation owners.

Move `devices/nats/session.go` and only the session DTO/mapping portions of
`wire.go` to `adapters/nats`. Entity Registration, owned mappings, availability,
enablement, Commands, Observations, Entity Events, and Device Facts stay in
`devices/nats`, with their references updated to Adapter-owned IDs/errors.
The existing `healthSupervisor` remains an application worker wired to
`adapters.Service.ExpireAdapterLeases`; there is no second supervisor or timer.

Move ownership of the existing Adapter HTTP routes, MCP tools, and resources to
`adapters/api`. Split mixed health mapping/history files by responsibility and
register each operation exactly once. Keep these existing contracts unchanged:

```text
GET /v1/adapters
GET /v1/adapters/{adapter_id}
GET /v1/adapters/{adapter_id}/health/history
list_adapters, get_adapter, list_adapter_health_history
hearth://adapters
hearth://adapter/{adapter_id}
hearth://adapter/{adapter_id}/health/history{?cursor,limit}
```

Preserve operation IDs, all existing URI templates, concrete collection resources,
response shapes, health source/reason semantics, pagination cursor encodings and
scope, and error mappings. The Device API continues to serve Entity availability
history with no route or cursor changes. Keep all existing NATS subjects, schema
IDs, payload bytes, five-second/fifteen-second Session behavior, public SDK types,
and device-free notification registration/sending compatible.

Stage one establishes module ownership and the selected NATS projection through
R1, H1, R2, H2, and R3. It must pass its acceptance gate before function
deliverables D1 through D7 begin. It adds health protocol/storage/resources but
no Function endpoint or SDK serving method. Implementation will be reviewed in
separate PRs from this specifications-only PR. Deliverable IDs describe dependency
order, not a requirement for one PR per deliverable. This spec does not authorize
implementation or stacked delivery.

Treat that first stage as one isolated migration phase in an implementation
worktree. Its scope is the listed ownership/projection changes; its exit gate is
E1 through E6 plus required checks. Temporary internal compile/test breakage is
allowed only inside that isolated phase, not on a deployed/shared branch. Use
disposable databases for iteration and retain a pre-feature DB/broker backup for
deployment reversal. Do not deploy a partial cutover or add permanent compatibility
wrappers solely to support mixed old/new Core processes. Begin Function work only
after the complete foundation is verified.

## 4. Catalog and schema contract

### 4.1 Descriptor

Domain definitions belong to `internal/modules/adapters/function_types.go`.
SDK equivalents belong to `sdk/adapter/functions.go`; neither imports the other.
Transport DTOs map between them.

```go
type CatalogID string    // afc_<UUIDv7>; public catalog_revision
type InvocationID string // afi_<UUIDv7>
type FunctionKey string  // existing subject-safe slug grammar

type Descriptor struct {
    Key          FunctionKey
    Description  string
    InputSchema  json.RawMessage
    OutputSchema json.RawMessage
    TimeoutMS    int
}

type Catalog struct {
    ID        CatalogID
    AdapterID string
    RuntimeID RuntimeID
    Functions []Descriptor
}

type Function struct {
    AdapterID       string
    CatalogRevision CatalogID
    Descriptor      Descriptor
    Callable        bool
}
```

Adapter IDs remain slug strings. `RuntimeID` is the canonical type already moved
to the Adapters domain by the prerequisite extraction.

Bounds are part of the public contract:

| Value | Bound |
| --- | --- |
| Functions per catalog | 1 to 64, distinct keys, sorted lexically by key during preparation |
| Function key | `^[a-z0-9][a-z0-9_-]{0,62}$` |
| Description | Nonblank valid UTF-8, 1 to 2000 Unicode code points |
| Timeout | Required integer milliseconds, 100 to 60000 inclusive |
| Each schema | At most 16 KiB after normalization |
| Entire encoded registration envelope | At most 256 KiB |
| Invocation arguments and successful result | Each at most 64 KiB after normalization |
| Decoded JSON arguments/results | Object root; at most 16 container levels and 1024 members/items per container |
| Invocation request/response envelope | At most 128 KiB each |
| Reason code | `^[a-z0-9][a-z0-9_.-]{0,127}$` |

Reject invalid UTF-8, trailing JSON values, duplicate object keys in schemas and
function arguments/results, non-JSON numbers, and oversized values. No truncation
or coercion. Decode generic numbers with `json.Decoder.UseNumber`; do not round
them through `float64` during Core or SDK preparation.

### 4.2 Supported schema profile

Use the existing `jsonschema/v6` dependency. Introduce the small public package
`contracts/functionschema` so SDK and Core enforce the same dynamic-schema rules
without importing Core modules. Existing embedded wire validation remains owned
by `contracts/v1`.

The v1 profile is a bounded subset of JSON Schema draft 2020-12. The root must
explicitly declare `type: "object"`. Supported schema keywords are:

```text
$schema, title, description, type, properties, required, additionalProperties,
items, minItems, maxItems, uniqueItems, minLength, maxLength,
minimum, maximum, exclusiveMinimum, exclusiveMaximum, multipleOf, enum, const
```

- `$schema`, when present, must be `https://json-schema.org/draft/2020-12/schema`.
- Nested schemas support the standard JSON primitive/container types and arrays
  of type names, including `null` for nullable fields.
- `additionalProperties` may be a boolean or a supported schema. `items` is a
  single supported schema, not tuple validation.
- Reject unsupported schema keywords at schema positions. Property names inside
  `properties` and object values inside `const`/`enum` are data, not keywords.
- Reject `$id`, `$ref`, `$defs`, dynamic references, regular-expression keywords,
  custom formats, and composition keywords. There is no network/file schema
  loader, reference resolver, or recursive schema graph.
- Limit a schema to 256 schema nodes and 16 schema nesting levels. Arrays in
  `enum`, `required`, and `type` have at most 64 elements. Schema annotation
  strings have at most 2000 code points. The compiler must also validate the
  supported keywords' standard types and constraints.
- Schema defaults do not exist in this profile. Missing values are never filled
  in by Core or the SDK.

These restrictions bound the initial validator and make unsupported assertions
fail explicitly rather than silently disappear. A future profile extension is a
protocol enhancement, not a reason to add per-function SDK methods.

```go
// contracts/functionschema; compiled schemas are immutable and concurrency-safe.
type Schema struct { /* private compiled representation */ }
func Compile(document json.RawMessage) (*Schema, error)
func (s *Schema) Validate(value json.RawMessage) error
func NormalizeObject(value json.RawMessage) (json.RawMessage, error)
```

Normalization sorts object keys through JSON decoding/re-encoding, removes
insignificant whitespace, and preserves number literals. Catalog comparison uses
normalized descriptors in sorted key order. Numerically equivalent but
differently spelled literals such as `1` and `1.0` remain distinct catalog data.

### 4.3 Registration and revision lifecycle

1. The SDK prepares an immutable descriptor/handler map and mints one `afc_` ID.
2. It activates and flushes its runtime's function invocation subscription.
3. It registers the complete catalog using that ID. Core validates all descriptors
   and commits the whole catalog atomically under active-runtime fencing.
4. Only then does the SDK's start method return successfully.

The SDK chooses the catalog ID before registration so handlers can verify an
invocation that arrives after Core commits but before the registration reply
arrives. Handlers must be ready when the start method is called, not only after
it returns.

Registration retries reuse one ID, envelope, bytes, and trace/correlation
metadata. For the same runtime, the same catalog ID and normalized descriptors
are idempotent; a different ID or changed descriptor is `catalog_conflict`.
Reject stale runtimes with the existing SDK fencing behavior. The SDK mints a
fresh catalog ID for every new Session. Core rejects an ID that is currently
stored against a different runtime or Adapter, including reuse of the Adapter's
last catalog ID by its replacement runtime. It does not maintain tombstones for
retired revisions. Catalog IDs are identities, not credentials or authorization.

A new runtime registers its own catalog and revision. Its registration replaces
the Adapter's last catalog in one transaction, including omission/removal of old
keys. There is no per-function canonical ID or cross-Adapter identity migration.
Adapter plus function key is the discovery address; revision pins its meaning.

Core retains the last catalog during Adapter downtime. `callable` means that the
catalog belongs to the current, non-ended runtime. It is not gated by Adapter
health and does not independently compare the lease clock. Before a new runtime
registers, the previous runtime's catalog is visible but not callable. An Adapter
that never registered functions has an empty collection.

Closing a function server stops local admission and joins handlers; it does not
add an unregister protocol. Discovery can briefly remain callable, as it can
after a transport disconnect. Confirmed NATS no-responders becomes a recorded
`not_executed` outcome. Graceful Session release or supervisor-owned expiry makes
the catalog non-callable without deleting it.

Core-only restart preserves the catalog and runtime association. Rebuild compiled
schema state from persisted descriptors, lazily per catalog revision or on read.
A corrupt persisted descriptor produces a safe internal error for the affected
catalog, not an empty catalog, a permissive validator, or a remembered global
admission fault. There is no unbounded cross-revision schema cache.

## 5. Invocation contract

### 5.1 Types and outcomes

```go
// internal/modules/adapters/function_types.go
type InvokeInput struct {
    AdapterID       string
    FunctionKey     FunctionKey
    CatalogRevision CatalogID
    Arguments       json.RawMessage
}

type RouteSnapshot struct {
    AdapterID string
    RuntimeID RuntimeID
}

type Evidence interface { functionEvidence() }
type Succeeded struct { Result json.RawMessage }
type Failed struct { ReasonCode string }
type NotExecuted struct { ReasonCode string }
type Unknown struct { ReasonCode string }
// The four value variants implement the private marker method.

type Completion struct {
    At       time.Time
    Evidence Evidence
}

type Invocation struct {
    ID              InvocationID
    CatalogRevision CatalogID
    Function        Descriptor
    Route           RouteSnapshot
    Arguments       json.RawMessage
    StartedAt       time.Time
    Deadline        time.Time
    Completion      *Completion // nil only while running
}
```

| Public status | Meaning |
| --- | --- |
| `running` | Admission committed; terminal evidence has not committed |
| `succeeded` | Adapter reported success under the advertised contract and its valid result was retained |
| `failed` | Adapter reported function failure; partial effects may have happened and are not rolled back |
| `not_executed` | Core or Adapter can establish that no function-specific work began |
| `unknown` | Work may have happened, but authoritative completion is unavailable |

Success is Adapter-defined contract completion, not proof of physical effect.
Failure is not permission to retry. Calling again creates another invocation and
may repeat effects. A function may be naturally idempotent, but Core does not
infer, declare, or enforce that property.

Only successful results carry arbitrary result data in v1. Other outcomes carry a
bounded reason code, never raw upstream errors, stack traces, or arbitrary error
objects. A function that needs structured diagnostic values may return a normal
schema-defined successful result describing the diagnostic finding.

### 5.2 Admission and dispatch

1. Check public admission and validate the Adapter ID, function key, required
   revision, and presence of arguments. Leave JSON normalization and dynamic
   argument validation to `PrepareInvocation` after descriptor resolution.
2. Resolve the descriptor and capture the start time. Prepare and validate
   normalized arguments once for that revision, including compilation of the
   supported schemas when not already available.
3. Set the absolute deadline to start plus the descriptor's timeout. It includes
   preparation, admission, transport, and handler execution, but not preliminary
   catalog discovery. Admission persistence is additionally bounded to five
   seconds, and may not extend that deadline.
4. In one SQLite transaction, verify that the selected revision/key still belongs
   to the active non-ended runtime, snapshot descriptor and route, and insert the
   `running` invocation. A revision change is `catalog_changed`; lost runtime
   eligibility is `adapter_unavailable`. Neither dispatches or inserts a row.
   Mint the invocation ID once for this admission. An ID-mint or insert failure
   prevents external work just as any other admission failure does.
5. Make at most one Core NATS request to the snapshotted runtime. No reroute,
   reconnect retry, application queue, JetStream stream, or durable worker is added.
6. Validate the response envelope and successful result against the snapshotted
   output schema. Commit terminal evidence with a separate five-second
   persistence context before returning confirmed completion.

After admission commits, the invocation is process-owned and bounded by its own
deadline. HTTP/MCP caller cancellation cannot cancel or extend it. No transaction
is held during NATS or Adapter work. Shutdown joins admitted invocations before
their dependencies close.

Core checks runtime eligibility at admission. Runtime takeover cannot retarget an
existing invocation. Fencing prevents new stale-Core writes and routes; it cannot
undo an external effect already begun by an old process. The SDK rejects known
fenced/closed Sessions and checks revision/key/deadline before calling a handler.

There is no new fixed Core/SDK concurrency cap, ordering guarantee, or waiting
queue. Handlers can overlap for the same key. Adapters own protocol serialization
and may reject before beginning work with a safe `not_executed` reason such as
`adapter_busy`. Their own waiting must honor the invocation deadline.

### 5.3 Failure, recovery, and evidence persistence

| Condition | Result |
| --- | --- |
| Invalid arguments or schema mismatch | Input error; no invocation or handler call |
| Missing Adapter/function | Not found; no invocation |
| Stale discovery revision | `catalog_changed`; no invocation |
| No eligible registered runtime at admission | `adapter_unavailable`; no invocation |
| Admission write fails | Internal error; no dispatch |
| Deadline expires before publication is attempted | `not_executed / deadline_expired` |
| Confirmed NATS no-responders | `not_executed / adapter_unavailable` |
| SDK rejects key/revision or expired request before the handler | `not_executed` with `function_unavailable`, `catalog_changed`, or `deadline_expired` |
| Adapter explicitly reports an outcome | Valid terminal evidence, subject to the deadline and result validation |
| Missing, malformed, mismatched, or output-schema-invalid response | `unknown / invalid_response`; do not retain invalid result data |
| Timeout or transport failure after possible publication | `unknown / outcome_timeout` or `transport_error` |
| Handler panic | SDK recovers at the callback boundary, returns `unknown / handler_panic`, and logs no panic value or stack payload |
| Terminal write fails | Internal error with the known invocation ID; no confirmed success, retry, or invented terminal record |
| Core restart with a `running` record | Atomically terminalize as `unknown / core_restarted`; never execute again |

The SDK bounds its wait for an outcome to the request deadline. It cancels the
handler context on expiry and may return `unknown / outcome_timeout` while a
non-cooperating handler is still returning. Server/Session cleanup must still
join that actual handler; context cancellation or a completed reply is not proof
that external work stopped. Do not leak a goroutine or withdraw handler
dependencies merely to make cleanup appear bounded.

Late replies never revise a terminal outcome. Evidence proving non-execution may
be recorded after deadline expiry; a late success or failure response cannot
establish timely completion. An Adapter reports `unknown` rather than
`not_executed` when cancellation happens after possible effects.

Persistence failures affect only the current operation. Do not latch admission or
readiness closed, retry evidence writes indefinitely, or block later independent
invocations after their own admission writes succeed. Startup recovery must
complete before the function service accepts public invocations.

## 6. Component interfaces

### 6.1 Core service and persistence

The service is transport-independent. Concrete SQLite and NATS implementations
are selected in `internal/app/hearthd`.

```go
// internal/modules/adapters/function_service.go
func NewFunctionService(ctx context.Context, repo FunctionRepository, invoker FunctionInvoker) (*FunctionService, error)
func (s *FunctionService) RegisterCatalog(ctx context.Context, catalog Catalog) error
func (s *FunctionService) ListFunctions(ctx context.Context, adapterID string, page PageInput) (FunctionPage, error)
func (s *FunctionService) GetFunction(ctx context.Context, adapterID string, key FunctionKey) (Function, error)
func (s *FunctionService) Invoke(ctx context.Context, input InvokeInput) (Invocation, error)
func (s *FunctionService) ListInvocations(ctx context.Context, filter InvocationFilter) (InvocationPage, error)
func (s *FunctionService) GetInvocation(ctx context.Context, id InvocationID) (Invocation, error)
func (s *FunctionService) StopAdmission()
func (s *FunctionService) AdmissionOpen() bool
func (s *FunctionService) Drain(ctx context.Context) error
func (s *FunctionService) PruneHistory(ctx context.Context, now time.Time) error

type FunctionInvoker interface {
    Invoke(context.Context, Invocation) Evidence
}

type FunctionRepository interface {
    RegisterCatalog(context.Context, Catalog) error
    ListFunctions(context.Context, string, PageInput) (FunctionPage, error)
    GetFunction(context.Context, string, FunctionKey) (Function, error)
    Admit(context.Context, PreparedInvocation) (Invocation, error)
    Complete(context.Context, InvocationID, Evidence, time.Time) (Invocation, error)
    GetInvocation(context.Context, InvocationID) (Invocation, error)
    ListInvocations(context.Context, InvocationFilter) (InvocationPage, error)
    Recover(context.Context, time.Time) error
    DeleteHistoryBefore(context.Context, time.Time, int) (int64, error)
}

type PageInput struct { Limit int; Cursor string }
type FunctionPage struct { Items []Function; NextCursor string }
type InvocationFilter struct {
    AdapterID   string
    FunctionKey FunctionKey // empty means all keys for this Adapter
    PageInput
}
type InvocationPage struct { Items []Invocation; NextCursor string }

type PreparedInvocation struct {
    function  Function
    arguments json.RawMessage
    startedAt time.Time
    deadline  time.Time
}
func PrepareInvocation(function Function, input InvokeInput, start time.Time) (PreparedInvocation, error)
func (p PreparedInvocation) Function() Function
func (p PreparedInvocation) Arguments() json.RawMessage
func (p PreparedInvocation) StartedAt() time.Time
func (p PreparedInvocation) Deadline() time.Time
```

`PreparedInvocation` is a domain-owned value with unexported fields. Its production
preparation function accepts a resolved `Function`, arbitrary `InvokeInput`, and
start time; validates scope, revision, schema, and deadline; and copies normalized
arguments/descriptor into the value. Repository admission consumes that
preparation and rechecks changing catalog/runtime facts transactionally. It does
not trust a caller-supplied runtime or reselect a different descriptor. Its
read-only accessors support the SQLite consumer without exporting mutable fields.
Preparation and accessors copy slices and raw JSON so callers cannot mutate a
validated descriptor or its arguments after preparation.
The repository also compares the prepared normalized descriptor to the stored
descriptor for that key and revision. A caller cannot use a freely constructed
`Function` with a real revision and a weaker schema to bypass validation.

Registration and completion are independently callable boundaries. They validate
their accepted domain inputs as well as transactional facts. Do not duplicate
schema preparation inside one service workflow or weaken repository fencing.
`Complete` is an immutable first-terminal-write operation. A conflicting later
completion cannot replace the first row. Completion validates successful result
data against the stored descriptor snapshot, not a newer catalog.

Use module-owned error identities for invalid input, unknown Adapter/function/
invocation, changed catalog, unavailable runtime, closed admission, catalog
conflict, and persistence failure. Errors after admission retain the known
invocation ID for safe transport mapping. No Huma, MCP, NATS, sqlc, or vendor type
crosses these interfaces.

### 6.2 SDK

```go
// sdk/adapter/functions.go
type FunctionDescriptor struct {
    Key          string
    Description  string
    InputSchema  json.RawMessage
    OutputSchema json.RawMessage
    TimeoutMS    int
}
type Function struct {
    Descriptor FunctionDescriptor
    Handler    FunctionHandler
}
type FunctionRequest struct {
    InvocationID string
    FunctionKey  string
    Arguments    json.RawMessage
    Deadline     time.Time
}
type FunctionHandler func(context.Context, FunctionRequest) FunctionResult

type FunctionResult interface { functionResult() }
type FunctionSucceeded struct { Result json.RawMessage }
type FunctionFailed struct { ReasonCode string }
type FunctionNotExecuted struct { ReasonCode string }
type FunctionUnknown struct { ReasonCode string }
// These SDK-owned value variants implement the private marker method.

func (s *Session) StartFunctionServer(ctx context.Context, functions []Function) (*FunctionServer, error)
func (s *FunctionServer) Wait(ctx context.Context) error
func (s *FunctionServer) Close(ctx context.Context) error

type FunctionRegistrationRejectedError struct { ReasonCode string }
```

Add one Session-owned server and immutable catalog state:

```diff
 type Session struct {
     notificationServer *NotificationServer
     notificationKeys   map[string]*notificationKeyRegistration
+    functionServer     *FunctionServer
+    functionCatalog    *functionCatalog
 }
```

This is a focused extension, not a request to unify Commands, notifications, and
functions behind a provider framework. Compare `NotificationServer` first and
share only mechanics whose actual admission/joining contracts match.

`StartFunctionServer` rejects nil handlers, duplicate keys, invalid descriptors,
and a second active function server. It validates input before handler execution
and output before replying. Nil or invalid handler results become
`unknown / invalid_response`. The service still validates responses because the
Adapter is a separate protocol boundary.

The Session retains the prepared catalog identity across a server restart within
that Session. The same descriptor set may be served again; a different set is a
permanent local validation error. A failed start unsubscribes and joins any
already-admitted callback, even when registration's outcome was ambiguous.

Closing a server stops admission, unsubscribes, and joins callbacks. A caller's
join timeout ends only its wait; a later `Close` can continue joining. Session
close/fencing also closes and joins the function server before releasing its
runtime/connection. Do not hold Session mutexes while joining callbacks. SDK
handler contexts retain incoming trace values and the absolute request deadline;
an unrelated start-context cancellation must not silently abandon admitted work.

`Wait` observes closure after handlers return and does not itself stop the server.
It returns the Session's terminal error when present, otherwise `ErrClosed` for
normal closure, or the caller's context error when its wait ends. Permanent Core
registration refusals return `FunctionRegistrationRejectedError`; stale-runtime
refusal returns `ErrRuntimeFenced` and terminates the Session. Invalid local
descriptors use the existing `ValidationError`, and a closed Session uses
`ErrClosed`. Start cancellation returns the caller's context error only after its
failed-start cleanup has joined admitted callbacks.

### 6.3 NATS wire

New plain request/reply subjects:

```text
hearth.v1.adapter.<adapter_id>.runtime.<runtime_id>.function-register
hearth.v1.adapter.<adapter_id>.runtime.<runtime_id>.function-invoke
```

The first is Adapter-to-Core; the second is Core-to-Adapter. Adapter and runtime
identity comes from the validated subject, not duplicated payload fields.

Add four strict v1 envelope schemas:

| File and schema suffix | `data` shape |
| --- | --- |
| `adapter-function-registration-request.schema.json` | `{catalog_revision, functions:[{key,description,input_schema,output_schema,timeout_ms}]}` |
| `adapter-function-registration-response.schema.json` | Accepted `{status:"accepted",catalog_revision}` or rejected `{status:"rejected",reason_code}` |
| `adapter-function-invoke-request.schema.json` | `{invocation_id,catalog_revision,function_key,arguments,deadline}` |
| `adapter-function-invoke-response.schema.json` | One of succeeded `{invocation_id,status:"succeeded",result}` or `{invocation_id,status:"failed"|"not_executed"|"unknown",reason_code}` |

Schema IDs follow `urn:hearth:schema:<file stem>:v1`. Add the ID definitions to
`common.schema.json` and register the files in `contracts/v1/embed.go`.
All object fields are strict except the explicitly Adapter-defined schema and
argument/result objects. Use schema alternatives to disallow contradictory
result/reason fields.

Registration envelope ID equals the catalog revision. Invocation envelope ID
equals `invocation_id`. Responses have a new response ID, matching correlation,
and the request envelope ID as causation. Reject subject/runtime mismatch,
incorrect data/envelope identity, wrong revision, or incorrect response
correlation/causation. Use UTC RFC3339Nano deadlines and existing W3C propagation.

The Core function registration server embeds `platformnats.RequestReplyServer`,
following notifications' `RegistrationServer`. Its callback owns module
preparation, transactional fencing, and safe rejection mapping. Functions add no
further worker, consumer, broker resource, or Core NATS connection beyond the
health foundation described above.

### 6.4 Validation responsibilities

Transport DTOs, domain values, and stored rows are different boundaries. A valid
wire envelope does not establish domain validity, and a service call does not
make a freely callable repository safe by assumption. Use shared pure validation
and preparation functions at those independent entry points. Within each entry
point, reuse normalized values, compiled schemas, encoded bytes, and resolved
references rather than preparing the same input again in successive helpers.

| Entry point and accepted input | Structural validation and preparation | Changing facts checked at the operation |
| --- | --- | --- |
| HTTP/MCP handlers accept transport models | Enforce outer fields, required/null semantics, body bounds, and scoped cursor decoding; map once to domain input. They do not interpret Adapter-specific arguments. | No authoritative eligibility decision in the handler. |
| `FunctionService.RegisterCatalog` and repository `RegisterCatalog` each accept freely constructed `Catalog` values | Each boundary checks IDs, descriptor bounds, duplicate keys, schema profile, and normalized lexical catalog order. Keep compiled schemas and encodings local to that preparation. | Repository checks active runtime, catalog identity reuse, and same-runtime immutability in its write transaction. |
| `FunctionService.Invoke` accepts `InvokeInput`; `PrepareInvocation` accepts resolved `Function`, input, and start time | Check routing identities before discovery. Prepare the full descriptor, arguments, and deadline once into `PreparedInvocation`; copy mutable data. | Repository `Admit` checks the stored descriptor/revision and runtime inside the admission transaction, not against the discovery result alone. |
| Repository `Admit` consumes `PreparedInvocation`, including its constructible zero value | Reject zero/incomplete preparation. Consume its owned normalized data without another argument-schema preparation pass. | Recheck descriptor equality and runtime eligibility in the transaction; keep admission bounded by the existing absolute deadline. |
| Core NATS invoker accepts the admitted `Invocation` and receives wire response bytes | Validate its route/request inputs and response envelope, identity, correlation, deadline, and output schema. Reuse normalized result data in the returned evidence. | Check the deadline immediately before publication and when classifying the response; never reroute after admission. |
| Repository `Complete` accepts invocation ID, freely constructed `Evidence`, and completion time | Reject invalid IDs/times, nil or unsupported evidence, invalid reason codes, and malformed or schema-invalid successful results. Normalize once against the retained descriptor. | Load the retained invocation and enforce first-terminal-write and snapshot-relative completion rules in its transaction. |
| SDK `StartFunctionServer` accepts descriptors and handlers; its callback accepts request bytes and handler results | Prepare/copy the catalog once, reject nil handlers, and retain immutable schemas. The callback validates envelope and dynamic arguments before the handler and result data before replying. | Session fencing, local admission, catalog/key, and deadline checks occur when the callback would begin work. |
| Service and repository discovery/history/maintenance methods accept IDs, pages, filters, times, or batch limits | Validate freely supplied IDs, cursor scope, supported limits, UTC times, and positive bounded batch sizes at their own entry points. Reuse decoded positions within that boundary. | Read current existence/catalog scope in the read operation; recovery and pruning select eligible rows in their own write operations. |

`PreparedInvocation` is justified by its actual producer/consumer pair. It carries
an immutable, schema-checked admission input from the domain preparation to
SQLite while allowing transaction-local comparisons. Do not add parallel prepared
types for every method solely to avoid independent boundary validation.

Extracted runtime repository methods still accept freely constructible
`ClaimRuntimeWrite`, `HeartbeatWrite`, `ReleaseRuntimeWrite`, and `ExpireLeasesWrite`.
They must enforce their IDs, software metadata, health/reason combinations, and
time/lease invariants rather than trusting the extracted service. Share the pure
checks with service preparation. Keep active-runtime, legal-transition, and
lease-expiry decisions transaction-local. This preserves the existing external
contract while making the repository's input responsibility explicit.

Every validator above must be called by the production constructor, decoder,
preparation, or persistence operation it protects. Test these operations with
invalid freely constructed values as well as wire input. An exported validator
with only test callers is not enforcement. Assertion-only fixture helpers stay
in `_test.go` files. When moving or removing a check, identify its remaining
production enforcement point and preserve the same safe error category.

Name raw-input helpers for the work they perform, such as `prepareCatalog` for
descriptor validation, normalization, and compilation. A helper consuming an
existing preparation should not advertise another validation pass. Keep these
helpers private unless a real cross-package production consumer needs them.

## 7. Persistence, retention, and recovery

Add an additive Goose migration after the health projection migration, currently
`00016_adapter_functions.sql` after `00015_adapter_health_projection.sql`. Recheck
the sequence at implementation time rather than editing an existing migration.

```sql
CREATE TABLE adapter_function_catalogs (
    adapter_id TEXT PRIMARY KEY REFERENCES adapter_instances(adapter_id),
    catalog_revision TEXT NOT NULL UNIQUE,
    runtime_id TEXT NOT NULL REFERENCES adapter_runtimes(runtime_id),
    registered_at TEXT NOT NULL,
    functions_json TEXT NOT NULL CHECK (json_valid(functions_json))
);

CREATE TABLE adapter_function_invocations (
    id TEXT PRIMARY KEY,
    adapter_id TEXT NOT NULL,
    runtime_id TEXT NOT NULL,
    catalog_revision TEXT NOT NULL,
    function_key TEXT NOT NULL,
    descriptor_json TEXT NOT NULL CHECK (json_valid(descriptor_json)),
    arguments_json TEXT NOT NULL CHECK (json_valid(arguments_json)),
    started_at TEXT NOT NULL,
    deadline TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN
        ('running','succeeded','failed','not_executed','unknown')),
    result_json TEXT,
    reason_code TEXT,
    completed_at TEXT,
    CHECK (
        (status = 'running' AND completed_at IS NULL AND result_json IS NULL AND reason_code IS NULL)
        OR (status = 'succeeded' AND completed_at IS NOT NULL
            AND result_json IS NOT NULL AND json_valid(result_json) AND reason_code IS NULL)
        OR (status IN ('failed','not_executed','unknown') AND completed_at IS NOT NULL
            AND result_json IS NULL AND reason_code IS NOT NULL)
    )
);
CREATE INDEX adapter_function_invocations_by_adapter
    ON adapter_function_invocations(adapter_id, started_at DESC, id DESC);
CREATE INDEX adapter_function_invocations_by_function
    ON adapter_function_invocations(adapter_id, function_key, started_at DESC, id DESC);
CREATE INDEX adapter_function_invocations_retention
    ON adapter_function_invocations(completed_at, id) WHERE status != 'running';
```

Invocation routing and descriptor snapshots have no foreign key to the mutable
catalog. Removing a function or replacing a catalog must not rewrite or delete
history. Store fixed-width UTC timestamps using the existing repository
convention. SQL constraints enforce outcome combinations; the domain boundary
also validates schema/profile, lengths, reason codes, and object shapes.

One catalog per Adapter is bounded by the descriptor limits and remains until
replaced. Replacing it must check active runtime and same-runtime immutability in
the same transaction. The catalog's revision is unique among current rows;
SDK-minted UUIDv7 identity is not a household-wide eternal tombstone registry.
Do not add unbounded historical catalog storage just to remember all revisions.

Retain terminal invocations for thirty days after `completed_at`; never prune
running records. Store normalized arguments, the descriptor snapshot, and valid
successful result. Do not retain invalid arguments, malformed responses, or raw
upstream error payloads. Invoke the module's prune pass after notifications in the
existing shared worker, with batches of 500 and no separate timer or setting.
The new `adapters` prune task calls `FunctionService.PruneHistory`; it does not
prune existing Adapter health history or runtime evidence, whose retention is
unchanged by either stage.

Startup recovery marks all `running` rows `unknown / core_restarted` with Core's
recovery time. No invocation is executable from SQLite. Outcome writes that fail
leave evidence for this recovery, not a retry backlog.

Migration Down drops only this feature's tables/indexes. Preserve a database
backup before using Down; dropped history is not recoverable by upgrading again.
Older binaries do not expose Functions, and a Functions-enabled Adapter requires
the new Core. Upgrade Core before the Adapter. An old Adapter can continue to run
without registering functions. Binary rollback uses a compatible database backup
and Adapter version; do not claim a destructive Down is a lossless rollback.

## 8. HTTP, MCP, and trust

### 8.1 HTTP API

| Method/path | Operation ID |
| --- | --- |
| `GET /v1/adapters/{adapter_id}/functions` | `list-adapter-functions` |
| `GET /v1/adapters/{adapter_id}/functions/{function_key}` | `get-adapter-function` |
| `POST /v1/adapters/{adapter_id}/functions/{function_key}/invocations` | `invoke-adapter-function` |
| `GET /v1/adapters/{adapter_id}/function-invocations` | `list-adapter-function-invocations` |
| `GET /v1/adapter-function-invocations/{invocation_id}` | `get-adapter-function-invocation` |

Invocation input:

```json
{
  "catalog_revision": "afc_<UUIDv7>",
  "arguments": {"entity_id": "ent_<UUIDv7>"}
}
```

Use explicit transport types:

```go
// internal/modules/adapters/api/function_models.go
type FunctionBody struct {
    AdapterID       string          `json:"adapter_id"`
    FunctionKey     string          `json:"function_key"`
    CatalogRevision string          `json:"catalog_revision"`
    Description     string          `json:"description"`
    InputSchema     json.RawMessage `json:"input_schema"`
    OutputSchema    json.RawMessage `json:"output_schema"`
    TimeoutMS       int             `json:"timeout_ms"`
    Callable        bool            `json:"callable"`
}
type InvokeBody struct {
    CatalogRevision string          `json:"catalog_revision"`
    Arguments       json.RawMessage `json:"arguments"`
}
type InvocationBody struct {
    ID              string          `json:"id"`
    AdapterID       string          `json:"adapter_id"`
    FunctionKey     string          `json:"function_key"`
    CatalogRevision string          `json:"catalog_revision"`
    Arguments       json.RawMessage `json:"arguments"`
    Status          string          `json:"status"`
    Result          json.RawMessage `json:"result,omitempty"`
    ReasonCode      string          `json:"reason_code,omitempty"`
    StartedAt       string          `json:"started_at"`
    Deadline        string          `json:"deadline"`
    CompletedAt     *string         `json:"completed_at"`
}
```

All listed input fields are required and non-null. Arguments and results are
objects, including `{}` for a no-argument or empty-result function. Collection
bodies contain `items` and optional `next_cursor`. Public invocation records omit
runtime/correlation IDs and internal descriptor snapshots. `completed_at` is
explicitly null while running. A successful result is present only for
`succeeded`; `reason_code` is present only for the other terminal variants.
OpenAPI and MCP output schemas must express these alternatives rather than
advertise impossible combinations of optional fields.

The required-field rule applies to the Function invocation input, not existing
Device or Entity PATCH operations. Preserve their JSON Merge Patch contract
during extraction and assembly changes. They require
`application/merge-patch+json`, accept optional fields and `{}` as a no-op for an
existing resource, reject unknown/read-only fields, and allow null only where
defined, such as clearing a name override. Request models, runtime validation,
and OpenAPI must continue to agree on omission/null semantics.

`POST` blocks until terminal evidence is retained and returns HTTP 200 with the
record for every modeled terminal outcome. This matches notification attempts
rather than converting retained failed attempts into transport exceptions.
Return a `Location` header pointing to the invocation detail on that response.
There is no HTTP 202, caller-provided invocation ID, idempotency key, or automatic
client retry. If the connection drops before the response, the Adapter-scoped
history list is the diagnostic recovery path; it is not a cross-request dedupe API.

Pre-admission errors use Huma Problem Details: 422 for malformed IDs/revisions,
unsupported fields, nulls, bad cursors, or invalid function arguments; 404 for an
unknown resource; 409 with `catalog_changed` for a stale revision; and 503 with
`adapter_unavailable` or `admission_closed` for unavailable admission. Bound
request reading before parsing: the POST body is at most 128 KiB, with normalized
arguments additionally limited to 64 KiB. Oversized bodies return 413.
An internal failure returns a generic 500; after admission it also includes the
safe known `invocation_id`. Never include raw schema-validator excerpts that
could echo payload values. Successful evidence must not be returned when its
terminal write failed.

Both collections use existing endpoint-scoped opaque keyset conventions, default
50 items and limits 1 to 200. Function listing orders by key; its cursor binds Adapter
and catalog revision, so replacement requires restarting the list and returns
`catalog_changed` for an old cursor. Invocation listing orders newest-first by
fixed-width `started_at`, then ID, and accepts optional `function_key`. Bind that
filter and Adapter in the cursor. Unknown Adapter is 404; an unknown but
well-formed function filter returns an empty invocation list, including when the
function has been removed from the current catalog. History pages are not
snapshots. Cursors confer no authority, and no totals are returned.

### 8.2 MCP

Register five fixed tools in the Adapters API package, alongside its extracted
Adapter management tools:

```text
list_adapter_functions
get_adapter_function
invoke_adapter_function
list_adapter_function_invocations
get_adapter_function_invocation
```

Invocation arguments are `adapter_id`, `function_key`, `catalog_revision`, and
`arguments`. The fixed MCP tool schema validates the outer object. The shared
service validates `arguments` against the selected function schema, including
when invoked through the built-in agent. Document the discover/describe/invoke
sequence in tool descriptions. Do not put Adapter-supplied prose into system
instructions or treat a returned schema/description as authorization.

Use the same service and DTO mapping as HTTP. Register reads and successful
invocation output with `mcpapi.Tool.ExactOutput` to preserve numeric JSON bytes.
For a non-success terminal outcome, return the existing `mcpapi.ToolError` with
`Code` equal to the terminal status, a fixed safe message, and
`Details: map[string]any{"invocation": invocationBody}`. Its structured content
contains `failure_code`, `message`, and the complete retained record under
`invocation`, including ID, arguments, status, and reason code. Keep the typed DTO
or raw JSON in Details; do not round-trip through `map[string]float64` or an
untyped float-decoding step. Successful output is the invocation record without
`isError`. The advertised output schema includes the existing ToolError variant.

Pre-admission domain failures use safe stable codes. Input-schema-invalid
arguments are a tool error `invalid_input` when only the service's dynamic schema
can establish invalidity; outer tool-shape validation remains the MCP SDK's
responsibility. Internal errors after admission carry `invocation_id` in safe
ToolError details, matching HTTP's diagnostic recovery contract.

Expose poll-only resource templates for every GET, consistent with the existing
MCP read contract:

```text
hearth://adapter/{adapter_id}/functions{?limit,cursor}
hearth://adapter/{adapter_id}/function/{function_key}
hearth://adapter/{adapter_id}/function-invocations{?function_key,limit,cursor}
hearth://adapter-function-invocation/{invocation_id}
```

No per-function MCP registration, catalog-change notification, agent tool refresh,
or separate agent function bridge is needed. An Adapter can register after the
built-in agent starts and still be discovered and invoked through the fixed tools.

### 8.3 Trust and non-secret payloads

The HTTP/MCP and NATS deployment trust boundaries are unchanged: trusted household
deployment, loopback defaults, no new authentication promise. Runtime/catalog IDs
are fencing and revision evidence, not credentials. Anyone with existing access
to these management APIs can invoke the functions they expose.

Arguments, schemas, descriptions, and results must contain no credentials. Use
Adapter-local credential files or references whose secret values never cross the
function boundary. Core persists valid payloads for thirty days, and the existing
built-in agent may also retain them in its own trace/history. A schema annotation
does not redact either store. No generic redaction or secret-bearing function
mode is promised by this feature.

Adapters own safe outbound-target policy. Functions may select configured
destinations and implement Adapter-specific requests; this framework does not
ship an unrestricted URL, MQTT-topic, filesystem-path, or shell-command proxy.
Core does not inspect custom argument fields to enforce vendor-specific rules.

Logs contain invocation/catalog/Adapter IDs and fixed safe codes, not arguments,
results, raw schema errors, provider responses, or raw panic values. Retained
structured payloads remain visible through the documented history APIs.

## 9. First real function: Zigbee2MQTT refresh_entity

Register exactly one function in the Zigbee2MQTT Adapter:

```json
{
  "key": "refresh_entity",
  "description": "Request an Entity's readable upstream properties once. Success means MQTT PUBACK, not fresh Hearth State.",
  "input_schema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["entity_id"],
    "properties": {"entity_id": {"type": "string", "minLength": 40, "maxLength": 40}}
  },
  "output_schema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["entity_id", "request_status"],
    "properties": {
      "entity_id": {"type": "string", "minLength": 40, "maxLength": 40},
      "request_status": {"const": "published"}
    }
  },
  "timeout_ms": 10000
}
```

The Adapter validates canonical Entity ID syntax and resolves it only against its
current reconciled inventory. The function never accepts a friendly name, IEEE
address, MQTT topic, property list, or raw MQTT payload from the caller.

`buildRouteSnapshot` currently places only commandable Entities in `routes`.
Refresh lookup must include all current registered Entities with their
`GetProperties`, including read-only sensors. Add a separate Adapter-owned
refresh-route map to `routeSnapshot` and `runtimeCoordinator`; do not make
read-only Entities commandable or add translators to them.

The coordinator admits refresh work through its existing event loop, snapshots
the current MQTT connection, generation, route revision, friendly name, and
Entity plan, then runs blocking publication as a tracked effect outside the
loop. Use exactly one existing `publishGet` call, QoS 1 and non-retained, with all
properties of the selected Entity plan. Use the invocation deadline and a
generation-scoped cancellation context. Do not keep work for a later connection,
silently resolve it to a replacement route, or add an application-level retry.

| Situation | Adapter outcome |
| --- | --- |
| Malformed canonical Entity ID | `not_executed / invalid_entity_id` |
| Entity absent from this Adapter's current inventory | `not_executed / entity_unavailable` |
| Entity has no `GetProperties` | `not_executed / refresh_unsupported` |
| No usable current connection, or invalidated route before the effect starts | `not_executed / adapter_unavailable` |
| Matching current-generation MQTT PUBACK before deadline | `succeeded` with `{entity_id,request_status:"published"}` |
| Publish error, timeout, or route/generation loss after possible publication | `unknown / refresh_outcome_unknown` |

The effect checks cancellation before calling MQTT. A route/generation change
cancels unfinished work; once publication may have begun it cannot establish
non-execution. A late PUBACK after invalidation must not become a successful
current-generation result. MQTT QoS 1 itself permits duplicates, so one
application publication does not promise exactly one physical request.

No `/set`, Entity Command, CommandEvidence capability, matcher, synthetic
Observation, or synthetic State is created. Refresh does not claim Entity
availability or enablement. A disabled Entity may still be asked for data;
ordinary Observation admission remains Core's existing policy. Read-only and
disabled Entities therefore need no fake control capability.

Real device reports continue through existing ordinary Observation processing.
An already-active Command may still claim a matching fresh report under its
existing matcher policy; refresh neither changes that policy nor invents a new
causation guarantee. Function success never waits for State projection or
guarantees that a device will answer.

Expose the descriptor/handler from `internal/adapters/zigbee2mqtt/functions.go`.
`internal/app/zigbee2mqtt/run.go` starts and supervises the FunctionServer alongside
the existing Adapter runtime and Command server. For normal process shutdown,
close function admission and join its handlers while the runtime and MQTT
connection remain available, then cancel and join the existing runtime and
Command server, and finally close the Session. The supervisor owns the component
contexts so parent cancellation cannot reverse that order.

Connection loss or an Adapter runtime failure can interrupt refresh work before
this graceful sequence. Cancel the affected generation, classify possible work as
unknown, and join tracked refresh effects before Adapter shutdown completes.
Do not require MQTT to remain usable after an actual loss. Preserve the existing
connection coordinator's cleanup behavior rather than redesigning all connection
generations for this function. Update supervisor component accounting and its
cancellation tests instead of launching an untracked goroutine.

## 10. Core assembly and shutdown

Construct Adapters and Devices repositories independently with the shared DB
handle. Construct `adapters.Service`, then inject its narrow Adapter reader into
`devices.Service` for existing preliminary scope checks. Neither repository
receives the other repository or a shared transaction. Notifications retains its
own repository and existing transaction-local canonical runtime/health reads.

Establish the health foundation before public/runtime serving: provision the
separate health stream and Devices durable, bootstrap/queue immutable health
heads, and start the Adapters health relay and Devices consumer. Wire the existing
health supervisor to Adapters as the sole expiry authority. Current availability
reads may lag; readiness does not promise that the consumer has caught up. See
the companion's lifecycle and recovery contract.
The public readiness checker additionally composes health-delivery checks, but
the health supervisor keeps its existing base readiness dependency. Projection
failure must not disable the source's lease-expiry decisions.

For stage two, construct the NATS function invoker and `adapters.FunctionService`
from the Adapters repository in `internal/app/hearthd/run.go`. Runtime management
does not depend on the function service. Run invocation recovery before public
function admission.
Register Huma/MCP operations in `http_handler.go`, before building the built-in
agent's fixed MCP bridge. Start catalog registration with the other request/reply
transports and include it in the existing transport drain/join ownership.

The extracted Adapters API package defines its consuming `Functions` interface
with only the five discovery/invocation/history methods above. Its separate
`Adapters` interface contains only the three existing Adapter management reads.
Neither interface requires registration,
retention, or shutdown methods. The application supplies admission readiness
separately, following the existing Command/Automation checker pattern. Assembly
changes follow the existing registration shape:

```diff
 // internal/app/hearthd/http_handler.go, target after both stages
 func newMCPServer(
     devices devicesapi.Devices,
     automations automationsapi.Automations,
     notifications notificationsapi.Notifications,
+    adapters adaptersapi.Adapters,
+    functions adaptersapi.Functions,
     logger *slog.Logger,
 ) *mcpapi.Server {
@@
     notificationsapi.RegisterMCP(mcpServer, notifications)
+    adaptersapi.RegisterMCP(mcpServer, adapters)
+    adaptersapi.RegisterFunctionsMCP(mcpServer, functions)
     return mcpServer
 }
@@
     notificationsapi.Register(v1, notifications)
+    adaptersapi.Register(v1, adapters)
+    adaptersapi.RegisterFunctions(v1, functions)
     agent.Register(v1, agentOperations)
```

Pass the Adapter management service, function service, and function admission
checker explicitly into `newHTTPHandler`, and update its callers and fixtures.
No package-global registry or service locator is introduced.

At shutdown:

1. Close public function admission alongside existing public admission gates.
2. Stop admitting and join agent turns under the existing order. A tool invocation
   already admitted to the function service continues independently to its
   deadline; a later call by a draining turn receives `admission_closed`.
3. Join admitted function invocations before canceling dependency contexts or
   closing NATS/SQLite. The current Automation and notification drain ordering
   stays intact; Automations do not call Functions in this scope.
4. Drain/join function registration with the other request/reply servers. Preserve
   health ingress while admitted Adapter-dependent work still needs it.
5. Stop the health supervisor and close/join runtime ingress,
   drain/cancel/join the health relay, then drain/stop and join
   the Devices health consumer. Pending outbox/messages remain for recovery.
6. Stop the shared history worker and withdraw dependencies under existing rules.
   Keep the existing Device Fact, Observation, Entity Event, Automation, and
   notification dependency order; the companion defines health worker deadlines.

Use `lifecycle.AdmissionGroup`, not another active-counter implementation. The
service admission gate participates in `/readyz` alongside other public
operation gates. Operation-scoped write failures do not become remembered
readiness failures. Function availability does not become a Core startup or
readiness dependency. Having no registered functions or an offline Adapter is
normal.

### Lifecycle and transport comparisons

Start from [the platform guide](../docs/platform.md). These concrete counterparts
establish which mechanics to reuse and which policies remain different:

| Proposed responsibility | Existing counterpart | Reuse and deliberate difference |
| --- | --- | --- |
| Function admission and completion | `notifications.Service.send` and `Drain` in `internal/modules/notifications/service.go`; `lifecycle.AdmissionGroup` | Reuse reservation tracking and joining without canceling admitted work. Functions add revision/schema preparation and descriptor-specific deadlines, not another active counter or notification-specific eligibility rule. |
| Catalog registration server | `StartRegistrationServer` in `internal/modules/notifications/nats/registration.go`; `platformnats.RequestReplyServer` | Reuse activation, envelope/reply framing, trace propagation, callback admission, and joining. The Adapters callback owns catalog preparation, route checks, fencing, and rejection mapping. `Drain()` alone does not join; assembly must also call `Close(ctx)`. |
| SDK Function server | `Session.StartNotificationServer` and `NotificationServer` in `sdk/adapter/notifications.go` | Follow flushed activation, concurrent handlers, Session ownership, and actual handler joins. Immutable catalogs and dynamic schemas are Function policy. Do not substitute the Core request/reply primitive, which is not a concurrent SDK handler lifecycle. |
| Invocation pruning | `HistoryPruner` in `internal/app/hearthd/history_pruning.go`; `lifecycle.WorkerHandle` | Add one module-owned pass to the existing schedule. No Function timer/worker; the application owns cancellation and joining, and Adapters owns retention/deletion policy. |
| Health delivery prerequisite | Section 9 of [the companion spec](adapter-health-projection.md) | Reuse the specified WorkerHandle/Consumer mechanics and module-owned relay/acknowledgement policy, not a second generic framework. |

On normal and error exits, contexts that own admitted work must survive until
that work joins. A wait timeout ends only the wait. Neither the application nor
the SDK may close dependencies on the assumption that cancellation, an expired
reply, or a nonblocking drain proves completion.

## 11. Project layout and ownership

The tree lists proposed ownership, not a mandatory file-per-type rule. Group
small cohesive declarations; retain the module boundary and named responsibilities.

```text
contracts/
├── functionschema/                         # new, D1: shared bounded dynamic-schema validation
│   ├── schema.go
│   ├── schema_test.go
│   └── fuzz_test.go
└── v1/                                    # modify, D1: strict language-neutral wire contract
    ├── adapter-health-update.schema.json   # new, H1: independent Core-only health snapshot
    ├── adapter-function-registration-request.schema.json   # new
    ├── adapter-function-registration-response.schema.json  # new
    ├── adapter-function-invoke-request.schema.json         # new
    ├── adapter-function-invoke-response.schema.json        # new
    ├── common.schema.json
    ├── embed.go
    └── function_schemas_test.go             # new
sdk/adapter/
├── functions.go                           # new, D2: public descriptors/results and Session API
├── function_server.go                     # new, D2: subscription, callbacks, deadlines, joining
├── functions_test.go                      # new, D2: SDK protocol/lifecycle evidence
├── session.go                             # modify, D2: Session-owned function state
└── lifecycle.go                           # modify, D2: close/join Session-owned function server
internal/
├── contracts/v1/natswire/subjects.go        # modify, D1: exact subjects and parsed routes
├── modules/
│   ├── adapters/                          # new module, R1 to R3, then D3/D4
│   │   ├── README.md                      # new, R3: ownership and NATS publication contract
│   │   ├── ids.go                         # move Adapter runtime ID/parser, R1
│   │   ├── health.go                      # move Adapter types; add HealthSnapshot/revision, R1
│   │   ├── health_updates.go              # new, R2: immutable heads and publication-store contract
│   │   ├── runtime.go                     # move runtime/health use cases and validation, R1
│   │   ├── read.go                        # move Adapter management reads, R1
│   │   ├── repository.go                  # move/narrow runtime and management store interfaces, R1
│   │   ├── service.go                     # new composition of extracted runtime use cases, R1
│   │   ├── function_types.go              # new, D3: catalog, snapshot, evidence variants
│   │   ├── function_catalog.go            # new, D3: descriptor preparation and discovery
│   │   ├── function_invocation.go         # new, D3: validated preparation and bounded execution
│   │   ├── function_service.go            # new, D3: function dependencies, admission, drain, recovery
│   │   ├── function_retention.go          # new, D3: thirty-day invocation retention
│   │   ├── function_service_test.go       # new, D3: distinct workflow fault checks
│   │   ├── sqlite/
│   │   │   ├── repository.go              # new, R2: independent Adapters repository
│   │   │   ├── runtime.go                 # move, R2: Adapter-only writes + head/outbox transaction
│   │   │   ├── health_updates.go          # new, R2: queue/read/delete publications and startup sync
│   │   │   ├── reads.go                   # move Adapter-only reads/mapping, R2
│   │   │   ├── runtime_test.go            # move relevant runtime tests, R2
│   │   │   ├── functions.go               # new, D3: catalog/admission/outcome transactions
│   │   │   ├── functions_test.go          # new, D3: real SQLite function evidence
│   │   │   ├── dbqueries/
│   │   │   │   ├── runtime.sql            # move Adapter-owned queries from health.sql, R2
│   │   │   │   ├── health_updates.sql     # new, R2: health heads/outbox queries
│   │   │   │   └── functions.sql          # new, D3: catalog and invocation queries
│   │   │   └── dbsqlc/                    # generated, R2/D3
│   │   ├── nats/
│   │   │   ├── session.go                 # move from devices/nats, R3
│   │   │   ├── session_wire.go            # move only Session DTO/mapping, R3
│   │   │   ├── session_test.go            # move runtime transport tests, R3
│   │   │   ├── health_relay.go            # new, H2: publication retries and snapshot-sync wakes
│   │   │   ├── health_resources.go        # new, H1: exact stream provisioning/validation
│   │   │   ├── health_updates_test.go     # new, H2: real JetStream publication/recovery
│   │   │   ├── function_registration.go   # new, D3: shared request/reply server consumer
│   │   │   ├── function_invoker.go        # new, D3: one runtime-pinned request
│   │   │   └── functions_test.go          # new, D3: actual NATS + public SDK
│   │   └── api/
│   │       ├── register.go                # move Adapter HTTP registrations, R3
│   │       ├── adapters.go                # move Adapter handlers/models/mapping, R3
│   │       ├── health_history.go          # move Adapter-only history handling, R3
│   │       ├── mcp.go                     # move Adapter management tools/resources, R3
│   │       ├── functions.go               # new, D4: function Huma routes/handlers
│   │       ├── function_models.go         # new, D4: exact function DTO alternatives
│   │       ├── functions_mcp.go           # new, D4: fixed function tools/resources
│   │       └── functions_test.go          # new, D4: HTTP/MCP validation and parity
│   └── devices/                           # modify, R1 to R3: remove Adapter-wide ownership
│       ├── README.md                      # R3: own projection; applied history versus control gate
│       ├── model.go                       # R1: remove RuntimeID definition; use adapters.RuntimeID
│       ├── health.go                      # R1: retain Entity-only types
│       ├── health_reads.go                # R1: retain Entity availability history
│       ├── adapter_health.go              # new, H2: domain-only health-update input/store/use case
│       ├── repository.go                  # R1: narrow AdapterReader and availability-history stores
│       ├── service.go                     # R1: remove runtime stores; inject AdapterReader
│       ├── registration.go                # R1: use injected reader and Adapter-owned IDs/errors
│       ├── ownership.go                   # R1: same cutover for owned-mapping checks
│       ├── sqlite/
│       │   ├── availability.go            # H2: revision-stamped reports and effective history
│       │   ├── adapter_health.go          # new, H2: own projection/invalidation/history transaction
│       │   ├── entity_mapping.go          # H2: read Devices-owned effective availability only
│       │   ├── health_mapping.go          # R2: retain Entity mapping only
│       │   ├── entity_availability_history.go # R2: Entity-only portion moved from adapter_reads.go
│       │   ├── dbqueries/health.sql        # H2: projection/effective/report writes and fencing reads
│       │   ├── dbsqlc/                    # regenerate, R2
│       │   └── health_test.go             # H2: generation races, duplicate/history and lag behavior
│       ├── nats/wire.go                   # R3: remove only moved Session DTOs
│       ├── nats/adapter_health.go         # new, H2: durable consumer, Ack only after own commit
│       ├── nats/adapter_health_resources.go # new, H1: Devices-owned durable configuration
│       └── api/                           # R3: remove Adapter routes/tools/resources exactly once
├── platform/db/migrations/
│   ├── 00015_adapter_health_projection.sql # new, R2/H2: ownership-specific state and view upgrade
│   └── 00016_adapter_functions.sql         # new, D3: additive function schema; verify sequence
├── app/hearthd/
│   ├── run.go                             # modify, R3/D5: runtime then function assembly
│   ├── http_handler.go                    # modify, R3/D5: management cutover then function APIs
│   ├── shutdown.go                        # modify, R3/D5: Session transport then function drain
│   ├── history_pruning.go                 # modify, D5: append module to shared sweep
│   ├── health_supervisor.go               # verify wiring, R3: same worker, Adapters service
│   ├── health_supervisor_test.go          # adapt/retain, R3: expiry timing and recovery grace
│   ├── health_projection_readiness.go     # new, R3: derived readiness, separate from expiry gate
│   ├── adapters_extraction_test.go        # new only for gaps, R3: cross-module compatibility
│   ├── adapter_health_projection_test.go  # new, R3: lag, bootstrap, recovery/drain
│   ├── adapter_health_reconnect_test.go   # new, R3: pending delivery without fresh snapshots
│   └── adapter_functions_integration_test.go # new, D5: recovery, drain, agent discovery
├── adapters/zigbee2mqtt/
│   ├── functions.go                       # new, D6: refresh_entity descriptor and handler
│   ├── runtime_functions.go               # new, D6: coordinator refresh effects and completion
│   ├── runtime.go                         # modify, D6: events, tracked work, shutdown joining
│   ├── runtime_routes.go                  # modify, D6: generation/route invalidation
│   ├── reconcile.go                       # modify, D6: all-Entity refresh routes
│   ├── functions_test.go                  # new, D6: independently specified refresh behavior
│   └── mqtt_integration_test.go            # extend, D6: real broker publication evidence
└── app/zigbee2mqtt/
    ├── run.go                             # modify, D6: function-server supervision
    └── run_cancellation_test.go            # extend, D6: function drain before runtime cancellation
sqlc.yaml                                  # modify, R2/D3: Adapters-owned query generation
README.md                                  # modify, D7: Zigbee2MQTT refresh and guide link
GLOSSARY.md                                # modify, R3/D7: projected availability then Function terms
docs/
├── adapter-health-projection.md            # new, R3: lag, applied history, recovery/upgrade guide
├── adapter-functions.md                    # new, D7: operator and Adapter author guide
├── architecture.md                        # modify, R3/D7: ownership then opaque function boundary
└── platform.md                            # modify, R3/D7: ownership and lifecycle/navigation
specs/adapter-health-projection.md          # companion contract for R1/H1/R2/H2/R3
```

The Zigbee2MQTT setup guide is the `README.md` section headed `Zigbee2MQTT adapter`.
Add refresh usage there and link to the new function guide. Existing SDK clients,
Entity-type manifests, generated Entity facades, vendor config structs, and
frontend types do not change for the generic framework.

The tree groups moved tests by responsibility rather than asking for duplicates
in their old and new owners. Other Devices/Automations/notification consumers of
Adapter types, transport error mapping, fixture setup, and application assembly
must be updated in the same extraction. Existing public SDK clients and frontend
API consumers retain their contracts. Do not refactor vendor behavior, generic
platform lifecycle machinery, or unrelated Device query layout during extraction.

For each implementation PR, record its actual base and changed/untracked scope,
then review every changed handwritten file against the coding standards. Give
each file one responsibility and a keep/rename/move/split verdict. The tree is
not evidence that those future files were reviewed or that each proposed split
is necessary. Keep source-specific preparation beside its use case or transport;
put shared pure rules with their domain owner. File length and shared prefixes
alone justify neither a package nor a service split.

Review test placement with that inventory. Pure schema/domain checks stay beside
their owner, SQLite proofs beside persistence, and wire/API checks beside the
transport. Preserve cohesive application-level transport-parity, recovery, and
shutdown-order tests. Name shared fixtures `*_helpers_test.go` or
`*_fixtures_test.go`; do not move them into production code for test access.

Generated `adapters/sqlite/dbsqlc` and changed Devices query output are reviewed
through their module-owned `dbqueries`, the shared migrations, and root
`sqlc.yaml`. Run generation and include intended output, never hand-edit generated
rows to satisfy a new interface. D1 schemas and embedded registration are
handwritten contract sources, not generated Go model substitutes.

R3 must update `devices/README.md`, the new `adapters/README.md`, architecture,
and platform navigation together. Replace the old Devices-owned runtime and
atomic health-fan-out statements rather than merely adding a conflicting section.
D7 adds Function ownership, API/SDK usage, and retention links. Keep the planned
operator guides and spec links accurate after any accepted filename change.

## 12. Deliverables and acceptance

The revised total is XL, approximately 3 to 5 weeks with low-to-medium confidence.
Module extraction plus NATS projection, recovery, migration, and lifecycle work
replace the smaller behavior-preserving extraction estimate. Stage one now
includes an approved semantic change, not only file moves. Estimates include
tests and review, not deployment promises. Validate that foundation before
starting stage two's function behavior.

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
| --- | --- | --- | --- | --- | --- |
| R1 | Extract canonical Adapter domain types/runtime use cases and management reads; migrate internal consumers | L | `internal/modules/adapters/{ids,health,runtime,read,repository,service}.go`, Devices domain/store changes, affected internal consumers | None | E1, E3 |
| H1 | Define the strict health snapshot wire/subject and provision the independent WorkQueue stream/durable contract | L | `contracts/v1/adapter-health-update.schema.json`, `natswire`, Adapters `nats/health_resources.go`, Devices `nats/adapter_health_resources.go`, companion spec | R1 | E2, E6 |
| R2 | Move Adapter persistence ownership; atomically commit its own health head/outbox, with no Devices writes | L | `internal/modules/adapters/sqlite`, Adapter half of projection migration, `sqlc.yaml` | R1, H1 | E1, E2, E4, E6 |
| H2 | Deliver and apply revisioned health snapshots; own effective availability and applied history in Devices | XL | Adapters health publication store/relay, Devices domain/SQLite/NATS projection and report/view changes, Devices half of migration | R2, H1 | E2, E5, E6 |
| R3 | Cut over Session/management transports, supervisor, projection lifecycle, startup synchronization, durable reconnect recovery, docs, and compatibility tests | L | `internal/modules/adapters/{nats,api}`, Devices removals, `internal/app/hearthd`, module READMEs, `GLOSSARY.md`, `docs/architecture.md`, health operator guide | R1, R2, H1, H2 | E1, E2, E3, E4, E5, E6 |
| D1 | Publish bounded schema profile and strict registration/invocation wire contracts | L | `contracts/functionschema`, `contracts/v1`, `internal/contracts/v1/natswire` | R3, extraction gate passed | A1, A2 |
| D2 | Expose Session-owned immutable catalog registration and concurrent function serving | L | `sdk/adapter/functions.go`, `function_server.go`, Session cleanup and SDK tests | D1 | A2, A3, A8 |
| D3 | Persist catalogs and invocations, fence admission, route once, retain/recover evidence | L | Function files in `internal/modules/adapters`, its `sqlite`/`nats`, new migration, `sqlc.yaml` | D1, D2 | A2, A4, A5, A6, A7, A9 |
| D4 | Expose matching HTTP/MCP discovery, invocation, history, and resources | L | `internal/modules/adapters/api/functions.go`, `function_models.go`, `functions_mcp.go`, tests | D3 | A1, A5, A10 |
| D5 | Assemble startup, fixed agent tools, readiness, shutdown, and shared pruning | L | `internal/app/hearthd` and integration tests | D2, D3, D4 | A6, A7, A8, A9, A10 |
| D6 | Ship real Zigbee2MQTT refresh_entity without synthetic Commands or State | L | `internal/adapters/zigbee2mqtt`, `internal/app/zigbee2mqtt` | D2, D5 | A11, A12 |
| D7 | Document extension ownership, safe payloads, upgrade/rollback, and refresh usage; validate integrated change | M | `README.md`, `GLOSSARY.md`, relevant `docs/`, all changed sources/generated artifacts | D1 to D6 | A13 |

### Acceptance criteria

The health foundation gate consists of E1 through E6. It must hold before any
Function endpoint, SDK serving method, or Function table is added. The new health
schema, migration, resources, and projection are part of this first stage:

- **E1, single ownership and preserved runtime behavior:** Adapters owns the
  canonical runtime type/parser, claim/heartbeat/release/expiry use cases, Adapter
  health, management reads, and their public transports. Devices has no duplicate
  runtime service, permanent forwarding APIs, or second expiry worker. A live
  notification-only Adapter can claim, report health, register a destination,
  send, and release without registering Entities. Claim retry/takeover, fencing,
  heartbeat timing, source/reason transitions, and supervisor grace remain as
  specified by the existing contracts.
- **E2, separate ownership and durable delivery:** A failed health-head/outbox
  insert rolls back the Adapters mutation without touching Devices rows. A
  committed mutation survives relay failure and reaches the separate health
  stream at least once. Devices commits projection revision, eligible report
  invalidation, effective current rows, and history in its own transaction before
  Ack. A failed Devices commit never rolls back already-committed Adapter health
  or advances the projected revision. Redelivery applies effects once. No peer
  module SQL transaction API or cross-owned-table write is introduced.
- **E3, compatibility with the explicit consistency change:** Existing public SDK
  Session and notification/device input contracts work before Functions are
  added. Adapter management shapes, operation IDs, MCP tools/resources, existing
  NATS subjects/schema IDs, deadline/lease values, and cursor bytes/scope remain
  compatible. Entity availability response shapes remain compatible, but tests
  assert projection-only reads and applied-order history rather than the removed
  synchronous fan-out. Existing IDs and retained history/cursors survive the
  additive migration without rewriting old evidence.
- **E4, executable extraction checkpoint:** Module generation, focused
  runtime/availability/notification/transport checks, existing Core restart and
  shutdown checks, and full repository validation are run before D1. Inspect
  generated diffs and dependency direction. Existing cross-module read queries
  keep their atomic fencing; generated sqlc types do not cross the new module
  boundary. Do not weaken guards, remove meaningful tests, or change wire
  contracts to make the extraction pass. Report pre-existing validation blockers
  separately; new regressions must be fixed before proceeding.
  The review includes boundary enforcement, concrete platform comparisons, and
  the complete handwritten/generated ownership inventory described in section 11.
- **E5, lag, generations, and applied history:** Pause the health consumer after a
  canonical unhealthy commit. Adapter management immediately reflects the new
  health and Command admission rejects under its existing rule, while Entity
  availability remains at the prior projection until delivery. New reports are
  stamped with authoritative health revision, but effective reads use the local
  projection only. Older/duplicate health messages cannot erase a newer report,
  reverse current state, or add duplicate/obsolete history. Higher snapshots may
  skip revisions without fabricating intermediate Entity transitions. New
  effective history is ordered by Devices application, not source commit time.
- **E6, health recovery and lifecycle:** Startup snapshot publication
  repairs current projection using immutable heads and fresh notification IDs,
  without advancing source revision or duplicating history. Broker capacity
  rejection leaves work in the outbox and no unconsumed update expires by age.
  NATS reconnect delivers pending outbox/consumer work without queuing new
  snapshots. Delivery-state loss requires Core restart for synchronization.
  Legacy report backfill/bootstrap follows the companion contract. Shutdown
  closes health producers, joins relay/consumer work before dependencies, and
  preserves unacknowledged work. Existing SDK heartbeat/lease/grace/fencing rules
  remain unchanged. No claim of complete historical replay or zero projection lag
  is made.
  A health-delivery readiness failure does not suspend canonical lease expiry
  under otherwise usable existing source dependencies.

Function acceptance remains A1 through A13:

- **A1, schema and input safety:** Valid bounded schemas and object values pass;
  unsupported keywords, references, duplicate keys, invalid UTF-8, trailing JSON,
  invalid numeric constraints, excessive nesting/count/size, unknown outer
  fields, and null required inputs reject before registration or handler effects.
  Valid values preserve JSON number precision across preparation and persistence.
- **A2, registration identity and fencing:** A lost registration reply followed by
  retry establishes one catalog. Functions can be invoked in the commit-before-
  reply window. Stale runtime writes and same-runtime catalog changes reject
  atomically. A new runtime cannot receive calls until it registers; its complete
  replacement removes omitted functions and leaves old invocation snapshots intact.
- **A3, SDK serving:** Concurrent requests, including the same key, reach their
  handlers without a fixed cap or implicit serialization. Unknown keys, wrong
  revisions, expired requests, fenced Sessions, panics, malformed results, and
  closed servers have the specified outcomes without raw error leakage.
- **A4, durable one-shot admission:** External work is impossible when admission
  fails. Revision/runtime changes racing admission cannot retarget a call or mix
  descriptors. Once admitted, at most one application dispatch is attempted;
  caller disconnect never triggers a retry or prevents timely outcome evidence.
- **A5, outcome semantics:** Each terminal variant survives detail/history reads.
  No-responders is not-executed; possible execution followed by timeout, lost
  response, invalid output, or wrong response identity is unknown. Late replies
  cannot replace terminal evidence. Success requires a valid result and committed
  terminal record.
- **A6, recovery:** Restart after admission or dispatch terminalizes running
  evidence as unknown and causes zero re-executions. Persisted active catalogs
  remain usable after Core-only recovery without SDK re-registration.
- **A7, scoped storage faults:** Admission failure sends nothing. Outcome-write
  failure returns an internal error with the invocation ID, leaves no fabricated
  terminal evidence, and does not poison readiness or later independent work.
- **A8, lifetime:** Shutdown and server/Session close stop new admission and join
  actual admitted handlers/invocations before closing dependencies. A timed-out
  join or expired handler context does not falsely claim the handler exited.
- **A9, discovery and retention:** Offline registered catalogs remain visible but
  non-callable; upstream-unhealthy live runtimes remain callable. Unknown Adapter
  differs from empty catalog/history. Cursor scope and ordering hold across
  catalog replacement and pruning. Only terminal invocations older than thirty
  days are pruned by the shared worker; current catalogs and running evidence stay.
- **A10, API/MCP/agent parity:** HTTP, tools, and resources expose the same service
  evidence and exact null/omission semantics. The built-in agent can discover and
  invoke a function registered after its tool bridge was created, without a tool
  catalog refresh or per-function registration.
- **A11, refresh publication:** For a controllable Entity and a read-only gettable
  sensor, one invocation publishes the exact planned properties once to the
  correct `/get` topic with QoS 1/non-retained. In an isolated refresh with no
  active Command, no `/set`, Command row, linked Observation, synthetic State, or
  matcher is introduced. Success occurs only on
  PUBACK and does not wait for or promise a device report.
- **A12, refresh invalidation and regression:** Unknown, foreign, non-gettable, or
  unavailable routes reject without publication. A route/generation change or
  deadline after possible publication yields unknown without reconnect replay.
  A blocked refresh cannot block the coordinator, and existing Command ordering,
  command-linked outcome matching, State ingestion, and shutdown joining hold.
- **A13, integrated delivery:** Additive migration preserves existing data;
  generated query changes match sources; operator examples demonstrate
  discover/describe/invoke/history; no credentials appear in fixtures or outputs;
  focused checks and repository validation pass or report concrete environmental
  blockers without claiming unrun behavior.
  Complete the section 11 structural review and section 6.4 boundary checks for
  actual changed files, including production validator callers and preserved
  Merge Patch behavior. Report unresolved questions instead of claiming that
  generation or passing tests proves appropriate organization.

## 13. Verification plan

The preceding contracts are the independent oracles. Assign each behavior one
primary test owner; add another layer only for a distinct transport, transaction,
or lifecycle failure it can uniquely expose. Do not test source text, copy a
production descriptor into an assertion, or use mocks that supply the very
fencing/commit behavior under test.

| Acceptance | Primary check and expected evidence | How to run | Prerequisites |
| --- | --- | --- | --- |
| E1 | Existing runtime behavior cases moved to the new owner, plus an old public-SDK notification-only Session through actual NATS and Core | `GO_PACKAGES='./internal/modules/adapters/... ./internal/modules/notifications/... ./sdk/adapter ./internal/app/hearthd' mise run --skip-deps test` | Embedded NATS, migrated temporary SQLite |
| E2 | Real migrated SQLite and JetStream. Abort Adapter outbox insertion and Devices projection writes in separate tests; lose PubAck/Ack and verify independent rollback, durable redelivery, and one applied history effect | `GO_PACKAGES='./internal/modules/adapters/sqlite ./internal/modules/adapters/nats ./internal/modules/devices/sqlite ./internal/modules/devices/nats ./internal/app/hearthd' mise run --skip-deps test` | Embedded JetStream and real module repositories; no cross-module transaction fixture |
| E3 | Official MCP client, HTTP/OpenAPI checks, existing SDK inputs, additive migration, old history/cursor fixtures, and changed availability timing assertions exercise the declared compatibility boundary | `GO_PACKAGES='./internal/modules/adapters/api ./internal/modules/adapters/nats ./internal/modules/devices/api ./internal/modules/notifications/... ./internal/app/hearthd' mise run --skip-deps test` | Existing fixtures captured before extraction; no live model needed |
| E4 | Inspect module imports, query/generated ownership, schema/migration diff, and stage-one test results. Run generation and full validation before any Function code is added | Focused stage-one command below, `mise run --skip-deps generate`, then `mise run validate` | Repository dependencies, reachable Docker for existing Mosquitto checks |
| E5 | Deterministically pause the real health consumer, commit source health, attempt a Command, read projected availability, then release delivery. Reorder full snapshots around fresh reports and inspect current/effective history | `GO_PACKAGES='./internal/modules/devices/... ./internal/app/hearthd' mise run --skip-deps test` | Embedded JetStream, shared migrated test DB, controlled barriers |
| E6 | Restart Core/relay/consumer against retained stores, reconnect with pending outbox work without queuing fresh snapshots, repeat startup snapshot synchronization, exercise controlled stream capacity, legacy migration, and shutdown barriers | `GO_PACKAGES='./internal/modules/adapters/... ./internal/modules/devices/nats ./internal/app/hearthd' mise run --skip-deps test` | Embedded JetStream, retained temporary DB, real resource conformance tests |
| A1 | Public schema compile/validate boundary with hand-authored valid/invalid documents; wire conformance fixtures; Huma/MCP null and body-limit cases | `GO_PACKAGES='./contracts/functionschema ./contracts/v1 ./internal/modules/adapters/api' mise run --skip-deps test` | Go toolchain |
| A2, A3 | Actual embedded NATS plus public SDK; withhold registration response after repository commit and invoke before retry acknowledgement; stale and changed catalog cases prove no partial writes | `GO_PACKAGES='./sdk/adapter ./internal/modules/adapters/nats' mise run --skip-deps test` | Embedded NATS fixture, temporary SQLite |
| A4, A5, A7 | Real SQLite admission/immutable-terminal transactions plus workflow faults; count actual Adapter handler entries and inspect retained rows after caller cancellation and lost/malformed replies | `GO_PACKAGES='./internal/modules/adapters/...' mise run --skip-deps test` | Temporary SQLite and embedded NATS |
| A6, A8, A9 | Core integration harness reuses one database/broker across Core restart; barriers block handlers and commits to establish join order; drive pruning time explicitly without sleeping thirty days | `GO_PACKAGES='./internal/app/hearthd' mise run --skip-deps test` | Existing Core fixtures and temporary dependencies |
| A10 | Official MCP client and HTTP fixture invoke the same registered function; construct built-in agent MCP bridge before registering catalog and call its fixed tools | `GO_PACKAGES='./internal/modules/adapters/api ./internal/app/hearthd' mise run --skip-deps test` | Existing MCP/agent fixtures; no live model needed |
| A11, A12 | Adapter coordinator fixture records exact MQTT packets using independently specified input inventory; real-Mosquitto integration observes `/get`, QoS, retain flag, deadline/invalidation behavior, and unchanged Command semantics | `GO_PACKAGES='./internal/adapters/zigbee2mqtt ./internal/app/zigbee2mqtt' mise run --skip-deps test` | Reachable Docker daemon for real Mosquitto; tests fail without it |
| A13 | Upgrade a pre-feature temporary database, register/invoke/read/prune, inspect generated diff, then all repository checks | `mise run --skip-deps generate`, focused tasks below, then `mise run validate` | Docker, Go, Node/pnpm, and repository tool dependencies |

Use deterministic synchronization or `testing/synctest` for concurrent admission,
deadline, and shutdown tests. The repository test task already enables the race
detector. Demonstrate sensitivity for the highest-risk contracts with isolated
negative controls where practical, such as permitting a stale runtime, skipping
the initial commit, or accepting an invalid output. Never inject deliberate faults
into files containing unrelated changes. A negative control is verification work,
not a new permanent production seam.

At the Adapters/health projection checkpoint, before implementing Functions:

```bash
GO_PACKAGES='./internal/modules/adapters/... ./internal/modules/devices/... ./internal/modules/notifications/... ./internal/app/hearthd ./sdk/adapter/...' mise run --skip-deps test
mise run --skip-deps generate
mise run validate
git diff --check
```

Then implement function behavior, use the focused package sets above, and run:

```bash
mise run --skip-deps generate
mise run --skip-deps format
GO_PACKAGES='./contracts/... ./sdk/adapter/... ./internal/modules/adapters/... ./internal/modules/devices/... ./internal/modules/notifications/... ./internal/adapters/zigbee2mqtt ./internal/app/zigbee2mqtt ./internal/app/hearthd' mise run --skip-deps lint
GO_PACKAGES='./contracts/... ./sdk/adapter/... ./internal/modules/adapters/... ./internal/modules/devices/... ./internal/modules/notifications/... ./internal/adapters/zigbee2mqtt ./internal/app/zigbee2mqtt ./internal/app/hearthd' mise run --skip-deps vet
mise run validate
git diff --check
```

Review generated, formatting, and module metadata changes before committing.
These commands are the implementation validation plan, not evidence that this
draft feature has been implemented or exercised.

A deployed Zigbee2MQTT smoke test is optional release evidence, not a dependency
for writing or approving this spec: start Core and the updated Adapter against
an operator-managed bridge, discover a gettable Entity, invoke using the returned
revision, inspect `/get` and invocation history, and distinguish PUBACK from later
State. Do not send radio requests or change household configuration without
operator authorization. Any simulator-based manual check must use
`mise run simulator-start` and `mise run simulator-stop`; no simulator feature
changes are required here.

## 14. Risks, trade-offs, and completion

| Risk | Likelihood / impact | Mitigation |
| --- | --- | --- |
| Projection lag is mistaken for authoritative health | High / high | Explicit projection-only read contract, unchanged canonical Command gate, documentation that readiness is not freshness, and paused-consumer behavior tests |
| Lost or stale health messages invalidate fresh reports or leave projection stuck | Medium / high | Atomic outbox/head writes, durable WorkQueue delivery, monotonic full snapshots, report revision stamps, Ack-after-commit, and startup synchronization |
| Ownership move changes existing management/SDK contracts or leaves duplicate paths | Medium / high | Extraction-first compatibility gate, old cursor/database fixtures, single registration per operation, migrated callers, and removal of old internal runtime ownership |
| Generic functions become a parallel canonical control API | Medium / high | Keep Entity Operations and notifications authoritative for existing modeled behavior; function-specific interpretation stays in Adapters |
| Client retries duplicate external effects | High / high | One-shot dispatch, explicit unknown outcome, durable history, and no automatic retry or idempotency promise |
| Custom schemas exhaust resources or silently drop assertions | Medium / high | Shared explicit profile, bounded documents/values, reject unknown keywords, no references or network loaders |
| Credentials leak through invocation or agent history | Medium / high | Non-secret contract, Adapter-local secrets, safe logs, explicit author/operator documentation; no claim that arbitrary data can be automatically scrubbed |
| Registration or shutdown races lose evidence or abandon work | Medium / high | Catalog identity known before registration, actual handler joins, AdmissionGroup, independent terminal-write context, restart tests |
| Refresh is mistaken for fresh canonical State | High / medium | Result says `published`; description and docs specify PUBACK only; no synthetic Command/Observation evidence |
| Deadline bounds are mistaken for forced cancellation | Medium / high | Core bounds its wait; SDK cancels context but joins actual handler work; document that external effects may outlive cancellation |

The fixed MCP tool set sacrifices direct per-function schemas in `tools/list` to
keep the existing agent bridge stable. Complete immutable Session catalogs avoid
hot-update races but require an Adapter restart to change descriptors. The bounded
schema profile intentionally favors ordinary structured function calls over a
full schema-composition language.

The ownership refactor removes Device-specific placement of a lifecycle already
used by notification-only Adapters. The approved semantic change removes the
cross-module atomic write invariant: Adapters commits its own state/outbox and
Devices later commits its own projection/history. This costs a health protocol,
outbox/relay, durable consumer, and revision/recovery rules, but preserves strict
write ownership without a shared transaction API or generic capability framework.

Success first requires passing the Adapters/health projection checkpoint under
the newly selected consistency contract.
Afterward, a useful new Adapter function can be added by changing only its
Adapter code and descriptor. It appears through existing fixed discovery tools,
is never automatically retried or replayed, and leaves truthful evidence without
a new SDK method or Core function-specific branch. The real `refresh_entity`
implementation demonstrates household use of the framework.

No blocking product questions remain after the planning answers. Implementation
must preserve the agreed ownership and execution semantics; new background-job,
secret-bearing, browser, or Automation requirements require separate scope
approval. This draft is ready for review, not yet approved for implementation.
