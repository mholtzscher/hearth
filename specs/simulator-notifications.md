# Native simulator notifications

Status: D1–D3 implemented in the working tree; live validation recorded below.
Effort: L. Not part of the Automation notification PR.

## Purpose and decision

Replace temporary SDK notification providers with native scripted destinations.
Use the existing production SDK on the managed simulator's NATS/Core stack,
with loopback HTTP controls and repeatable validation scripts. Do not emulate
Discord or introduce a second Core. Configuration-only outcomes would be smaller
but cannot deterministically test pending sends; a full transport fault lab would
add unnecessary scope. Recommend configured outcomes plus hold/release control.

## Implementation contract

`internal/app/simulator/config.go` gains an optional per-Adapter collection:

```diff
 type ScriptedAdapterConfig struct {
     AdapterID string                `yaml:"adapter_id"`
     Devices   []scripted.DeviceSpec `yaml:"devices"`
+    Notifications []scripted.NotificationSpec `yaml:"notifications"`
 }
```

New types owned by `internal/adapters/scripted/notifications.go`:

```go
type NotificationSpec struct {
    Key string `yaml:"key"`
    Name string `yaml:"name"`
    Outcome string `yaml:"outcome"`
    ReasonCode string `yaml:"reason_code"`
    Hold bool `yaml:"hold"`
}
type NotificationBehavior struct {
    Outcome string `json:"outcome"`
    ReasonCode string `json:"reason_code,omitempty"`
    Hold bool `json:"hold"`
}
type NotificationDestinationInfo struct {
    AdapterID string `json:"adapter_id"`
    Key string `json:"key"`
    DestinationID string `json:"destination_id"`
    Behavior NotificationBehavior `json:"behavior"`
}
type NotificationCapture struct {
    AdapterID string `json:"adapter_id"`
    Sequence uint64 `json:"sequence"`
    AttemptID string `json:"attempt_id"`
    DestinationID string `json:"destination_id"`
    LocalKey string `json:"local_key"`
    Message adapter.NotificationMessage `json:"message"`
    ReceivedAt time.Time `json:"received_at"`
    Deadline time.Time `json:"deadline"`
    Behavior NotificationBehavior `json:"behavior"`
    FinishedAt *time.Time `json:"finished_at"`
}
```

Keys are unique per Adapter, using SDK slug/name bounds. Outcomes are accepted,
rejected, not_sent, unknown; default accepted. Reuse notification outcome/reason
validation, not Entity health reason rules. Reject unsupported combinations at
configuration/control boundaries. Notification-only Adapters are permitted;
require at least one Device or destination. Existing Device-only YAML remains valid.

Extend the app-owned `scriptedSession` seam in `run_scripted.go` with the existing
SDK `StartNotificationServer` and `RegisterNotificationDestination` signatures.
Start the notification server before registration and retain Core-issued IDs.
Readiness includes destination registration and active handler serving. Supervise
both serving loops: failure cancels sibling workers and joins them before Session
close. Do not leave notification workers unobserved behind Command serving.
`StartNotificationServer(ctx, handler)` returns only after subscription activation;
the returned server's `Wait(ctx)` observes termination after admitted handlers join.
Waiting does not cancel handlers: the app owns the parent context and shutdown.

Each handler snapshots behavior and records the exact received attempt ID/message
before replying. Held requests wait on a per-attempt release signal or context
cancellation; never hold the runtime mutex while waiting. Behavior updates affect
future requests only. Release is idempotent while an attempt remains captured;
unknown IDs return 404, and release never sends or retries a notification.

Loopback controls in `internal/app/simulator/control_notifications.go`:

