# Core notifications and destinations

Status: Contract-reviewed proposal; ready for implementation planning. Effort: XL.
Includes the minimum runtime/session work required for notification adapters.
Consumers: [Discord](discord-notification-adapter.md) and
[Automation notification Steps](automation-notification-steps.md).

## Purpose and scope

Core owns adapter-registered destinations, immediate notification attempts,
durable outcome evidence and a manual-send API. Provider adapters own credentials
and external protocol translation. There is no delivery queue or automatic retry.
Notification history belongs to this module, independently of Automations. Both
manual and Automation sends appear in its list/detail APIs; Automation history
links to attempts rather than owning their records or retention.

## Types and identity

Proposed types owned by `internal/modules/notifications`:

```go
type DestinationID string
type AttemptID string
type Destination struct {
    ID DestinationID
    Name string
    Available bool
}
type Message struct { Title string; Text string }
type SendInput struct {
    AttemptID AttemptID
    DestinationID DestinationID
    Message Message
}
type Outcome string
const (
    Accepted Outcome = "accepted"
    Rejected Outcome = "rejected"
    NotSent Outcome = "not_sent"
    Unknown Outcome = "unknown"
)
```

IDs are immutable Core-generated identities; names are adapter-supplied display
labels. Automations reference IDs. Core privately maps each `(adapter_id, local_key)`
to one stable destination ID. Names can change without changing identity. Adapter
identity and local key are bounded subject-safe slugs. Secrets never enter Core.
There is no user-managed binding resource. Moving to another adapter or renaming
a local key creates a different destination and requires updating Automation
references; explicit migration is future work.

Proposed message limits: optional title up to 256 Unicode code points, required
nonblank text up to 2000; reject invalid UTF-8 and oversized input. Adapters may
reject content they cannot represent, never silently truncate it.

## Registration lifecycle

1. The operator configures named local destinations and secret references in the adapter.
2. The adapter claims a fenced runtime and registers its configured destinations.
3. Core idempotently records each destination and returns its stable ID.
4. Automations select those destinations through Core discovery.

Registration is idempotent and scoped to the active adapter runtime. A new runtime
must register a destination before that runtime can receive sends for it. Repeated
registration retains IDs. Registration is additive, not a complete-inventory
reconciliation: omission from a later registration does not remove a destination.
A new runtime must register its own routes; old destination identities and history
remain visible. If Core sends to a route the adapter no longer recognizes, the
adapter rejects it without an external request. Inventory removal management is
deferred. Graceful release or supervisor-owned lease expiry makes destinations
unavailable without deleting them. A transport disconnect can precede Core's lease
evidence, so discovery may temporarily still show available and a send may fail.
Core recovery preserves registration runtime IDs; routing eligibility is derived
against the existing persisted active runtime as specified below.

Send admission atomically snapshots destination identity, name, adapter, local key
and selected runtime before dispatch. Subsequent registration or runtime changes
cannot retarget that attempt. No CRUD or binding endpoints are exposed.

## Minimum runtime support

Extend the existing adapter session and SDK with destination registration and
notification serving. Existing lifecycle is device-coupled; extract only mechanics
the implementation actually requires, rather than making shared runtime extraction
a prerequisite. Preserve device health/availability transaction guarantees and
the public device SDK facade. Keep notification capability in `sdk/adapter`.
Use the established single-active-runtime claim, heartbeat, release and fencing
semantics. Notification adapters register no synthetic Devices or Entities.
Share domain-neutral mechanics; do not introduce a generic provider capability
framework as a prerequisite.

Registration requests contain a runtime-scoped local key and display name;
responses map that key to its canonical ID. Send requests contain
attempt identity, local key, message and absolute deadline. Outcomes use the four
alternatives below. Session/registration retries are idempotent; sends never retry.
The session seam and wire bounds are specified below.

## Send contract

`Send(context.Context, SendInput) (Attempt, error)` admits a process-owned bounded
attempt and waits for terminal evidence. HTTP disconnect does not cancel an
admitted attempt. Each invocation creates one attempt and dispatches at most once.
The manual API accepts no caller-provided attempt ID or idempotency key: repeating
an HTTP send is a new invocation and may produce another message. Internally,
Automation execution may reserve an identity to persist its Step link before
calling Send; that identity is correlation, not a public retry/deduplication API.

