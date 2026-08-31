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
- configured-external-system evidence, reported by Adapter code in each heartbeat.

An unhealthy Adapter makes every owned Entity effectively unavailable. A healthy Adapter does not make an Entity available; the active runtime must explicitly report that Entity. Entity availability remains separate from enablement, State, and State freshness.

Availability is advisory when an owner is usable. A Command fails before dispatch when its owning Adapter is known unhealthy or has no active runtime. A Command is still attempted when Adapter health is unknown but an active runtime exists, or when the Entity is reported unavailable under a healthy Adapter. A fresh Adapter rejection may then return `entity_unavailable` without changing stored availability.

One Adapter instance permits one active runtime. A Core-issued runtime ID scopes the NATS subject for every post-claim Adapter-originated operation and every Command. Payloads do not repeat the runtime ID; the subject is the authoritative wire source. A runtime ID is not Adapter identity or an authentication credential. The deployment's NATS account permissions remain the transport security boundary.

## Scope

Included work covers Adapter persistence and archival, runtime lifecycle and fencing, explicit Entity availability, current and historical HTTP reads, and Command outcome changes. It also covers Core recovery, SDK lifecycle and replay, first-party Adapter adoption, migration, code generation, runtime-scoped NATS subjects and Observation consumer provisioning, OpenAPI, and tests.

This feature does not add Device health, a `degraded` status, inferred availability, Adapter replication or forced takeover, authentication changes, Adapter disablement, or Binding ownership transfer. It also excludes automation, alerting, incident tracking, uptime metrics, UI or CLI work, configurable timing or limits, and history pruning. Archival does not cascade through Bindings. Ownership intervals support future transfer behavior, but this change does not implement transfer or reconciliation.

## Behavioral contract

### Core readiness takes precedence

`/healthz` continues to report process liveness. `/readyz` remains the authoritative check for migrated SQLite, NATS connectivity, JetStream resources, and the active Observation consumer.

When Core readiness fails:

- Adapter and Entity evaluation freezes;
- claim, heartbeat, release, and availability handlers return a transient infrastructure error without a schema-level reply, so SDK calls retry under their contexts;
- lease expiry does not create transitions;
- stored current evidence and history remain unchanged; and
- callers must treat child health as non-authoritative while `/readyz` is not ready.

When readiness recovers, Core begins one lease-duration evaluation grace:

- Adapters without a post-recovery heartbeat evaluate as `unknown` with `hearth.core_recovering`;
- Entities owned by those Adapters also evaluate as `unknown`;
- these evaluation-only overrides create no history transitions;
- the first accepted heartbeat restores persisted Adapter evaluation; and
- the heartbeat response requests Entity availability refresh.

After 15 seconds of normal readiness, a runtime without a fresh heartbeat expires and creates the ordinary durable `unhealthy` transition.

### Adapter instance and runtime lifecycle

An Adapter instance is identified by its configured stable slug. `adapter.Connect` claims one active runtime before returning.

Claim behavior:

1. The SDK connects to NATS and creates one `clm_` request envelope containing Adapter ID, software name, and software version.
2. It retries that same envelope through transient no-response failures so a lost accepted response cannot create a second runtime.
3. Core creates the Adapter instance on its first claim, or loads the existing non-archived instance.
4. If the same `clm_` request already succeeded, Core returns the existing `run_` runtime ID.
5. If another unexpired runtime is active, Core rejects with transient `adapter_active` and its lease expiry; the SDK waits and retries until its context ends or the lease becomes claimable.
6. If the slug is archived, Core rejects permanently with `adapter_archived`.
7. Otherwise Core issues a `run_` UUIDv7 runtime ID, records `unknown` health with `hearth.awaiting_health`, and returns a five-second heartbeat interval and fifteen-second lease.

A replacement process cannot evict a healthy runtime. Graceful release or lease expiry ends the current runtime. A later claim reuses the stable Adapter slug but receives a new runtime ID. Software name and version are bounded diagnostic evidence attached to that runtime, never Adapter identity.

Every Adapter-originated Registration, heartbeat, release, Entity availability report, Entity enablement request, and Observation uses a subject containing its Adapter and runtime IDs. Core parses the subject and verifies the active runtime in the same SQLite transaction as the requested write. Commands are sent on the subject for the runtime ID committed on the Command record.

A `runtime_fenced` request/reply rejection is terminal. The SDK stops Command serving and further publications, clears its availability cache, closes the session, and exposes `ErrRuntimeFenced`. It does not try to reclaim from the same process.

### Adapter heartbeat and health

Heartbeats use Core NATS request/reply every five seconds. The SDK serializes regular heartbeats and immediate health changes through one loop so older health cannot arrive after newer health from the same runtime.

