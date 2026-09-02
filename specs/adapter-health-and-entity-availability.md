# Adapter health and Entity availability implementation spec

**Status:** Implemented and verified
**Type:** Feature plan
**Effort:** XL, approximately 8-12 focused days, 65% confidence
**Approved by:** User design confirmation
**Date:** 2026-08-29
**Completed:** 2026-08-31
**Baseline:** branch `availability` at `88d4217`, plus the confirmed glossary, architecture, and ADR changes in this worktree

## Problem

Hearth discovers Adapter failure only when a Command receives no reply. Operators cannot distinguish a dead Adapter process from a live Adapter whose configured external system is unavailable, inspect outages before issuing a Command, or see Entity-specific reachability. The last accepted State and its timestamps remain useful but do not answer whether Hearth has a usable path now.

Adapter health is a core diagnostic requirement. Device health is not: a Device only groups independently addressable Entities, and mixed Entity conditions do not have one unambiguous Device result.

## Decision

Add one first-class Adapter health assessment and one Entity availability assessment:

```text
Adapter health:      unknown | healthy | unhealthy
Entity availability: unknown | available | unavailable
Device health:       not modeled
```

Adapter health combines two pieces of evidence under one result:

- runtime evidence, established by a fenced request/reply heartbeat lease; and
- a configured-external-system assessment, reported by Adapter code in each heartbeat.

The reported assessment is an input to Adapter health, not a separate persisted or exposed health model. Current health records whether Core or the Adapter supplied the effective evidence and retains Adapter source time only for Adapter-supplied evidence.

An unhealthy Adapter makes every owned Entity effectively unavailable. A healthy Adapter does not make an Entity available; the active runtime must explicitly report that Entity. Entity availability remains separate from enablement, State, and State freshness.

Availability is advisory when an owner is usable. A Command fails before dispatch when its owning Adapter is known unhealthy or has no active runtime. A Command is still attempted when Adapter health is unknown but an active runtime exists, or when the Entity is reported unavailable under a healthy Adapter. A fresh Adapter rejection may then return `entity_unavailable` without changing stored availability.

One Adapter instance permits one active runtime. The SDK generates a runtime ID before claiming and reuses it across retries. That runtime ID scopes the NATS subject for every post-claim Adapter-originated operation and every Command. Later payloads do not repeat the runtime ID; the subject is the authoritative wire source. A runtime ID is not Adapter identity or an authentication credential. The deployment's NATS account permissions remain the transport security boundary.

## Scope

Included work covers Adapter persistence, runtime lifecycle and fencing, explicit Entity availability, current and historical HTTP reads, and Command outcome changes. It also covers Core recovery, SDK lifecycle, first-party Adapter adoption, migration, code generation, runtime-scoped NATS subjects and Observation consumer provisioning, OpenAPI, and tests.

This feature does not add Device health, a `degraded` status, inferred availability, Adapter replication or forced takeover, authentication changes, Adapter disablement, or Binding ownership transfer. It also excludes automation, alerting, incident tracking, uptime metrics, UI or CLI work, configurable timing or limits, and history pruning. Entity ownership remains immutable.

## Behavioral contract

### Core readiness takes precedence

`/healthz` continues to report process liveness. `/readyz` remains the authoritative check for migrated SQLite, NATS connectivity, JetStream resources, and the active Observation consumer.

When Core readiness fails:

- the supervisor pauses lease expiry;
- stored Adapter health, Entity availability, and history remain unchanged;
- claim, heartbeat, release, and availability handlers are not gated by readiness and continue whenever their direct dependencies are usable; and
- callers must treat child health as non-authoritative while `/readyz` is not ready.

When readiness recovers, Core grants one lease-duration expiry grace. Existing runtimes may renew or release during that interval, and competing claims remain fenced. After the grace ends, the supervisor resumes its one-second expiry poll. A runtime still overdue when that poll commits creates the ordinary durable `unhealthy` transition.

Core readiness never overlays persisted Adapter or Entity reads and never invalidates Entity availability. A Core-only restart therefore requires neither a heartbeat-triggered refresh nor an availability resend.

### Adapter instance and runtime lifecycle

An Adapter instance is identified by its configured stable slug. `adapter.Connect` claims one active runtime before returning.

Claim behavior:

1. The SDK connects to NATS, generates one `run_` UUIDv7 runtime ID, and creates one `clm_` request envelope containing the Adapter ID, runtime ID, software name, and software version.
2. It retries that same envelope and runtime ID through transient no-response failures so a lost accepted response cannot create a second runtime.
3. Core creates the Adapter instance on its first claim, or loads the existing instance.
4. If the supplied runtime ID already exists with the same Adapter and software metadata and remains that Adapter's active runtime, Core accepts the retry without another write; ended or superseded runtime IDs are rejected permanently.
5. If another runtime remains active, Core rejects with transient `adapter_active` and its recorded lease expiry; the SDK waits and retries until its context ends or the supervisor expires that runtime.
6. Otherwise Core records the supplied runtime ID, sets health to `unknown` with `hearth.awaiting_health`, and acknowledges the claim. The protocol fixes the heartbeat interval at five seconds and the lease at fifteen seconds rather than returning those constants in every claim response.

A replacement process cannot evict an active runtime. Graceful release or supervisor-owned lease expiry ends the current runtime. A later process reuses the stable Adapter slug but generates a new runtime ID. Software name and version are bounded diagnostic evidence attached to that runtime, never Adapter identity.

Every Adapter-originated Registration, heartbeat, release, Entity availability report, Entity enablement request, and Observation uses a subject containing its Adapter and runtime IDs. Core parses the subject and verifies the active runtime in the same SQLite transaction as the requested write. Commands are sent on the subject for the runtime ID committed on the Command record.

A `runtime_fenced` request/reply rejection is terminal. The SDK stops Command serving and further publications, closes the session, and exposes `ErrRuntimeFenced`. It does not try to reclaim from the same process.