Resolve the current registration and persist its snapshot and initial attempt in
one admission transaction, then issue one NATS request with an absolute deadline. Never reroute an
attempt after publication. Adapters return a typed outcome, stable reason code
and optional opaque provider receipt. Raw upstream errors are not public evidence.
Store a receipt when supplied; do not look it up, reconcile it or use it to retry.

| Outcome | Evidence |
|---|---|
| accepted | Provider explicitly confirmed acceptance |
| rejected | Provider or adapter explicitly refused the request |
| not_sent | Core or adapter can establish no external submission occurred |
| unknown | External submission may have occurred; acceptance is unproven |

Timeout after dispatch is unknown, not proof of failure to deliver. Startup
terminalizes unfinished attempts conservatively without resending. A lost outcome
commit is likewise ambiguous. Persisted evidence gates successful return; storage
failure cannot be reported as confirmed acceptance. Shutdown closes public send
and Automation admission first, lets admitted Runs finish their current Step,
then closes notification service admission and joins its attempts before closing
NATS/SQLite. Public HTTP/MCP admission and internal service admission are separate
gates so an already-reserved Automation Step can still call Send during drain.

### Agreed execution and retention policy

- Use one fixed ten-second end-to-end deadline per admitted attempt, beginning at
  admission. No per-Step timeout setting. A timeout after possible submission is
  unknown; expiry proven to precede submission is not-sent.
  Capture start/deadline before the admission transaction. The deadline bounds
  eligibility for external work, not the final evidence commit: use a separate
  five-second persistence context to record the terminal outcome after expiry.
  Therefore the synchronous API may take longer than ten seconds to return.
- Permit parallel sends with no fixed concurrency limit in Core or the SDK,
  no ordering guarantee and no waiting queue. Each admitted attempt still has
  its own absolute deadline. Admission closes for shutdown or persistence failure,
  not because other sends are in flight.
- Retain terminal attempts, including title and message, for thirty days from
  terminal completion. The existing shared history-pruning worker owns scheduling;
  notifications owns eligibility. Never prune active attempts. Automation history
  retention neither extends nor shortens notification retention; a surviving Run
  retains its Step outcome even after its linked notification record is pruned.
- The browser lists destinations, supports manual test sends, and browses standalone
  notification history. Automation notification-Step authoring uses API/MCP first.
- Terminal outcomes are immutable. Ignore late responses after terminalization;
  a timeout recorded as unknown stays unknown and never revises an Automation Run.
  There is no retained late-response reconciliation path.
- If attempt or outcome persistence fails, latch notification admission closed
  and mark notification readiness failed until Core restart. Do not dispatch an
  attempt whose initial record could not be established, and do not invent a
  terminal outcome if its write fails. Already dispatched attempts may finish
  and attempt to record their evidence; they are never resent. Startup conservatively
  terminalizes unfinished records before reopening admission. This latch belongs
  to notifications; it does not independently close device Command admission.

## Proposed HTTP surface

```text
GET    /v1/notification-destinations
GET    /v1/notification-destinations/{id}
POST   /v1/notification-destinations/{id}/notifications
GET    /v1/notifications
GET    /v1/notifications/{id}
```

Use existing Huma Problem Details and bounded keyset pagination. Manual send
returns the persisted attempt, including non-accepted outcomes; malformed input,
unknown destination and unavailable admission remain HTTP errors. Discovery
includes current availability and never exposes webhook credentials. Attempt
history retains routing snapshots when destinations become unavailable.

## Layout

```text
internal/modules/notifications/    new — types, destination and send use cases
  sqlite/                         new — registration mappings, attempts, sqlc queries
  nats/                           new — registration and request/outcome transport
  api/                            new — Huma DTOs and operation registration
internal/app/hearthd/              modify — assembly, startup, drain, retention
contracts/v1/                     new schemas — send requests and outcomes
sdk/adapter/                      modify — destination registration and send serving
```

## Deliverables and acceptance