Each heartbeat carries the Adapter's latest configured-external-system assessment:

- `unknown` before Adapter code has a result;
- `healthy` when the external system is usable; or
- `unhealthy` with a mandatory reason code and optional safe detail.

Core receipt renews the lease to 15 seconds after receipt. Registration, availability reports, enablement requests, Observations, Command traffic, and NATS connection presence never renew it. Adapter source time is diagnostic only. Core receipt order and Core-owned time determine current status and transition order.

Effective Adapter health is:

| Runtime evidence | External-system evidence | Effective health | Effective reason |
|---|---|---|---|
| no runtime before first claim | none | `unknown` | `hearth.awaiting_runtime` |
| active lease | `unknown` | `unknown` | `hearth.awaiting_health` |
| active lease | `healthy` | `healthy` | none |
| active lease | `unhealthy` | `unhealthy` | reported reason |
| expired lease | retained but ignored | `unhealthy` | `hearth.heartbeat_expired` |
| graceful release | retained but ignored | `unhealthy` | `hearth.stopped` |
| archived | retained in history | no current health | none |

A status or stable reason-code change appends one Adapter transition. Same status and reason refresh current evidence and latest detail without appending history. Detail-only changes do not rewrite the historical transition.

`Session.SetHealth` updates the SDK's desired health immediately and waits for an acknowledged immediate heartbeat or caller cancellation. The desired value remains in memory after caller cancellation so the regular heartbeat loop can report it later. Setting `unhealthy` clears the SDK's Entity availability cache; a Core-only evaluation override does not change the SDK's desired health and therefore preserves the cache.

### Entity availability

Only an explicit report from the active, currently healthy owning Adapter sets Entity availability. Reports contain only `available` or `unavailable`; `unknown` is Core-derived.

For one Entity, effective availability is:

| Owning Adapter | Current report for Adapter health epoch | Effective availability |
|---|---|---|
| `unhealthy` | any | `unavailable`, inherited Adapter cause |
| `unknown` | any | `unknown` |
| `healthy` | none | `unknown`, `hearth.awaiting_entity_report` |
| `healthy` | `available` | `available` |
| `healthy` | `unavailable` | `unavailable`, reported Entity cause |

A transition into healthy increments the Adapter's persisted availability epoch. Earlier Entity reports no longer apply without per-Entity invalidation writes. Core accepts availability reports only while the submitted runtime is active, fresh under the current Core evaluation epoch, and persistently healthy. Core stamps accepted reports with the current Adapter availability epoch.

After actual Adapter recovery:

1. Adapter code reports healthy and waits for acknowledgement.
2. Core invalidates prior reports by incrementing the Adapter availability epoch.
3. Adapter code acquires current resource status and sends one or more batches.
4. Omitted Entities remain unknown.

After a Core-only restart, the SDK keeps its in-memory latest acknowledged reports because Adapter health never became unhealthy. The first heartbeat response asks for refresh; the SDK replays the cache in batches and retries transient report failures. The cache is not durable and is not an outbox.

Entity report batches:

- contain 1-256 independently identified Entities;
- commit all submitted entries atomically or commit none;
- may omit owned Entities without changing their current report; after a healthy recovery invalidates old reports, omitted Entities therefore remain unknown;
- reject the whole batch for an invalid ID, wrong owner, fenced runtime, nonhealthy Adapter, malformed reason, or invalid source time;
- accept reports for disabled Entities because enablement and availability are independent; and
- return only after SQLite commits.

`unavailable` requires a stable reason code. `available` omits reason and detail. Repeating the same status and reason in the same availability epoch refreshes current evidence and detail without appending history. The first report in a new epoch creates a transition because it changes effective availability from unknown.

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

Rename durable `adapter_unavailable` status, failure code, errors, and HTTP detail to `adapter_unhealthy`. Migration rewrites existing rows.

Extend Adapter Command rejection with `entity_unavailable`. It completes the Command with status and failure code `entity_unavailable`, returns HTTP 503, and does not update Entity availability. Existing `upstream_rejected` remains HTTP 502.

The SDK Responder adds `RejectUnavailable(message string)`. Generic `Reject` remains `upstream_rejected`.

### Observation, Registration, and enablement fencing

Registration and Entity enablement subjects carry runtime ID and validate it in their existing serializable transactions. Schema-valid requests on a stale runtime subject receive typed `runtime_fenced` rejection so the SDK closes.

Observation subjects carry runtime ID while Observation payloads remain unchanged. Projection checks the subject runtime in the same transaction as deduplication, ownership validation, receipt insertion, State projection, and Command satisfaction.