### Adapter heartbeat and health

Heartbeats use Core NATS request/reply every five seconds. The SDK serializes regular heartbeats and immediate health changes through one loop so older health cannot arrive after newer health from the same runtime.

Each heartbeat carries the Adapter's latest configured-external-system assessment:

- `unknown` before Adapter code has a result;
- `healthy` when the external system is usable; or
- `unhealthy` with a mandatory reason code.

Core receipt renews the lease to 15 seconds after receipt while the runtime remains active. The supervisor is the only code path that expires leases, so a heartbeat may renew an elapsed lease if its transaction commits before supervisor expiry. Registration, availability reports, enablement requests, Observations, Command traffic, and NATS connection presence never renew it. Adapter source time is diagnostic only. Core receipt order and Core-owned time determine current status and transition order.

Effective Adapter health is:

| Runtime evidence | Adapter report | Effective health | Effective reason |
|---|---|---|---|
| no runtime before first claim | none | `unknown` | `hearth.awaiting_runtime` |
| active runtime | `unknown` | `unknown` | `hearth.awaiting_health` |
| active runtime | `healthy` | `healthy` | none |
| active runtime | `unhealthy` | `unhealthy` | reported reason |
| supervisor-expired runtime | ignored | `unhealthy` | `hearth.heartbeat_expired` |
| graceful release | ignored | `unhealthy` | `hearth.stopped` |

A status or stable reason-code change appends one Adapter transition. Same status and reason refresh current evidence without appending history. Current health source is `adapter` after an accepted heartbeat and `core` after claim, release, or expiry; only Adapter-supplied evidence has `source_observed_at`.

`Session.SetHealth` updates the SDK's desired health immediately and waits for an acknowledged immediate heartbeat or caller cancellation. The desired value remains in memory after caller cancellation so the regular heartbeat loop can report it later. A Core-only readiness change does not change the SDK's desired health.

### Entity availability

Only an explicit report from the active, currently healthy owning Adapter sets Entity availability. Reports contain only `available` or `unavailable`; `unknown` is Core-derived.

For one Entity, effective availability is:

| Owning Adapter | Current report | Effective availability |
|---|---|---|
| `unhealthy` | none | `unavailable`, inherited Adapter cause |
| `unknown` | none | `unknown` |
| `healthy` | none | `unknown`, `hearth.awaiting_entity_report` |
| `healthy` | `available` | `available` |
| `healthy` | `unavailable` | `unavailable`, reported Entity cause |

Whenever an Adapter actually leaves healthy state through an unhealthy heartbeat, release, or supervisor-owned lease expiry, Core deletes that Adapter's current Entity availability rows in the same transaction as the health change. Every Adapter health status or reason transition also appends the resulting effective availability transition for each owned Entity in that transaction. A later healthy report therefore leaves every owned Entity unknown until fresh explicit reports arrive.

After actual Adapter recovery:

1. The unhealthy transition has already invalidated current Entity reports.
2. Adapter code reports healthy and waits for acknowledgement.
3. Adapter code acquires current resource status and sends one or more batches.
4. Omitted Entities remain unknown.

A Core-only restart does not change Adapter health and therefore preserves current Entity availability without an overlay, heartbeat-triggered refresh, or SDK resend.

Entity report batches:

- contain 1-256 independently identified Entities;
- commit all submitted entries atomically or commit none;
- may omit owned Entities without changing their current report; after an actual Adapter outage invalidates old reports, omitted Entities therefore remain unknown;
- reject the whole batch for an invalid ID, wrong owner, fenced runtime, nonhealthy Adapter, malformed reason, or invalid source time;
- accept reports for disabled Entities because enablement and availability are independent; and
- return only after SQLite commits.

`unavailable` requires a stable reason code. `available` omits a reason. Repeating the same status and reason refreshes current evidence without appending history. The first report after invalidation creates a transition because effective availability changed to unknown.

### Commands

Command creation remains the serialization point for enablement and now also captures the selected runtime and Adapter health.

Within one SQLite transaction, `CreateCommand`:

1. reloads the Entity and current owner;
2. returns terminal `entity_disabled` when disabled, preserving its existing precedence;
3. loads current Adapter health and active runtime;
4. returns terminal `adapter_unhealthy` when health is unhealthy or no active runtime exists;
5. permits unknown health only when an active runtime exists;
6. ignores Entity availability for dispatch policy;
7. stores the chosen runtime ID on a requested Command; and
8. commits before any NATS request is sent.

Commit order defines races:

- Command-first captures its runtime and proceeds through its normal lifecycle even if health changes immediately afterward.
- Health-first creates terminal `adapter_unhealthy` and sends nothing.
- A takeover never retargets an already requested Command. The request keeps its committed runtime subject, a newer runtime does not receive it, and the Command completes `adapter_unhealthy` if no responder remains on the old subject.

Rename durable `adapter_unavailable` status, failure code, errors, and HTTP detail to `adapter_unhealthy`. The fresh baseline schema uses the new value directly.

Extend Adapter Command rejection with `entity_unavailable`. It completes the Command with status and failure code `entity_unavailable`, returns HTTP 503, and does not update Entity availability. Existing `upstream_rejected` remains HTTP 502.

The SDK Responder adds `RejectUnavailable(message string)`. Generic `Reject` remains `upstream_rejected`.

### Observation, Registration, and enablement fencing

Registration and Entity enablement subjects carry runtime ID and validate it in their existing serializable transactions. Schema-valid requests on a stale runtime subject receive typed `runtime_fenced` rejection so the SDK closes.

Observation subjects carry runtime ID while Observation payloads remain unchanged. Projection checks the subject runtime in the same transaction as deduplication, ownership validation, receipt insertion, State projection, and Command satisfaction.