| ID | Outcome | Owner | Depends on | Acceptance |
|---|---|---|---|---|
| N0 | Extend existing sessions for notifications | SDK, devices, app | — | A0 |
| N1 | Idempotent destination registration and discovery | notifications, sqlite, nats, api | N0 | A1 |
| N2 | Durable immediate attempt lifecycle | notifications, sqlite, nats | N1 | A2, A3, A6 |
| N3 | Manual send and history APIs | api, app | N2 | A4 |
| N4 | Browser discovery, test send and standalone history | web/src/api, web/src/pages, web/src/App.tsx | N3 | A5 |

- A0: Competing runtime claims cannot both succeed; stale registration is rejected.
  Existing device registration, health, availability and Command guarantees pass
  unchanged, including atomic lease-expiry effects.
- A1: Repeated registration and adapter restart preserve destination IDs. Partial
  registration does not remove prior entries. A new runtime cannot receive sends
  for keys it has not registered; unknown local routes reject without external
  submission. Downtime retains history. New adapter/key pairs get different IDs.
  Race registration with admission and verify one coherent snapshot per attempt.
- A2: Accepted, refused, unavailable and ambiguous sends produce the documented
  evidence. Each admitted invocation issues at most one send; two manual API
  invocations are distinct attempts, with no cross-request deduplication promise.
- A3: Crash after dispatch/before outcome persistence yields unknown on recovery
  and zero replay. Reconnect cannot dispatch after the absolute deadline.
- A4: Exercise manual send against a controlled provider through actual NATS;
  verify standalone history includes manual and Automation attempts, remains
  independent of Automation history retention, and exposes no credentials.
  Verify cancellation independence.
- A5: In the browser, select a registered destination, submit one test message and
  inspect its independent history entry. Failed outcomes remain visible; pruned
  detail does not change a retained Automation outcome. Browser sends never retry
  automatically after an ambiguous HTTP error.
- A6: Admit more than four concurrent sends through Core and the SDK and verify
  each reaches the provider. Verify late responses, storage-fault latch, recovery and
  shutdown while an Automation has reserved its Step link. Verify outcome writes
  still run with their own persistence timeout after the external deadline expires.
  Close the adapter's notification subscription before releasing its Session;
  a concurrent admitted send receiving confirmed NATS no-responders must persist
  `not_sent / destination_unavailable`, without retry or provider execution.

Focused tests use the repository mise tasks; final verification is
`mise run validate`. Real Discord smoke verification belongs to the adapter spec.

## Code-grounded technical decisions

### Session ownership and recovery

`internal/modules/devices/sqlite/runtime.go` creates `adapter_instances` during
claim without requiring a Device. Reuse that claim/heartbeat/release service and
the existing SDK Session directly. Keep its present ownership for this slice;
do not extract an adapters module or change device health transactions.

Notifications owns destination mappings and stores the runtime that last registered
each destination. Its SQLite registration and send-admission transactions read
`adapter_instances` and `adapter_runtimes` to fence against the same persisted
active-runtime identity. This narrow read dependency is deliberate; notifications
never writes those tables or independently expires leases. Supervisor expiry wins
by transaction order, exactly as it does for devices.

Persist registrations across Core restart. A still-active same runtime can keep
using its registered routes; a successor must register them again. Destination
availability requires registration against the current active runtime and no
explicit unhealthy adapter health. Unknown health permits an attempt, matching
existing device admission. Availability is routing evidence, not a Discord probe.

### Registration and wire

Extend `sdk/adapter` with `RegisterNotificationDestination` and
`StartNotificationServer`, public notification message/request/outcome types, and
concurrent handler admission. Activate the serving subscription before registration
so a returned destination is routable. Reuse SDK lifecycle joining and health
reporting, without synthetic Entities or Command evidence responders.

Registration is one key/name pair per request. Key uses existing adapter slug
validation; name is trimmed, 1–200 Unicode code points. Reject unknown fields.
Core mints `ndst_` UUIDv7 identities and `ntf_` UUIDv7 attempt identities. Registration
retries for the same adapter/key preserve identity and update the display name.
No bulk inventory contract or global destination-count setting is introduced.

New strict schemas under `contracts/v1` use the existing Hearth envelope:

| Interaction | Runtime-scoped subject suffix | Request data | Response data |
|---|---|---|---|
| Register | `notification-destination.register` | `key`, `name` | destination ID or rejection code |
| Send | `notification.send` | attempt ID, local key, title, message, UTC deadline | attempt ID, outcome, reason code, optional receipt |