A first-seen Observation from a stale runtime records a rejected receipt with `stale_runtime`, commits, and is acknowledged. Existing exact-ID redelivery remains a no-op. Stable-subject Observations do not match the runtime-scoped stream and are never projected.

### Adapter archival

`DELETE /v1/adapters/{adapter_id}` archives rather than deletes.

Archival requires a known Adapter instance. An already archived Adapter returns success idempotently. Otherwise it requires:

- no active runtime, including one still within its lease; and
- no current Bindings owned by the Adapter.

Success sets `archived_at`, clears current health, excludes the Adapter from default lists, keeps detail and history readable, and permanently reserves the slug. It does not delete health transitions, runtime records, Devices, Entities, Bindings, Commands, or Observations. Archived slugs reject future claims permanently.

## Reason-code contract

Reason codes are lowercase dotted identifiers with a maximum of 128 characters.

- Hearth-owned codes use `hearth.<reason>`.
- Adapter-specific codes use `adapter.<software_name>.<reason>`.
- The Adapter-specific software-name segment must equal the subject-safe software name from the active claim.
- Optional detail is valid UTF-8 from 1-512 characters.
- Adapters must exclude credentials, tokens, raw upstream payloads, and other secrets.

Initial common codes:

```text
hearth.awaiting_runtime
hearth.awaiting_health
hearth.core_recovering
hearth.heartbeat_expired
hearth.stopped
hearth.awaiting_entity_report
hearth.network_unreachable
hearth.authentication_failed
hearth.rate_limited
hearth.external_system_unavailable
hearth.entity_unavailable
```

`hearth.core_recovering` is evaluation-only and never stored as a transition.

## Public HTTP contract

All routes remain under `/v1`, use Huma Problem Details, and preserve the current trusted-network policy. Collection limits default to 50 and accept 1-200. Cursors remain unsigned, endpoint-scoped base64url JSON positions.

### Adapter representation

```go
type AdapterBody struct {
    ID         string             `json:"id"`
    ArchivedAt *string            `json:"archived_at,omitempty"`
    Health     *AdapterHealthBody `json:"health"`
}

type AdapterHealthBody struct {
    Status         string                     `json:"status"`
    Since          string                     `json:"since"`
    EvidenceAt     string                     `json:"evidence_at"`
    Reason         *HealthReasonBody          `json:"reason,omitempty"`
    Runtime        *AdapterRuntimeEvidenceBody `json:"runtime,omitempty"`
    ExternalSystem *ExternalSystemEvidenceBody `json:"external_system,omitempty"`
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

type ExternalSystemEvidenceBody struct {
    Status           string            `json:"status"`
    SourceObservedAt string            `json:"source_observed_at"`
    EvidenceAt       string            `json:"evidence_at"`
    Reason           *HealthReasonBody `json:"reason,omitempty"`
}

type HealthReasonBody struct {
    Code   string  `json:"code"`
    Detail *string `json:"detail,omitempty"`
}
```

Example:

```json
{
  "id": "homeassistant",
  "health": {
    "status": "unhealthy",
    "since": "2026-08-29T15:00:01Z",
    "evidence_at": "2026-08-29T15:00:01Z",
    "reason": {
      "code": "hearth.network_unreachable",
      "detail": "dial tcp: network is unreachable"
    },
    "runtime": {
      "id": "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
      "status": "online",
      "software_name": "hearth-adapter-homeassistant",
      "software_version": "0.1.0",
      "claimed_at": "2026-08-29T14:30:00Z",
      "last_heartbeat_at": "2026-08-29T15:00:01Z",
      "lease_expires_at": "2026-08-29T15:00:16Z"
    },
    "external_system": {
      "status": "unhealthy",
      "source_observed_at": "2026-08-29T15:00:00Z",
      "evidence_at": "2026-08-29T15:00:01Z",
      "reason": {
        "code": "hearth.network_unreachable",
        "detail": "dial tcp: network is unreachable"
      }
    }
  }
}
```

`health` is required for active and inactive non-archived Adapter instances, including unknown health. Archived Adapter detail returns `"health": null`. Runtime evidence remains visible after release or expiry with status `offline`; an Adapter that has never claimed a runtime has `runtime` and `external_system` omitted.

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
- Query: `limit`, `cursor`, and `include_archived` defaulting to `false`
- Ordering: stable Adapter slug ascending
- Response: `{ "items": [...], "next_cursor": "..." }`
- Errors: invalid cursor 400; structural validation 422; internal failure 500

The cursor stores resource `adapters`, last slug, and `include_archived` so it cannot cross filter scope.

#### `GET /v1/adapters/{adapter_id}`