A first-seen Observation from a stale runtime records a rejected receipt with `stale_runtime`, commits, and is acknowledged. Existing exact-ID redelivery remains a no-op. Stable-subject Observations do not match the runtime-scoped stream and are never projected.

## Reason-code contract

Reason codes are lowercase dotted identifiers with a maximum of 128 characters.

- Hearth-owned codes use `hearth.<reason>`.
- Adapter-defined codes use `adapter.<reason>`.
- Components after `adapter.` are Adapter-chosen and are not validated against runtime software metadata.
- Adapters must exclude credentials, tokens, raw upstream payloads, and other secrets.

Initial common codes:

```text
hearth.awaiting_runtime
hearth.awaiting_health
hearth.heartbeat_expired
hearth.stopped
hearth.awaiting_entity_report
hearth.network_unreachable
hearth.authentication_failed
hearth.rate_limited
hearth.external_system_unavailable
hearth.entity_unavailable
```

## Public HTTP contract

All routes remain under `/v1`, use Huma Problem Details, and preserve the current trusted-network policy. Collection limits default to 50 and accept 1-200. Cursors remain unsigned, endpoint-scoped base64url JSON positions.

### Adapter representation

```go
type AdapterBody struct {
    ID     string            `json:"id"`
    Health AdapterHealthBody `json:"health"`
}

type AdapterHealthBody struct {
    Status           string                      `json:"status"`
    Source           string                      `json:"source"` // core or adapter
    Since            string                      `json:"since"`
    EvidenceAt       string                      `json:"evidence_at"`
    SourceObservedAt *string                     `json:"source_observed_at,omitempty"`
    Reason           *HealthReasonBody           `json:"reason,omitempty"`
    Runtime          *AdapterRuntimeEvidenceBody `json:"runtime,omitempty"`
}

type AdapterRuntimeEvidenceBody struct {
    ID               string `json:"id"`
    Status           string `json:"status"` // online or offline
    SoftwareName     string `json:"software_name"`
    SoftwareVersion  string `json:"software_version"`
    ClaimedAt        string `json:"claimed_at"`
    LastHeartbeatAt  *string `json:"last_heartbeat_at"`
    LeaseExpiresAt   string `json:"lease_expires_at"`
}

type HealthReasonBody struct {
    Code string `json:"code"`
}
```

Example:

```json
{
  "id": "homeassistant",
  "health": {
    "status": "unhealthy",
    "source": "adapter",
    "since": "2026-08-29T15:00:01Z",
    "evidence_at": "2026-08-29T15:00:01Z",
    "source_observed_at": "2026-08-29T15:00:00Z",
    "reason": {
      "code": "hearth.network_unreachable"
    },
    "runtime": {
      "id": "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
      "status": "online",
      "software_name": "hearth-adapter-homeassistant",
      "software_version": "0.1.0",
      "claimed_at": "2026-08-29T14:30:00Z",
      "last_heartbeat_at": "2026-08-29T15:00:01Z",
      "lease_expires_at": "2026-08-29T15:00:16Z"
    }
  }
}
```

`health` is required for all Adapter instances, including unknown health. Runtime evidence remains visible after release or expiry with status `offline`; an Adapter that has never claimed a runtime omits `runtime`. Core-sourced health omits `source_observed_at`.

### Entity representation

Modify every direct, collection, and Device-embedded Entity body:

```diff
type EntityBody struct {
    ID           string            `json:"id"`
    DeviceID     string            `json:"device_id"`
+   AdapterID    string            `json:"adapter_id"`
    Name         string            `json:"name"`
    Type         string            `json:"type"`
    Support      map[string]any    `json:"support"`
    Enabled      bool              `json:"enabled"`
+   Availability AvailabilityBody  `json:"availability"`
    State        *StateBody        `json:"state"`
}

type AvailabilityBody struct {
    Status           string            `json:"status"`
    Source           string            `json:"source"` // core, adapter_health, or entity_report
    Since            string            `json:"since"`
    EvidenceAt       string            `json:"evidence_at"`
    SourceObservedAt *string           `json:"source_observed_at,omitempty"`
    Reason           *HealthReasonBody `json:"reason,omitempty"`
}
```

State remains nullable or last accepted and is never replaced by an availability sentinel.

### Adapter routes

#### `GET /v1/adapters`

- Operation ID: `list-adapters`
- Tag: `Adapters`
- Query: `limit` and `cursor`
- Ordering: stable Adapter slug ascending
- Response: `{ "items": [...], "next_cursor": "..." }`
- Errors: invalid cursor 400; structural validation 422; internal failure 500

The cursor stores resource `adapters` and the last slug.

#### `GET /v1/adapters/{adapter_id}`

- Operation ID: `get-adapter`
- Tag: `Adapters`
- Errors: invalid slug 400; unknown slug 404; internal failure 500

### History routes

#### `GET /v1/adapters/{adapter_id}/health/history`

- Operation ID: `list-adapter-health-history`
- Returns stored Adapter transitions newest first.
- Unknown slug returns 404.

#### `GET /v1/entities/{entity_id}/availability/history`

- Operation ID: `list-entity-availability-history`
- Returns the stored effective Entity timeline newest first. Core materializes Adapter-inherited and directly reported transitions when their status or reason changes.
- Unknown Entity returns 404.

Both accept `limit` and `cursor`, use `receive_order DESC`, fetch `limit + 1`, omit totals, and scope cursors to resource plus parent ID. Core readiness changes are absent because they do not change health or availability.

```go
type HealthTransitionBody struct {
    Status           string            `json:"status"`
    Source           string            `json:"source"`
    Reason           *HealthReasonBody `json:"reason,omitempty"`
    SourceObservedAt *string           `json:"source_observed_at,omitempty"`
    ObservedAt       string            `json:"observed_at"`
}
```

## NATS wire contract

### Subject routes