| Endpoint | Contract |
| --- | --- |
| GET /v1/sim/notifications/destinations | All registered destination snapshots, including Adapter ID |
| PUT /v1/sim/notifications/destinations/{id}/behavior | Strict complete NotificationBehavior replacement; 400 invalid, 404 unknown |
| GET /v1/sim/notifications/requests?adapter_id=sim-healthy&after_sequence=N&limit=100 | Object: items array, metadata array, has_more; exclusive per-Adapter cursor |
| POST /v1/sim/notifications/requests/{attempt_id}/release | Release a captured held handler; 404 unknown |

Capture storage is in-memory and bounded (1,000 entries per Adapter). Evict oldest
finished entries only; if all entries are pending, reject new handler work with
valid not_sent/adapter_busy evidence. Global capture listing includes Adapter ID
and per-Adapter sequence; cursors are per Adapter, requiring an adapter_id query
when after_sequence is supplied. Return truncation metadata so absence after
eviction cannot be mistaken for proof of no send. Captures reset on simulator
restart; Core retains authoritative attempt history. Do not expose a control send
endpoint: manual sends use Core's existing API.

Metadata includes `oldest_sequence`, `latest_sequence`, `highest_evicted_sequence`,
`highest_dropped_sequence`, `dropped_count` and sticky `truncated`, per Adapter.
Latest includes overflow admissions; dropped includes evictions and uncaptured
overflow, including pre-Attach SDK receipts without a known canonical ID.
These return not_sent/adapter_busy and increment latest/dropped metadata.
Oldest alone cannot detect holes behind pending entries. Count/absence
checks use a saved latest cursor and require highest dropped at or below that
cursor, no runtime reset and no remaining page. Capture `message` uses SDK
`Title`/`Text` fields; Core attempts use `title`/`message`.

## Layout and deliverables

```text
internal/
  adapters/scripted/
    notifications.go             new — behavior, capture, hold/release runtime
    notifications_test.go        new — bounds, snapshots, cancellation/concurrency
  app/simulator/
    config.go                    modify — destination config and validation
    run_scripted.go              modify — SDK serving/registration lifecycle
    control.go                   modify — notification controls
    config_scripted_test.go      modify — additive configuration acceptance
    run_scripted_test.go         modify — lifecycle and readiness evidence
    control_test.go              modify — HTTP validation and exact-ID capture
configs/scripted.simulator.yaml  modify — native accepted/rejected/unknown destinations
scripts/validate-simulator-notifications.sh new — bounded live validation
.agents/skills/simulator-validation/references/
  http-control.md               modify — controls and Core evidence distinction
  validation.md                 modify — repeatable notification scenarios
```

| ID | Outcome | Owner | Depends | Acceptance | Effort |
| --- | --- | --- | --- | --- | --- |
| D1 | Validated config and concurrent runtime | scripted notifications + simulator config | — | V1 | M |
| D2 | SDK lifecycle, destination discovery and controls | simulator run/control files | D1 | V2 | M |
| D3 | Native presets and repeatable live checks | config, script, skill references | D2 | V3 | M |

## Acceptance and validation

- V1: Old Device-only presets load unchanged; notification-only config works;
  duplicate keys and invalid outcome/reason pairs fail. Captured messages remain
  exact; behavior changes cannot mutate in-flight requests; bounded capture,
  cancellation and repeated release behave as documented.
- V2: Core lists registered destinations; simulator captures share Core attempt IDs.
  Held sends can be released; serving failures stop sibling workers. Restart with
  unchanged Adapter/key preserves Core destination identity.
- V3: Script discovers ports with mise env --json, pauses/restores relevant tickers,
  creates temporary definitions, injects simulator Events and matches exact Event
  IDs to Core Run causes. Verify accepted then satisfied simulated Command;
  rejected/not_sent/unknown stop Commands; unselected branches produce no capture
  or attempt; held send retains busy admission; Core restart interrupts without
  resend. Save JSON evidence before cleanup. Every poll has a deadline (90s max)
  and HTTP timeout; never blindly retry an ambiguous mutation.