- Operation ID: `get-adapter`
- Tag: `Adapters`
- Errors: invalid slug 400; unknown slug 404; internal failure 500
- Archived detail remains readable.

#### `DELETE /v1/adapters/{adapter_id}`

- Operation ID: `archive-adapter`
- Tag: `Adapters`
- Success: 204 with no body
- Errors: invalid slug 400; unknown slug 404; active runtime or owned Binding 409; internal failure 500
- Same-value archival is idempotent and returns 204 for an already archived known Adapter.

### History routes

#### `GET /v1/adapters/{adapter_id}/health/history`

- Operation ID: `list-adapter-health-history`
- Returns stored Adapter transitions newest first.
- Unknown slug returns 404; archived history remains readable.

#### `GET /v1/entities/{entity_id}/availability/history`

- Operation ID: `list-entity-availability-history`
- Returns the effective Entity timeline newest first by merging direct Entity report transitions with owning-Adapter transitions during persisted ownership intervals.
- Unknown Entity returns 404.

Both accept `limit` and `cursor`, use `receive_order DESC`, fetch `limit + 1`, omit totals, and scope cursors to resource plus parent ID. Core-only recovery overrides are absent because they are not persisted transitions.

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
  "software_name": "hearth-adapter-homeassistant",
  "software_version": "0.1.0"
}
```

Accepted response:

```json
{
  "status": "accepted",
  "runtime_id": "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
  "heartbeat_interval_ms": 5000,
  "lease_duration_ms": 15000
}
```

Rejected claim codes:

- `adapter_active`, transient, with `retry_after` UTC timestamp;
- `adapter_archived`, permanent.

### Heartbeat data

Request:

```json
{
  "external_system": {
    "status": "unhealthy",
    "source_observed_at": "2026-08-29T15:00:00Z",
    "reason": {
      "code": "hearth.network_unreachable",
      "detail": "dial tcp: network is unreachable"
    }
  }
}
```

`healthy` omits reason. Adapter-originated `unknown` omits reason and is accepted only before that runtime has reported its first known external-system condition. Core supplies later unknown causes such as recovery grace.

Accepted response:

```json
{
  "status": "accepted",
  "lease_expires_at": "2026-08-29T15:00:15Z",
  "refresh_entity_availability": true
}
```

Rejected code: `runtime_fenced`.

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
        "code": "adapter.hearth-adapter-homeassistant.entity_unavailable",
        "detail": "Home Assistant reported unavailable"
      }
    }
  ]
}
```

The schema enforces 1-256 entries, unique Entity IDs, available-without-reason, unavailable-with-reason, and 512-character detail. Core also validates uniqueness because JSON Schema `uniqueItems` compares whole objects rather than only Entity IDs.

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
    Code   string
    Detail *string
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

type ExternalSystemEvidence struct {
    Status           AdapterHealthStatus
    SourceObservedAt time.Time
    EvidenceAt       time.Time
    Reason           *HealthReason
}

type AdapterHealth struct {
    Status         AdapterHealthStatus
    Since          time.Time
    EvidenceAt     time.Time
    Reason         *HealthReason
    Runtime        *RuntimeEvidence
    ExternalSystem *ExternalSystemEvidence
}