```text
hearth.v1.adapter.<adapter>.claim
hearth.v1.adapter.<adapter>.runtime.<runtime_id>.heartbeat
hearth.v1.adapter.<adapter>.runtime.<runtime_id>.release
hearth.v1.adapter.<adapter>.runtime.<runtime_id>.register
hearth.v1.adapter.<adapter>.runtime.<runtime_id>.availability
hearth.v1.adapter.<adapter>.runtime.<runtime_id>.observation.<entity_id>
hearth.v1.adapter.<adapter>.runtime.<runtime_id>.command.<entity_id>.<operation>
hearth.v1.adapter.<adapter>.runtime.<runtime_id>.enablement.<entity_id>
```

Add route types, constructors, wildcard constructors, and strict parsers in `internal/contracts/v1/natswire/subjects.go`. Runtime IDs match `run_` plus canonical UUIDv7. Runtime-scoped subjects are the sole wire source of runtime identity, and this implementation does not serve old stable-slug-only subjects.

### IDs and schemas

Add common definitions:

```text
run_ runtime ID
clm_ claim request ID
hbt_ heartbeat request ID
rel_ release request ID
avl_ availability request ID
```

Add the request IDs to the causation union and add eight schemas:

```text
adapter-claim-request / adapter-claim-response
adapter-heartbeat-request / adapter-heartbeat-response
adapter-release-request / adapter-release-response
entity-availability-request / entity-availability-response
```

Embed and compile them with the existing schemas. All documents remain strict JSON Schema 2020-12 objects with UTC timestamps and the existing envelope fields.

### Claim data

Request:

```json
{
  "adapter_id": "homeassistant",
  "runtime_id": "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "software_name": "hearth-adapter-homeassistant",
  "software_version": "0.1.0"
}
```

Accepted response:

```json
{
  "status": "accepted"
}
```

Rejected claim codes:

- `adapter_active`, transient, with the active runtime's recorded lease expiry as the `retry_after` UTC timestamp. That timestamp may already have elapsed while the claim waits for the supervisor to commit expiry;
- `claim_conflict`, permanent, when the runtime ID conflicts with prior metadata or no longer identifies the active runtime.

### Heartbeat data

Request:

```json
{
  "external_system": {
    "status": "unhealthy",
    "source_observed_at": "2026-08-29T15:00:00Z",
    "reason": {
      "code": "hearth.network_unreachable"
    }
  }
}
```

`healthy` omits reason. Adapter-originated `unknown` omits reason and is accepted only before that runtime has reported its first known external-system condition.

Accepted response:

```json
{
  "status": "accepted",
  "lease_expires_at": "2026-08-29T15:00:15Z"
}
```

Rejected codes: `runtime_fenced` and permanent `invalid_transition` when one runtime attempts to return from known health to `unknown`.

### Release data

Request data is an empty object. Accepted response contains only `status: accepted`. A same-runtime retry on its release subject after a committed release is accepted idempotently while no newer runtime has claimed the Adapter. Once a newer claim exists, every request on the earlier runtime subject receives `runtime_fenced`.

### Entity availability data

Request:

```json
{
  "entities": [
    {
      "entity_id": "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
      "status": "unavailable",
      "source_observed_at": "2026-08-29T15:00:00Z",
      "reason": {
        "code": "adapter.entity_unavailable"
      }
    }
  ]
}
```

The schema enforces 1-256 entries, unique whole report objects, available-without-reason, and unavailable-with-reason. Core validates Entity ID uniqueness semantically because JSON Schema `uniqueItems` compares whole objects rather than Entity IDs.

Accepted response:

```json
{
  "status": "accepted",
  "reported_at": "2026-08-29T15:00:01Z",
  "count": 1
}
```

Rejected codes:

- `runtime_fenced`;
- `adapter_unhealthy`;
- `invalid_request`, for duplicate Entity IDs or request-ID conflicts;
- `unknown_entity`, with Entity ID;
- `wrong_adapter`, with Entity ID.

Infrastructure failure publishes no schema-level reply. The SDK retries transient no-response failures under caller context.

### Existing payload compatibility

Registration, Observation, Command request/response, and Entity enablement request/response data do not add `runtime_id`. Their NATS subjects provide runtime identity. Registration and enablement add `runtime_fenced` to their typed rejection codes. Command rejection code becomes one of `upstream_rejected` or `entity_unavailable`.

A JSON payload captured without its NATS subject does not identify the runtime. Transport code must preserve or record the subject when retaining or diagnosing a message.

### Observation stream provisioning

Provision stream `HEARTH_OBSERVATIONS_V1` with the runtime-scoped Observation wildcard and durable consumer `hearthd-state-runtime-v1` with the same filter. Existing configuration drift remains a readiness failure.

Hearth had no deployments before this incompatible subject change, so provisioning does not migrate or roll back pre-runtime stream resources. Core, contracts, the SDK, and first-party Adapters use the runtime-scoped contract together; mixed versions remain unsupported.

## Go implementation contract

### Domain types

Owner: `internal/modules/devices/model.go` and new `health.go`.

```diff
 type EntityWithState struct {
     Entity Entity
     State  *State
+    Availability EntityAvailability
 }

 type CommandStatus string
 const (
-    CommandStatusAdapterUnavailable CommandStatus = "adapter_unavailable"
+    CommandStatusAdapterUnhealthy   CommandStatus = "adapter_unhealthy"
+    CommandStatusEntityUnavailable  CommandStatus = "entity_unavailable"
 )

 type CommandFailureCode string
 const (
-    CommandFailureAdapterUnavailable CommandFailureCode = "adapter_unavailable"
+    CommandFailureAdapterUnhealthy  CommandFailureCode = "adapter_unhealthy"
+    CommandFailureEntityUnavailable CommandFailureCode = "entity_unavailable"
 )

 type CommandRequest struct {
     ID            CommandID
     CorrelationID CorrelationID
     EntityID      EntityID
     OperationName OperationName
     Parameters    CommandParameters
     Deadline      time.Time
 }

 type CommandRecord struct {
     ID        CommandID
     EntityID  EntityID
     AdapterID string
+    RuntimeID *RuntimeID
     // existing fields
 }
```