Use GO_PACKAGES='./internal/adapters/scripted ./internal/app/simulator'
with mise run --skip-deps test, lint and vet, then mise run validate. Start/stop
only through mise run simulator-start/simulator-stop. The new validation script
runs against that stack and does not manage a second simulator/Core.

## Limits and risks

No Discord delivery, browser controls, malformed wire injection, database write
faults or precise persistence crash windows. Those remain external integration or
owner-boundary tests. Explicit unknown replies are not transport-timeout evidence.
Provider-offline behavior can be exercised by stopping the managed simulator;
native not_sent replies alone do not prove Core rejected before dispatch.

Main risks: cancellation leaks (mitigate with bounded waits and serving-worker
join tests), and interpreting captures as durable delivery (always reconcile via
Core attempt and Run APIs). Further registration/fencing fault controls are a
separate follow-up, not required for this slice.

## Resolved live-validation assumptions

- Native presets are five destinations on the existing healthy Adapter; held
  behavior is accepted plus hold. Rejected/provider_rejected,
  not_sent/adapter_busy and unknown/provider_error are valid configured pairs.
- The executable script uses Bash plus Mise's Python, saves all HTTP evidence,
  bounds polls at 90 seconds and requests at two seconds, and never retries a
  mutation whose transport result is ambiguous. It requires the existing managed
  default stack and restores original pause/behavior controls and deletes only
  its temporary definitions. Configuration and durable data remain intact.
  Each invocation records a unique marker and exact definitions before create;
  ambiguous creates are reconciled by paginated discovery during cleanup, never
  POST retry. Changed definitions are not deleted; unresolved ambiguity is an error.
- Managed Core stop/start exercises graceful interruption, not a forced crash.
  Expected pending-attempt recovery is unknown/core_restarted. Run interruption
  may be committed during shutdown; notification Step evidence remains
  interrupted. A three-second post-readiness capture/count window establishes
  observed no resend, not precise crash-window or indefinite guarantees.
- Managed simulator stop/start preserves destination IDs by Adapter/key; captures
  reset. Pause/behavior controls are restored after this reset; ticker phase and
  startup publications are not preserved.
- Final repository-wide `mise run validate` belongs to root integration after
  this slice; focused checks and live evidence do not substitute for it.

### D3 live evidence (2026-10-12)

Managed default stack: Core `http://127.0.0.1:11080`, simulator
`http://127.0.0.1:11181`; Mise 2026.10.8. The live script passed selected and
unselected branches for all four outcomes, held/busy/release, Core restart
interruption/no observed resend, and simulator restart identity preservation.
Evidence: `.data/simulator-stack/notification-validation/20261012T090820/`.

Accepted attempt `ntf_01a13071-6689-7abb-b4b0-6f8508f1655a` belongs to Run
`arn_01a13071-6683-749c-9205-49270fb111fe` caused by exact Event
`evt_01a13071-6678-76f7-93be-7fcdaa081429`; its following Command
`cmd_01a13071-6695-7aa4-8b5d-dfff59c7c6d9` is satisfied. Rejected,
not_sent/adapter_busy and unknown/provider_error Runs leave the Command
not_attempted. Restarted held attempt `ntf_01a13071-7adf-7a94-9922-f9b4047e394a`
is unknown/core_restarted; its Run and notification Step are interrupted, with
no later Command. Independent capture and Core attempt counts show one send,
not two, across the bounded post-readiness observation window.

Focused race tests, lint (zero issues), and vet passed for
`./internal/adapters/scripted ./internal/app/simulator`. Cleanup reported no
errors and no remaining temporary Automations; original controls were restored.
Managed logs were inspected before `mise run simulator-stop`; data/evidence
remain available. No forced-crash-window or external delivery claim is made.
The final executable rerun after tightening the poll budget also passed:
`.data/simulator-stack/notification-validation/20261012T091046/`, with clean
restoration and managed stack shutdown.

### Reviewer P2 follow-up evidence (2026-10-12)