Both suffixes follow `hearth.v1.adapter.<adapter>.runtime.<runtime>.`.
Responses correlate with their request and validate identity. Registration retries
reuse identity/bytes; sends use one request/reply invocation with no retry. A
response cannot extend the deadline. SDK checks expiry before invoking a handler.
Neither broker persistence nor JetStream consumers are needed for sends.

### Evidence, errors and persistence

An attempt records ID, destination/name snapshot, adapter/key/runtime snapshot when
known, message, start/deadline/completion timestamps, outcome/reason and optional
receipt. Store an initial `running` row before any dispatch. IDs are unique; an
internal identity collision fails without dispatch, not as a replay lookup.

Unavailable registration/runtime: `not_sent / destination_unavailable`.
The existing `not_sent / adapter_busy` wire code remains recognized for adapter
evidence, but Core and the SDK do not emit it based on in-flight counts. Proven pre-submit expiry:
`not_sent / deadline_expired`. Unknown local key: `rejected / destination_unknown`.
After possibly dispatching, timeout is `unknown / outcome_timeout`; malformed or
lost responses are `unknown / invalid_response` or `unknown / transport_error`.
On restart, remaining running rows become `unknown / core_restarted` conservatively.
Only a confirmed NATS no-responders result proves no listener accepted the request;
persist it as `not_sent / destination_unavailable`. Other transport errors must
not invent non-delivery.

Add notification-owned destination and attempt tables in a new additive Goose
migration; keep sqlc queries under `notifications/sqlite`. Snapshot fields do not
cascade with resource deletion. History pages order by start time and ID, newest
first, with the existing 50-default/200-maximum keyset convention. An expired/pruned
attempt detail returns 404. Manual send returns 201 with the terminal attempt;
invalid input is 422, unknown destination 404, closed admission 503 and persistence
failure 500. Known but unavailable destinations yield a recorded not-sent attempt.

Append notifications to `newHistoryPruneTasks` in
`internal/app/hearthd/history_pruning.go`. Delete terminal rows strictly older than
the 30-day cutoff in batches of 500, using the existing startup/hourly worker.
No cascade or retention dependency on Automation history is permitted.

### Transport and browser integration

Follow module-owned Huma and MCP registration patterns in
`internal/modules/automations/api`. MCP exposes destination discovery, manual send
and notification history using the same service and DTOs. Add notification pages
under `web/src/pages`, API types/client calls under `web/src/api`, and navigation
in `web/src/App.tsx`. Both transports call the same process-owned Send service.

### Concrete boundary shapes

The types below complete the domain examples above. SDK types mirror values in
the SDK package; neither SDK nor adapters import the Core domain. SQL and wire DTOs
are mapped at their owning boundaries rather than exported as domain objects.

```go
type Registration struct { AdapterID, RuntimeID, Key, Name string }
type RouteSnapshot struct { AdapterID, LocalKey, RuntimeID string }
type Attempt struct {
    ID AttemptID
    DestinationID DestinationID
    DestinationName string
    Route RouteSnapshot
    Message Message
    StartedAt, Deadline time.Time
    CompletedAt *time.Time
    Outcome Outcome // empty only while running
    ReasonCode string // empty for running/accepted
    Receipt string // optional, accepted only
}
type AttemptFilter struct {
    DestinationID DestinationID // optional
    Limit int
    Cursor string
}
type AttemptPage struct { Items []Attempt; NextCursor string }
type DestinationPage struct { Items []Destination; NextCursor string }
```

Service entry points are `Register(ctx, Registration) (Destination, error)`,
`GetDestination(ctx, DestinationID) (Destination, error)`,
`ListDestinations(ctx, limit, cursor) (DestinationPage, error)`, `Send` as above,
`GetAttempt(ctx, AttemptID) (Attempt, error)` and
`ListAttempts(ctx, AttemptFilter) (AttemptPage, error)`. Manual transports mint
attempt IDs internally; Automation execution mints its reserved ID. No public
transport accepts one. Domain and repository entry points validate independent
input; repositories own transactional runtime eligibility checks.

SDK additions have this shape:

```go
type NotificationMessage struct { Title, Text string }
type NotificationRequest struct {
    AttemptID, LocalKey string
    Message NotificationMessage
    Deadline time.Time
}
type NotificationOutcome struct { Outcome, ReasonCode, Receipt string }
type NotificationHandler func(context.Context, NotificationRequest) NotificationOutcome
// Methods on *Session:
// RegisterNotificationDestination(ctx context.Context, key, name string) (string, error)
// StartNotificationServer(ctx context.Context, handler NotificationHandler) (*NotificationServer, error)
// (*NotificationServer).Close(ctx context.Context) error
```

`StartNotificationServer` subscribes and flushes before returning. Callers get an
explicit readiness boundary before registration without sleeps or polling. Only one server
can be active per Session. Close unsubscribes, prevents new handler admission and
joins handlers within the supplied context; Session.Close also owns this cleanup.

Registration JSON is `{"key":"household","name":"Household alerts"}`.
Success is `{"status":"accepted","destination_id":"ndst_..."}`; rejection
is `{"status":"rejected","reason_code":"..."}`. Registration codes are
`invalid_registration` and `stale_runtime`; transient persistence failure receives
no success response and remains retryable with the same request identity.
Send JSON uses `attempt_id`, `local_key`, `title`, `message`, `deadline`;
response uses `attempt_id`, `outcome`, `reason_code`, optional `receipt`.
Accepted responses omit reason; other outcomes require a code from the fixed
Core and Discord mappings in these specs, at most 64 ASCII slug characters. Only accepted
responses may include a receipt (1–256 code points). All objects reject unknown
fields; optional title is omitted or a string (empty allowed), never null.
The four schema IDs are `urn:hearth:schema:notification-registration-request:v1`,
`urn:hearth:schema:notification-registration-response:v1`,
`urn:hearth:schema:notification-send-request:v1` and
`urn:hearth:schema:notification-send-response:v1`.

Manual-send JSON is `{"title":"Freezer","message":"Too warm"}`. Public
attempt DTOs expose ID, destination ID/name, title/message, `status` (running or
one of the four outcomes), UTC timestamps, nullable `completed_at`, optional
reason and receipt. Private adapter/runtime routing is retained in storage but
not exposed by these APIs. Lists use `{items,next_cursor}` with nullable next
cursor; destination lists sort by ID, attempt lists by `(started_at,id)` descending.
Optional attempt `destination_id` filter is bound into its cursor. Unknown filtered
IDs return an empty page. Destination detail includes `available`; it is current
Core routing evidence, not a guarantee a connection has not just failed.

### Lifecycle limits and failure checks

Core uses its existing shared NATS connection and does not change its global
buffer policy. A request buffered during reconnect may arrive before its absolute
deadline; after that deadline the SDK rejects it before invoking the handler.
SDK admission starts concurrent handlers without a fixed concurrency cap.
The Discord client honors its context deadline and does not detach HTTP work.
Core tracks each admitted attempt until terminalization or persistence failure;
the SDK joins each handler until it actually returns. Cancellation alone does not
establish that an external HTTP request has stopped or was never submitted.

Validate response arrival against the deadline even if a select races timeout;
persist unknown for responses arriving late. Terminal writes are conditional on
the row still running. SDK validates runtime routing and known session fencing
before handling. A runtime fenced concurrently after admission can still have an
external request in flight: no design here can retract it or prove non-delivery.

Persist destination mappings indefinitely in this slice; retiring stale discovery
entries is deferred. Use unique `(adapter_id,local_key)` and attempt-ID constraints,
plus a history-order index. One additive Core migration creates these tables;
the Automation evidence migration follows it. Down migrations lose new evidence;
binary rollback requires a pre-feature database backup. Existing rows are unchanged.

### Review risks and verification

- A send may be accepted externally while Core loses the response. Preserve unknown
  and never retry; verify with a dropped-response provider and restart after dispatch.
- Direct runtime-table reads could diverge from device rules. Keep them confined to
  notification SQLite queries, reuse active/ended/health semantics, and exercise
  claim, expiry, release and registration races in integration checks.
- History retention is independent: verify pruning in either order leaves the other
  module's retained evidence readable.

Implementation deliverables additionally update the notification gap analysis and
operator documentation under `docs/`, with N3 owning Core usage, Discord D3 owning
setup and Automation A3 owning Step usage. There are no open product decisions.