New types:

```go
type RuntimeID string

type AdapterHealthStatus string
const (
    AdapterHealthUnknown   AdapterHealthStatus = "unknown"
    AdapterHealthHealthy   AdapterHealthStatus = "healthy"
    AdapterHealthUnhealthy AdapterHealthStatus = "unhealthy"
)

type EntityAvailabilityStatus string
const (
    EntityAvailabilityUnknown     EntityAvailabilityStatus = "unknown"
    EntityAvailabilityAvailable   EntityAvailabilityStatus = "available"
    EntityAvailabilityUnavailable EntityAvailabilityStatus = "unavailable"
)

type HealthReason struct {
    Code string
}

type RuntimeEvidence struct {
    ID              RuntimeID
    Status          string
    SoftwareName    string
    SoftwareVersion string
    ClaimedAt       time.Time
    LastHeartbeatAt *time.Time
    LeaseExpiresAt  time.Time
}

type AdapterHealth struct {
    Status           AdapterHealthStatus
    Source           string
    Since            time.Time
    EvidenceAt       time.Time
    SourceObservedAt *time.Time
    Reason           *HealthReason
    Runtime          *RuntimeEvidence
}

type AdapterInstance struct {
    ID     string
    Health AdapterHealth
}

type EntityAvailability struct {
    Status           EntityAvailabilityStatus
    Source           string
    Since            time.Time
    EvidenceAt       time.Time
    SourceObservedAt *time.Time
    Reason           *HealthReason
}

type HealthTransition struct {
    ReceiveOrder     int64
    Status           string
    Source           string
    Reason           *HealthReason
    SourceObservedAt *time.Time
    ObservedAt       time.Time
}
```

Add `ParseRuntimeID` using the existing UUIDv7 rules. The SDK generates runtime IDs with the same rules. Adapter IDs remain validated slugs rather than canonical UUIDs.

### Service types and methods

Owner: new `internal/modules/devices/health.go`, `health_reads.go`, and `health_evaluation.go`.

```go
type ClaimAdapterRuntimeParams struct {
    AdapterID       string
    RuntimeID       RuntimeID
    SoftwareName    string
    SoftwareVersion string
}

type AdapterHeartbeat struct {
    AdapterID       string
    RuntimeID       RuntimeID
    ExternalStatus AdapterHealthStatus
    SourceObservedAt time.Time
    Reason          *HealthReason
}

type EntityAvailabilityReport struct {
    EntityID        EntityID
    Status          EntityAvailabilityStatus
    SourceObservedAt time.Time
    Reason          *HealthReason
}

func (service *Service) ClaimAdapterRuntime(context.Context, ClaimAdapterRuntimeParams) error
func (service *Service) RecordAdapterHeartbeat(context.Context, AdapterHeartbeat) (HeartbeatResult, error)
func (service *Service) ReleaseAdapterRuntime(context.Context, string, RuntimeID) error
func (service *Service) ReportEntityAvailability(context.Context, string, string, RuntimeID, []EntityAvailabilityReport) (time.Time, error)
func (service *Service) ExpireAdapterLeases(context.Context, time.Time) error

func (service *Service) ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
func (service *Service) GetAdapter(context.Context, string) (AdapterInstance, error)
func (service *Service) ListAdapterHealthHistory(context.Context, ListAdapterHealthParams) (Page[HealthTransition], error)
func (service *Service) ListEntityAvailabilityHistory(context.Context, ListEntityAvailabilityParams) (Page[HealthTransition], error)
```

Validation rules live in the service. SQLite owns claim idempotency, runtime/health transition atomicity, availability batch atomicity, and Command/health serialization.

Add errors:

```go
var (
    ErrAdapterNotFound      = errors.New("adapter not found")
    ErrAdapterActive        = errors.New("adapter already has an active runtime")
    ErrAdapterUnhealthy     = errors.New("adapter unhealthy")
    ErrEntityUnavailable    = errors.New("entity unavailable")
    ErrRuntimeFenced        = errors.New("adapter runtime fenced")
)
```

Remove `ErrAdapterUnavailable` after migrating callers and tests.

### Repository interface

Owner: `internal/modules/devices/repository.go`.

`RegistrationRepository` keeps its existing interface. Registration adds the runtime ID parsed from the subject to `RegisterBindingParams`. Command sending adds the committed runtime ID as a routing argument:

```diff
 type CommandSender interface {
-    Send(context.Context, string, CommandRequest) (CommandAcceptance, error)
+    Send(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error)
 }
```

The sender uses the runtime ID only to construct the Command subject.

Add a focused repository capability and embed it in `Repository`:

```go
type HealthRepository interface {
    ClaimAdapterRuntime(context.Context, ClaimRuntimeWrite) error
    RecordAdapterHeartbeat(context.Context, HeartbeatWrite) (HeartbeatResult, error)
    ReleaseAdapterRuntime(context.Context, ReleaseRuntimeWrite) error
    ExpireAdapterLeases(context.Context, ExpireLeasesWrite) error
    ReportEntityAvailability(context.Context, AvailabilityBatchWrite) (time.Time, error)
    ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
    GetAdapter(context.Context, string) (AdapterInstance, error)
    ListAdapterHealthHistory(context.Context, ListAdapterHealthParams) (Page[HealthTransition], error)
    ListEntityAvailabilityHistory(context.Context, ListEntityAvailabilityParams) (Page[HealthTransition], error)
}
```

Existing repository writes that originate from an Adapter add runtime ID and validate it transactionally:

```diff
 type RegisterBindingParams struct {
     AdapterID string
+    RuntimeID RuntimeID
     // existing fields
 }

 type SetEntityEnabledParams struct {
     EntityID      EntityID
     Enabled       bool
     RequiredOwner *string
+    RequiredRuntime *RuntimeID
     UpdatedAt     time.Time
 }

 type ProjectObservationParams struct {
     AdapterID   string
+    RuntimeID   RuntimeID
     Observation Observation
     // existing fields
 }
```

`CreateCommand` reloads owner, enablement, Adapter health, and active runtime in its existing transaction. It returns the committed terminal or requested record, including runtime ID.

### SDK interface

Owner: `sdk/adapter/types.go`, `errors.go`, and `session.go`.

```diff
 type Config struct {
     AdapterID string
+    SoftwareName string
+    SoftwareVersion string
     NATSURL   string
     Logger    *slog.Logger
 }

 type Responder interface {
     Accept() error
     Reject(message string) error
+    RejectUnavailable(message string) error
 }
```

New public SDK types and methods:

```go
type HealthStatus string
const (
    HealthUnknown   HealthStatus = "unknown"
    HealthHealthy   HealthStatus = "healthy"
    HealthUnhealthy HealthStatus = "unhealthy"
)

type HealthReport struct {
    Status           HealthStatus
    SourceObservedAt time.Time
    ReasonCode       string
}

type EntityAvailabilityStatus string
const (
    AvailabilityAvailable   EntityAvailabilityStatus = "available"
    AvailabilityUnavailable EntityAvailabilityStatus = "unavailable"
)

type EntityAvailabilityReport struct {
    EntityID         string
    Status           EntityAvailabilityStatus
    SourceObservedAt time.Time
    ReasonCode       string
}

func (session *Session) SetHealth(context.Context, HealthReport) error
func (session *Session) ReportEntityAvailability(context.Context, []EntityAvailabilityReport) error
```

`Connect` requires Core claim success before returning and starts one serialized heartbeat loop. `Close` attempts release with an internal five-second timeout before draining NATS; failure falls back to lease expiry. `Register`, `SetEntityEnabled`, and `PublishObservation` use the hidden runtime ID to construct subjects. `ServeCommands` subscribes only to the Session's runtime-scoped Command wildcard.

The Session does not retain acknowledged availability reports. It serializes explicit availability request/reply calls and retries transient disconnect or no-response failures under caller context. Core stores the accepted result by availability request ID, returns that original result to exact retries even after health changes, and permanently rejects reuse with different data. `Register` owns the same transient retry behavior, reuses one request envelope across attempts, and stops on local validation or schema-defined permanent rejection; first-party applications call it once. A fenced response closes the Session and returns `ErrRuntimeFenced` from all pending and future methods. The session state machine serializes heartbeat, close, and fencing changes; it never invokes callbacks while holding its locks.

### NATS transport interfaces

Owner: `internal/modules/devices/nats`.

Add:

```go
type RuntimeClaimer interface {
    ClaimAdapterRuntime(context.Context, devices.ClaimAdapterRuntimeParams) error
}

type HealthRecorder interface {
    RecordAdapterHeartbeat(context.Context, devices.AdapterHeartbeat) (devices.HeartbeatResult, error)
    ReleaseAdapterRuntime(context.Context, string, devices.RuntimeID) error
}

type AvailabilityReporter interface {
    ReportEntityAvailability(context.Context, string, string, devices.RuntimeID, []devices.EntityAvailabilityReport) (time.Time, error)
}
```

`SessionServer` owns claim, heartbeat, and release subscriptions. `EntityAvailabilityServer` owns batch request/reply. Existing Registration and enablement servers receive runtime-scoped routes. Observation Consumer parses runtime scope. Command Sender takes the committed runtime ID and constructs the runtime-scoped subject.

Transport owns DTO mapping, subject parsing and identity, schema, trace, correlation, causation, rejection mapping, safe logging, and drain. It does not decide health or availability.

### Core health supervisor

Owner: new `internal/app/hearthd/health_supervisor.go`.

The app-owned supervisor polls the existing `RuntimeReadiness` once per second:

- ready to not-ready stops expiry;
- not-ready to ready records a one-lease-duration grace deadline;
- while ready and at or after that deadline, it calls `ExpireAdapterLeases(now)` once per second; and
- shutdown stops the supervisor before NATS servers drain.

Claim, heartbeat, release, and availability operations remain available while expiry is paused. They verify active runtime identity but do not evaluate lease deadlines or expire runtimes. No readiness state changes persist health or read projections. `/readyz` remains the externally authoritative boundary.

The app-owned supervisor owns readiness inspection, expiry pause and grace, and process lifecycle. The `devices` module exposes expiry as a validated persistence operation; it does not retain scheduler state. Supervisor-invoked expiry is the only path that ends an elapsed lease.

## Persistence

### Fresh baseline schema

Hearth had no deployments before this feature. `00001_initial.sql` therefore creates the final schema directly; it does not upgrade, backfill, or preserve pre-feature databases. Existing local databases must be recreated.

The baseline creates:

1. `adapter_instances`
   - `adapter_id` primary key with slug length and shape checks;
   - nullable `active_runtime_id` and `health_runtime_id`;
   - current health status/reason/source/evidence/since fields.

2. `adapter_runtimes`
   - SDK-supplied `runtime_id` primary key with `run_` check and lost-response idempotency;
   - Adapter foreign key;
   - software name/version;
   - claimed, nullable last-heartbeat, lease-expiry, ended timestamps and end reason;
   - partial unique index allowing one `ended_at IS NULL` runtime per Adapter.

3. `entity_availability_current`
   - one row per Entity;
   - reporting Adapter and runtime;
   - status restricted to available/unavailable;
   - reason/source-observed/Core-evidence/current-since fields;
   - latest direct transition receive order.

4. `entity_availability_receipts`
   - accepted `avl_` request ID;
   - canonical request fingerprint; and
   - original reported-at result returned to exact retries.