- Real SDK registration-race regression failed before the fix: its pre-Attach
  receipt had all-zero metadata. After the fix it reports latest/dropped sequence
  1, dropped count 1 and sticky truncation; the next captured receipt is sequence 2.
- `TestNotificationWaitCancellationLeavesHandlersRunningAndAllowsLaterJoin`
  proves observer timeout leaves an admitted handler running and the subscription
  accepting another real request, then both return accepted and a later Wait joins.
  Both lifecycle tests passed five shuffled race-detector repetitions.
- Focused race tests, lint (zero issues), and vet passed for
  `./internal/adapters/scripted ./internal/app/simulator ./sdk/adapter`.
- Native live rerun passed all scenarios with exact attempt/Run notification
  terminal reason assertions. Evidence:
  `.data/simulator-stack/notification-validation/20261012T091859/`; cleanup had
  no errors, remaining Automations or ambiguous creations.
- Live lost-response fault injection discarded the response only after real Core
  committed creation. The script aborted without retrying POST, discovered and
  deleted that enabled definition, and preserved a similar-name unrelated
  definition and all pre-existing definitions. Evidence:
  `.data/simulator-stack/notification-validation/20261012T092002/lost-create-proof.json`.
  Its failed result is the expected injected timeout; cleanup was clean.
- Final executable rerun, including interrupted Step reason `core_restarted`,
  passed: `.data/simulator-stack/notification-validation/20261012T092129/`.
  Cleanup reported no errors, remaining Automations or ambiguous creations.
  Managed logs had no ERROR/panic evidence; `mise run simulator-stop` succeeded.
- Full `mise run validate` was attempted. Repository lint (zero issues), vet,
  generated fixture execution, frontend build and all 69 frontend tests passed.
  Six existing real-Mosquitto tests failed during testcontainers container creation
  with `reaper: context deadline exceeded`: both Adapter `TestPahoDialerAgainstMosquitto`
  tests (ecowitt/zigbee2mqtt), both app `TestRunConnectsNATSAndMosquitto` tests,
  and zigbee2mqtt app `TestRunProjectsColorBulbAndLinksColorCommand` and
  `TestRunProjectsRelayAndTemperatureDevices`. This was the earlier default-Ryuk
  Docker/testcontainers environment failure, not the final validation result.
  Generation, formatting and
  module tidying introduced no additional tracked changes.

### Cleanup discovery deadline follow-up (2026-10-12)

- Cleanup now creates one deadline and passes it through the outer poll, every
  paginated discovery scan and each HTTP request. Each request uses the lesser
  of two seconds and the remaining budget. Neither pagination nor a repeated
  scan renews the deadline; the production 88-second budget is unchanged.
- Practical evidence used the production functions against a delayed loopback
  HTTP server with injected short deadlines: continuing pagination stopped in
  0.251 seconds for a 0.25-second budget (four requests; final timeout 0.005s),
  and two incomplete scans stopped in 0.750 seconds for a 0.75-second budget
  (second request timeout 0.169s). Both retained unresolved ownership rather than
  reporting cleanup success. Harness/result:
  `/tmp/opencode/notification-discovery-deadline.py` and
  `/tmp/opencode/notification-discovery-deadline-result.json`.
- `bash scripts/validate-simulator-notifications.sh` passed all native scenarios
  after `mise run simulator-start`. Evidence:
  `.data/simulator-stack/notification-validation/20261012T092751/`.
  Cleanup had no errors, remaining Automations or ambiguous creations.
  Managed logs had no ERROR/panic evidence; `mise run simulator-stop` succeeded.
- Final `TESTCONTAINERS_RYUK_DISABLED=true mise run validate` passed in this
  worktree, independently confirming the root session's successful validation.
  This explicitly disables testcontainers' Ryuk reaper; the earlier default-Ryuk
  timeout is retained above as historical evidence, not a remaining test failure.