type AdapterInstance struct {
    ID         string
    ArchivedAt *time.Time
    Health     *AdapterHealth
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

Add `NewRuntimeID` and `ParseRuntimeID` using the existing UUIDv7 rules. Adapter IDs remain validated slugs rather than canonical UUIDs.

### Service types and methods

Owner: new `internal/modules/devices/health.go`, `health_reads.go`, and `health_evaluation.go`.

```go
type ClaimAdapterRuntimeParams struct {
    ClaimID        string
    AdapterID      string
    SoftwareName   string
    SoftwareVersion string
}

type RuntimeClaim struct {
    RuntimeID        RuntimeID
    HeartbeatInterval time.Duration
    LeaseDuration     time.Duration
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

func (service *Service) ClaimAdapterRuntime(context.Context, ClaimAdapterRuntimeParams) (RuntimeClaim, error)
func (service *Service) RecordAdapterHeartbeat(context.Context, AdapterHeartbeat) (HeartbeatResult, error)
func (service *Service) ReleaseAdapterRuntime(context.Context, string, RuntimeID) error
func (service *Service) ReportEntityAvailability(context.Context, string, RuntimeID, []EntityAvailabilityReport) (time.Time, error)
func (service *Service) ExpireAdapterLeases(context.Context, time.Time) error
func (service *Service) PauseHealthEvaluation()
func (service *Service) ResumeHealthEvaluation(time.Time)

func (service *Service) ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
func (service *Service) GetAdapter(context.Context, string) (AdapterInstance, error)
func (service *Service) ArchiveAdapter(context.Context, string) error
func (service *Service) ListAdapterHealthHistory(context.Context, ListAdapterHealthParams) (Page[HealthTransition], error)
func (service *Service) ListEntityAvailabilityHistory(context.Context, ListEntityAvailabilityParams) (Page[HealthTransition], error)
```

Validation rules live in the service. SQLite owns claim idempotency, runtime/health transition atomicity, availability batch atomicity, and Command/health serialization.

Add errors:

```go
var (
    ErrAdapterNotFound      = errors.New("adapter not found")
    ErrAdapterActive        = errors.New("adapter already has an active runtime")
    ErrAdapterArchived      = errors.New("adapter is archived")
    ErrAdapterUnhealthy     = errors.New("adapter unhealthy")
    ErrEntityUnavailable    = errors.New("entity unavailable")
    ErrRuntimeFenced        = errors.New("adapter runtime fenced")
    ErrAdapterHasBindings   = errors.New("adapter owns bindings")
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
    ClaimAdapterRuntime(context.Context, ClaimRuntimeWrite) (RuntimeClaim, error)
    RecordAdapterHeartbeat(context.Context, HeartbeatWrite) (HeartbeatResult, error)
    ReleaseAdapterRuntime(context.Context, ReleaseRuntimeWrite) error
    ExpireAdapterLeases(context.Context, ExpireLeasesWrite) error
    ReportEntityAvailability(context.Context, AvailabilityBatchWrite) (time.Time, error)
    ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
    GetAdapter(context.Context, string) (AdapterInstance, error)
    ArchiveAdapter(context.Context, ArchiveAdapterParams) error
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
    Detail           string
}

type EntityAvailabilityStatus string
const (
    AvailabilityAvailable   EntityAvailabilityStatus = "available"
    AvailabilityUnavailable EntityAvailabilityStatus = "unavailable"
)

type EntityAvailabilityReport struct {
    EntityID        string
    Status          EntityAvailabilityStatus
    SourceObservedAt time.Time
    ReasonCode      string
    Detail          string
}

func (session *Session) SetHealth(context.Context, HealthReport) error
func (session *Session) ReportEntityAvailability(context.Context, []EntityAvailabilityReport) error
```

`Connect` requires Core claim success before returning and starts one serialized heartbeat loop. `Close` attempts release with an internal five-second timeout before draining NATS; failure falls back to lease expiry. `Register`, `SetEntityEnabled`, and `PublishObservation` use the hidden runtime ID to construct subjects. `ServeCommands` subscribes only to the Session's runtime-scoped Command wildcard.

The Session caches latest acknowledged availability by Entity ID, chunks automatic replay to 256, and owns no durable state. It retries availability request/reply through transient disconnect or no-response under caller context. First-party applications retain their retry loops for transient Registration failures and stop on schema-defined permanent rejection. A fenced response closes the Session and returns `ErrRuntimeFenced` from all pending and future methods. The session state machine serializes heartbeat, cache, close, and fencing changes; it never invokes callbacks while holding its locks.

### NATS transport interfaces

Owner: `internal/modules/devices/nats`.

Add:

```go
type RuntimeClaimer interface {
    ClaimAdapterRuntime(context.Context, devices.ClaimAdapterRuntimeParams) (devices.RuntimeClaim, error)
}

type HealthRecorder interface {
    RecordAdapterHeartbeat(context.Context, devices.AdapterHeartbeat) (devices.HeartbeatResult, error)
    ReleaseAdapterRuntime(context.Context, string, devices.RuntimeID) error
}

type AvailabilityReporter interface {
    ReportEntityAvailability(context.Context, string, devices.RuntimeID, []devices.EntityAvailabilityReport) (time.Time, error)
}
```

`SessionServer` owns claim, heartbeat, and release subscriptions. `EntityAvailabilityServer` owns batch request/reply. Existing Registration and enablement servers receive runtime-scoped routes. Observation Consumer parses runtime scope. Command Sender takes the committed runtime ID and constructs the runtime-scoped subject.

Transport owns DTO mapping, subject parsing and identity, schema, trace, correlation, causation, rejection mapping, safe logging, and drain. It does not decide health or availability.

### Core health supervisor

Owner: new `internal/app/hearthd/health_supervisor.go`.

The app-owned supervisor polls the existing `RuntimeReadiness` once per second:

- ready to not-ready calls `PauseHealthEvaluation` and stops expiry;
- not-ready to ready calls `ResumeHealthEvaluation(now)`;
- while ready it calls `ExpireAdapterLeases(now)` once per second; and
- shutdown stops the supervisor before NATS servers drain.

Session and availability use cases read the same evaluation gate before starting a transaction. While it is paused they return a transient infrastructure error that their NATS servers deliberately leave unanswered. The polling transition is the health evaluator's readiness boundary; `/readyz` remains the externally authoritative boundary.

The `devices` module owns evaluation rules and the in-memory recovery epoch. Application assembly owns readiness inspection and process lifecycle.

## Persistence

### Migration `00004_adapter_health_and_entity_availability.sql`

Add an immutable Goose migration with foreign keys enabled.

New tables:

1. `adapter_instances`
   - `adapter_id` primary key with slug length and shape checks;
   - `archived_at`, `created_at`, `updated_at`;
   - nullable `active_runtime_id` and `health_runtime_id`;
   - nullable current health status/reason/detail/source/evidence/since fields;
   - `availability_epoch INTEGER NOT NULL DEFAULT 0`;
   - latest transition receive order.

2. `adapter_runtimes`
   - `runtime_id` primary key with `run_` check;
   - `claim_id` unique with `clm_` check for lost-response idempotency;
   - Adapter foreign key;
   - software name/version;
   - claimed, nullable last-heartbeat, lease-expiry, ended timestamps and end reason;
   - partial unique index allowing one `ended_at IS NULL` runtime per Adapter.

3. `entity_availability_current`
   - one row per Entity;
   - reporting Adapter and runtime;
   - Adapter availability epoch;
   - status restricted to available/unavailable;
   - reason/detail/source-observed/Core-evidence/current-since fields;
   - latest direct transition receive order.

4. `health_transitions`
   - global `receive_order INTEGER PRIMARY KEY AUTOINCREMENT`;
   - resource kind adapter/entity;
   - Adapter ID, optional Entity ID, optional runtime ID;
   - status with resource-specific checks;
   - source, reason, detail, source-observed time, Core-observed time;
   - checks requiring negative reasons and prohibiting reasons for positive states;
   - indexes for Adapter and Entity newest-first histories.

5. `entity_ownership_intervals`
   - Entity ID, Adapter ID, starting receive order, optional ending receive order;
   - one open interval per Entity;
   - indexes supporting effective history joins.

Migration seeding:

- create one non-archived Adapter instance for every distinct existing `adapter_bindings.adapter_id`;
- seed current Adapter health as unknown with `hearth.awaiting_runtime`, no runtime, and no synthetic pre-feature history;
- seed one open ownership interval for every current `adapter_entity_mappings` row with starting order zero; and
- leave all Entity availability unknown without synthetic direct reports.

For a newly created Entity after migration, Registration inserts one effective Entity baseline transition in the same transaction as its mapping, then uses that transition's receive order as the ownership interval start. The baseline is inherited unavailable when the owner is unhealthy, inherited unknown when the owner is unknown, or Core-derived unknown with `hearth.awaiting_entity_report` when the owner is healthy. Re-registration of the same mapping creates neither a new interval nor a baseline transition.

Rebuild `commands`:

- add nullable `runtime_id` for pre-feature history;
- replace `adapter_unavailable` with `adapter_unhealthy` in status and failure checks and existing rows;
- add `entity_unavailable` status/failure pairing;
- preserve all IDs, timestamps, indexes, State references, and terminal checks.

Rebuild `observation_receipts` and `entity_states` together:

- add nullable `runtime_id` for pre-feature receipts;
- add `stale_runtime` rejection;
- preserve receipt order, current-State references, expiry index, and foreign-key validity.

Down migration:

- map `adapter_unhealthy` Commands to `adapter_unavailable`;
- map `entity_unavailable` Commands to `rejected` / `upstream_rejected`;
- delete stale-runtime receipts after asserting none back current State;
- rebuild Commands and receipts without runtime columns and new checks;
- drop health, runtime, availability, and ownership tables in dependency order; and
- restore the prior schema and indexes accepted by the previous binary.

Migration tests cover empty and populated databases, existing Adapter IDs, all Command statuses, current State, receipts, up/down data mappings, constraints, partial indexes, and `PRAGMA foreign_key_check`.

### Query sources

Add a fifth sqlc group:

```yaml
queries: internal/platform/db/queries/health
package: health
out: internal/platform/db/sqlc/health
```

`health/health.sql` owns claims, heartbeat/current health, runtime release/expiry, current availability, transition insertion, Adapter reads, archival checks, and history queries.

Modify:

- `registration/registration.sql` to create ownership intervals and validate active runtime in registration transactions;
- `state/state.sql` to select Adapter and availability evidence needed for current effective Entity views;
- `commands/commands.sql` for runtime ID and renamed/new outcomes;
- `receipts/receipts.sql` for runtime ID.

Generated sqlc types remain inside the SQLite repository implementation.

### Effective Entity history

`ListEntityAvailabilityHistory` performs one bounded SQL `UNION ALL` over:

- direct Entity report and ownership-baseline rows in `health_transitions`; and
- Adapter rows whose receive order falls within the Entity's ownership interval.

It maps Adapter `unhealthy` to Entity `unavailable`, Adapter `healthy` or `unknown` to Entity `unknown`, and preserves Adapter reasons as inherited causes. A CTE orders candidates chronologically and uses the preceding effective status and reason code to suppress cross-source rows that do not represent a transition. A change of source or detail alone must not fabricate one. The result then orders by global `receive_order DESC` and fetches `limit + 1`.

Future ownership transfer must close the old interval and open the new interval in the same transaction as mapping reconciliation, using the global transition order. Ownership transfer itself remains out of scope.

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

Add deterministic simulator scenarios for a healthy Adapter with an available Entity, configured-external-system unhealthy, and Entity unavailable with Command recovery. Cover heartbeat expiry, duplicate claim and takeover, stale-runtime isolation, Core readiness recovery replay, and graceful release in the simulator process matrix, where the test can control Core and competing sessions directly.

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
| Migration and sqlc queries | `internal/platform/db/migrations/00004_adapter_health_and_entity_availability.sql`, `internal/platform/db/queries/{health,commands,receipts,registration,state}`, and `sqlc.yaml` |
| Adapter lifecycle client | `sdk/adapter` |
| First-party adoption | `internal/adapters/{homeassistant,simulator}` |
| Canonical language and decisions | `CONTEXT.md`, `docs/architecture.md`, and ADRs 0016-0017 |

Tests stay beside their owners. Generated sqlc output remains under `internal/platform/db/sqlc`.

## Deliverables

| Deliverable | Effort | Depends on |
|---|---:|---|
| D1. Wire schemas, IDs, runtime-scoped subjects, and stream provisioning | L | - |
| D2. Migration, health sqlc package, claim idempotency, and current/history persistence | XL | D1 |
| D3. Domain health evaluation, lease supervisor, Adapter reads, and archival | L | D2 |
| D4. SDK claim/heartbeat/release lifecycle and end-to-end fencing | XL | D1, D2, D3 |
| D5. Entity availability batches, current Entity views, and effective history | XL | D2, D3, D4 |
| D6. Command, Observation, Registration, and enablement integration | L | D2, D4, D5 |
| D7. HTTP Adapter/availability/history contract and OpenAPI | L | D3, D5, D6 |
| D8. Home Assistant, simulator, process recovery matrix, docs, and verification | XL | D4, D5, D6, D7 |

## Acceptance criteria

### Adapter session and fencing

- [x] Claim is idempotent by envelope ID, permits one active runtime, rejects archived Adapter IDs, and permits takeover only after release or expiry. The response returns the five-second heartbeat interval and fifteen-second lease.
- [x] Only an accepted heartbeat renews the lease.
- [x] Every post-claim subject contains an Adapter and runtime ID, and strict parsing rejects malformed combinations. Every Adapter-originated write also checks that subject runtime is active in its committing transaction.
- [x] After takeover, the old runtime cannot register, change enablement, project an Observation, report availability, or receive a newly created Command.
- [x] `runtime_fenced` terminates the SDK session without reclaim. Runtime IDs remain fencing data, not authorization credentials.

### Health and readiness

- [x] Adapter health and transition creation follow the effective-health table, including claim, expiry, release, and archive.
- [x] Reads expose Core and Adapter times. Core receive order alone controls transitions.
- [x] A readiness failure freezes evaluation and expiry without transitions. Recovery applies the evaluation-only grace, requests availability refresh on the first heartbeat, and does not create a transition when persisted health is unchanged.
- [x] `/healthz` and `/readyz` keep their current meanings and readiness checks.

### Entity availability

- [x] Effective availability follows the owning-Adapter and current-epoch report table. A healthy transition invalidates old reports without per-Entity writes.
- [x] A batch contains 1-256 unique owned Entity IDs and commits atomically. Omitted reports remain unchanged, disabled Entities are accepted, and invalid identity, ownership, runtime, health, reason, or time rejects the batch.
- [x] A repeated status and reason in one epoch refreshes current evidence only. The first report in a new epoch creates a transition.
- [x] Core-only restart replays the SDK cache. An actual unhealthy assessment clears it and requires fresh reports after recovery.
- [x] Availability never replaces State, which remains nullable or last accepted.

### Commands and other Adapter traffic

- [x] Disabled wins classification. An unhealthy owner or one without an active runtime creates terminal `adapter_unhealthy` without dispatch; unknown health with an active runtime and Entity unavailability under a healthy owner both dispatch.
- [x] `RejectUnavailable` creates terminal `entity_unavailable`, returns HTTP 503, and does not change availability. Generic rejection remains `upstream_rejected` and HTTP 502.
- [x] SQLite commit order decides Command and health races. Takeover does not retarget requested Commands, and migration preserves `adapter_unavailable` history as `adapter_unhealthy`.
- [x] Runtime-scoped Observation preserves acknowledgement, deduplication, tracing, IDs, receipt retention, and State ordering. A stale runtime commits one acknowledged `stale_runtime` receipt without changing State or Commands.
- [x] Registration and enablement keep their current behavior after subject runtime validation. The Observation consumer processes only runtime-scoped subjects.

### HTTP, history, persistence, and compatibility

- [x] Adapter list, detail, archive, both history routes, and all Entity representations match the specified DTOs, filters, errors, pagination, and OpenAPI metadata.
- [x] Archived Adapters remain readable only when addressed or included, have null current health, retain history, and reserve their Adapter IDs. Archival rejects active runtimes and owned Bindings and is otherwise idempotent.
- [x] Effective Entity history uses ownership intervals and global receive order, omits source-only and detail-only changes, and remains retained indefinitely.
- [x] Migration 00004 passes empty and populated up/down tests, preserves State, receipt, Command, ID, Binding, and history relationships, applies the specified outcome mappings, and passes foreign-key checks.
- [x] Indexes and transactions enforce one open runtime per Adapter and one open ownership interval per Entity. sqlc output is reproducible and stays behind the repository interface.
- [x] Core, contracts, the SDK, first-party Adapters, and NATS resources use the runtime-scoped v1 contract together while canonical resource IDs and histories remain compatible.

## Test strategy

| Layer | Required coverage |
|---|---|
| Contract | New IDs, strict schemas, reason branches, 256 bound, causation, typed rejections, and unchanged existing payload shapes. |
| Subject routing | Every constructor, parser, and wildcard; invalid runtime IDs; old-route rejection; and runtime isolation. |
| Service | Health validation, reason namespace, evaluation override, report epochs, archival rules, owned copies, and page validation. |
| SQLite | Claim retry, duplicate claim race, lease expiry, heartbeat transitions, batch atomicity, effective reads/history, ownership intervals, and Command/health commit races. |
| NATS/SDK | Claim retry after lost response, heartbeat serialization, cache replay, fenced shutdown, request/reply route identity, availability retry, and runtime-scoped Command serving. |
| JetStream | Runtime-scoped stream and consumer creation, configuration-drift rejection, stale-runtime receipts, redelivery, and readiness validation. |
| HTTP | Bodies, null archived health, nested availability, archive conflict, pagination scopes, history causes, errors, and runtime OpenAPI. |
| Adapter | Home Assistant connection/outage/resource states and deterministic simulator health and availability scenarios. |
| Process | Core restart command recovery, readiness pause/recovery, takeover, active Command behavior, stale-runtime isolation, and clean shutdown/release. |
| Migration | Empty/populated up/down, enum rewrites, current State receipts, indexes, constraints, and foreign keys. |

Use fake clocks and direct expiry calls for domain/repository tests. Do not make the unit suite sleep for five or fifteen seconds. Keep real timers only in a small process-level test with bounded deadlines.

## Resolved contract decisions

- `AdapterRuntimeEvidenceBody.LastHeartbeatAt`, `RuntimeEvidence.LastHeartbeatAt`, and persisted `last_heartbeat_at` are nullable. A claimed runtime returns null until Core accepts its first heartbeat.
- Availability batches reject every nonhealthy Adapter, including `unknown`, with `adapter_unhealthy`.
- Hearth had no deployments before the runtime-scoped Observation subject change. JetStream provisioning supports fresh runtime-scoped resources and rejects drift rather than migrating pre-runtime resources.

## Delivery and verification

All eight deliverables are implemented. Tests prove that a stale runtime cannot receive a newly created Command after takeover and cover effective Entity history and Core-recovery concurrency.

Final review covered generated sqlc output, embedded schemas, populated migration up/down behavior, ownership interval boundaries, runtime-scoped NATS resources, runtime OpenAPI, first-party Adapter fixtures, and race-enabled SDK lifecycle tests.

Verified on 2026-08-31:

```sh
mise run validate
git diff --check
git status --short
```

All commands passed, and the worktree was clean before this documentation closeout.