5. `health_transitions`
   - global `receive_order INTEGER PRIMARY KEY AUTOINCREMENT`;
   - resource kind adapter/entity;
   - Adapter ID, optional Entity ID, optional runtime ID;
   - Adapter health transitions and materialized effective Entity availability transitions;
   - status with resource-specific checks;
   - source, reason, source-observed time, Core-observed time;
   - checks requiring negative reasons and prohibiting reasons for positive states;
   - indexes for Adapter and Entity newest-first histories.

The baseline `commands` and `observation_receipts` tables include their final runtime IDs, statuses, failure and rejection codes, indexes, and checks. Registration inserts the initial availability transition for each newly created Entity; no migration seeding exists.

Migration tests cover creation from an empty database, idempotent startup, final tables, indexes, and constraints. Repository tests cover transactional relationships and foreign-key behavior.

### Query sources

One feature-owned sqlc group generates `internal/modules/devices/dbsqlc` from the concern-specific SQL files in `internal/modules/devices/dbqueries`. `health.sql` owns claims, heartbeat/current health, runtime release/expiry, current availability, transition insertion, Adapter reads, and history queries.

Modify:

- `registration.sql` to insert initial Entity availability and validate active runtime in registration transactions;
- `state.sql` to select Adapter and availability evidence needed for current effective Entity views;
- `commands.sql` for runtime ID and renamed/new outcomes;
- `receipts.sql` for runtime ID.

Generated sqlc types remain inside the SQLite repository implementation.

### Effective Entity history

The first effective Entity transition is inserted transactionally with Entity registration. A changed explicit report appends its resulting effective transition. Each Adapter health status or reason transition uses one transactional `INSERT ... SELECT` to append the resulting effective transition for every owned Entity whose latest status or reason differs. Adapter `unhealthy` maps to Entity `unavailable`; Adapter `healthy` or `unknown` maps to Entity `unknown`; and Adapter reasons remain inherited causes. A change of source alone does not append a transition.

`ListEntityAvailabilityHistory` reads the stored Entity transitions by `receive_order DESC`, applies the optional cursor, and fetches `limit + 1`. It does not reconstruct effective history from Adapter transitions.

## First-party Adapter changes

### Home Assistant migration Adapter

The app passes software name/version in `adapter.Config`; `Connect` claims before Registration.

The Adapter reports:

- unknown while starting;
- healthy only after WebSocket connection, state-change subscription, snapshot acquisition, and buffered-event reconciliation are ready;
- unhealthy immediately when connection, authentication, or external service use fails, with common or namespaced reason; and
- fresh Entity availability after each healthy transition.

Home Assistant State `on` or `off` reports Entity available and publishes the existing Observation. State `unavailable` or `unknown` reports Entity unavailable and does not replace State. Other unsupported values retain safe warning behavior.

A Command with no current client retains generic upstream rejection. A command attempt that specifically confirms the Home Assistant Entity is unavailable uses `RejectUnavailable` and separately reports Entity unavailable.

### Simulator

Add deterministic simulator scenarios for a healthy Adapter with an available Entity, configured-external-system unhealthy, and Entity unavailable with Command recovery. Cover heartbeat expiry, duplicate claim and takeover, stale-runtime isolation, Core readiness lease grace, persisted reads across Core restart, and graceful release in the simulator process matrix, where the test can control Core and competing sessions directly.

Replace the existing `unavailable-adapter` expectation and durable Command outcome with `adapter_unhealthy`.

## Implementation ownership

| Area | Owner |
|---|---|
| Wire schemas and embedding | `contracts/v1` |
| Subject parsing | `internal/contracts/v1/natswire` |
| Health rules, persistence interfaces, current/history reads, Commands, Registration, enablement, and Observation projection | `internal/modules/devices` |
| HTTP operations and DTO mapping | `internal/modules/devices/api` |
| NATS servers, consumer, sender, stream policy, and wire mapping | `internal/modules/devices/nats` |
| Readiness supervisor and process assembly | `internal/app/hearthd` |
| Baseline schema and sqlc queries | `internal/platform/db/migrations/00001_initial.sql`, `internal/modules/devices/dbqueries`, and `sqlc.yaml` |
| Adapter lifecycle client | `sdk/adapter` |
| First-party adoption | `internal/adapters/{homeassistant,simulator}` |
| Canonical language and decisions | `CONTEXT.md`, `docs/architecture.md`, and ADRs 0016-0017 |

Tests stay beside their owners. Generated sqlc output remains under `internal/modules/devices/dbsqlc`.

## Deliverables

| Deliverable | Effort | Depends on |
|---|---:|---|
| D1. Wire schemas, IDs, runtime-scoped subjects, and stream provisioning | L | - |
| D2. Baseline schema, feature-owned sqlc package, claim idempotency, and current/history persistence | XL | D1 |
| D3. Domain health evaluation, lease supervisor, and Adapter reads | L | D2 |
| D4. SDK claim/heartbeat/release lifecycle and end-to-end fencing | XL | D1, D2, D3 |
| D5. Entity availability batches, current Entity views, and effective history | XL | D2, D3, D4 |
| D6. Command, Observation, Registration, and enablement integration | L | D2, D4, D5 |
| D7. HTTP Adapter/availability/history contract and OpenAPI | L | D3, D5, D6 |
| D8. Home Assistant, simulator, process recovery matrix, docs, and verification | XL | D4, D5, D6, D7 |

## Acceptance criteria

### Adapter session and fencing

- [x] Claim is idempotent by the SDK-supplied runtime ID, permits one active runtime, and permits takeover only after release or supervisor expiry. The accepted response is an acknowledgement; the protocol fixes the five-second heartbeat interval and fifteen-second lease.
- [x] Only an accepted heartbeat renews the lease. An active runtime may renew after its recorded deadline until the supervisor commits expiry.
- [x] Every post-claim subject contains an Adapter and runtime ID, and strict parsing rejects malformed combinations. Every Adapter-originated write also checks that subject runtime is active in its committing transaction.
- [x] After takeover, the old runtime cannot register, change enablement, project an Observation, report availability, or receive a newly created Command.
- [x] `runtime_fenced` terminates the SDK session without reclaim. Runtime IDs remain fencing data, not authorization credentials.

### Health and readiness

- [x] Adapter health and transition creation follow the effective-health table, including claim, expiry, and release.
- [x] Reads expose Core and Adapter times. Core receive order alone controls transitions.
- [x] A readiness failure freezes expiry without transitions. Recovery applies the expiry-only grace, allows the active runtime to renew, and does not create a transition when persisted health is unchanged.
- [x] `/healthz` and `/readyz` keep their current meanings and readiness checks.

### Entity availability

- [x] Effective availability follows the owning Adapter and its optional current Entity report. An actual transition away from healthy deletes current reports transactionally.
- [x] A batch contains 1-256 unique owned Entity IDs and commits atomically. Omitted reports remain unchanged, disabled Entities are accepted, and invalid identity, ownership, runtime, health, reason, or time rejects the batch.
- [x] A repeated status and reason refreshes current evidence only. The first report after invalidation creates a transition.
- [x] Core-only restart preserves and immediately exposes persisted availability without an SDK resend. Actual Adapter recovery requires fresh reports.
- [x] Availability never replaces State, which remains nullable or last accepted.

### Commands and other Adapter traffic

- [x] Disabled wins classification. An unhealthy owner or one without an active runtime creates terminal `adapter_unhealthy` without dispatch; unknown health with an active runtime and Entity unavailability under a healthy owner both dispatch.
- [x] `RejectUnavailable` creates terminal `entity_unavailable`, returns HTTP 503, and does not change availability. Generic rejection remains `upstream_rejected` and HTTP 502.
- [x] SQLite commit order decides Command and health races. Takeover does not retarget requested Commands, and the baseline uses `adapter_unhealthy` directly.
- [x] Runtime-scoped Observation preserves acknowledgement, deduplication, tracing, IDs, receipt retention, and State ordering. A stale runtime commits one acknowledged `stale_runtime` receipt without changing State or Commands.
- [x] Registration and enablement keep their current behavior after subject runtime validation. The Observation consumer processes only runtime-scoped subjects.

### HTTP, history, persistence, and compatibility

- [x] Adapter list, detail, both history routes, and all Entity representations match the specified DTOs, errors, pagination, and OpenAPI metadata.
- [x] Effective Entity history materializes Adapter-inherited and directly reported transitions in global receive order, omits source-only changes, and remains retained indefinitely.
- [x] The fresh baseline schema creates the final State, receipt, Command, Adapter, runtime, availability, and history relationships with the specified constraints and indexes.
- [x] Indexes and transactions enforce one open runtime per Adapter. sqlc output is reproducible and stays behind the module's capability interfaces.
- [x] Core, contracts, the SDK, first-party Adapters, and NATS resources use the runtime-scoped v1 contract together while canonical resource IDs and histories remain compatible.

## Test strategy

| Layer | Required coverage |
|---|---|
| Contract | New IDs, strict schemas, reason branches, 256 bound, causation, typed rejections, and unchanged existing payload shapes. |
| Subject routing | Every constructor, parser, and wildcard; invalid runtime IDs; old-route rejection; and runtime isolation. |
| Service | Health validation, top-level reason namespace, expiry validation, owned copies, and page validation. |
| SQLite | Claim retry, duplicate claim race, supervisor-owned lease expiry, late heartbeat renewal, batch atomicity, effective reads, transactional history fan-out, source-only transition suppression, immutable Entity ownership, and Command/health commit races. |
| NATS/SDK | Claim retry reuses one SDK-generated runtime ID after a lost response, heartbeat serialization, fenced shutdown, request/reply route identity, explicit availability retry, absence of heartbeat-triggered availability reporting, and runtime-scoped Command serving. |
| JetStream | Runtime-scoped stream and consumer creation, configuration-drift rejection, stale-runtime receipts, redelivery, and readiness validation. |
| HTTP | Bodies, nested availability, pagination scopes, history causes, errors, and runtime OpenAPI. |
| Adapter | Home Assistant connection/outage/resource states and deterministic simulator health and availability scenarios. |
| Process | Core restart command recovery, readiness pause/recovery, takeover, active Command behavior, stale-runtime isolation, and clean shutdown/release. |
| Schema | Empty-database creation, idempotent startup, final indexes and constraints, and repository foreign-key behavior. |

Use fake clocks and direct expiry calls for domain/repository tests. Do not make the unit suite sleep for five or fifteen seconds. Keep real timers only in a small process-level test with bounded deadlines.

## Resolved contract decisions

- `AdapterRuntimeEvidenceBody.LastHeartbeatAt`, `RuntimeEvidence.LastHeartbeatAt`, and persisted `last_heartbeat_at` are nullable. A claimed runtime returns null until Core accepts its first heartbeat.
- Availability batches reject every nonhealthy Adapter, including `unknown`, with `adapter_unhealthy`.
- Hearth had no deployments before the runtime-scoped Observation subject change. JetStream provisioning supports fresh runtime-scoped resources and rejects drift rather than migrating pre-runtime resources.

## Delivery and verification

All eight deliverables are implemented. Tests prove that a stale runtime cannot receive a newly created Command after takeover and cover effective Entity history, Core-readiness lease grace, and direct availability invalidation.

Final review covered generated sqlc output, embedded schemas, fresh baseline creation, Entity history boundaries, runtime-scoped NATS resources, runtime OpenAPI, first-party Adapter fixtures, and race-enabled SDK lifecycle tests.

Verified on 2026-08-31:

```sh
mise run validate
git diff --check
git status --short
```

All commands passed, and the worktree was clean before this documentation closeout.
