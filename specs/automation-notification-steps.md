# Automation notification Steps

Status: Contract-reviewed proposal; ready for implementation planning. Effort: L.
Depends on [Core notifications](core-notifications.md). Discord is the first
end-to-end integration, not a dependency of the Automation domain.

## Purpose

Allow any reached Automation sequence to send a static title/text message to a
adapter-registered destination. Preserve sequential execution, immutable snapshots,
single-active-Run admission and non-resumable restart behavior.

## Definition and consumer seam

Proposed addition to `internal/modules/automations/definition.go`:

```diff
 type StepBody interface{ isStepBody() }
+
+type NotificationStep struct {
+    DestinationID notifications.DestinationID
+    Title string
+    Message string
+}
+
+func (NotificationStep) isStepBody() {}
```

Proposed consumer-owned interface:

```go
type AutomationNotifications interface {
    GetDestination(context.Context, notifications.DestinationID) (notifications.Destination, error)
    Send(context.Context, notifications.SendInput) (notifications.Attempt, error)
    GetAttempt(context.Context, notifications.AttemptID) (notifications.Attempt, error)
}
```

Wire example:

```json
{
  "id": "notify-freezer",
  "kind": "notification",
  "destination_id": "<Core-issued destination ID>",
  "title": "Freezer warning",
  "message": "The freezer has been too warm for ten minutes."
}
```

Definition validation enforces the shared message limits and destination identity.
Proposed policy: require destination existence at save time, but permit unavailable
destinations so definitions can be prepared while adapters are offline. Recheck
current registration and runtime at Step execution. Provider availability is not
a definition-validity condition.

## Execution and evidence

- Support notification leaves at top level and inside If/Choose sequences.
- The immutable Run snapshot holds destination identity and message. The
  notifications module atomically snapshots the destination and registered routing
  when the Step is reached. Automation definitions contain no adapter identity or
  local route. An unavailable destination fails without provider dispatch. Moving
  to another adapter/key requires selecting its new destination ID in the definition.
- Reserve a stable attempt identity and persist the Step-to-attempt link before
  calling Send. Ownership checks must prevent adopting an unrelated attempt.
- Continue only after durably confirmed `accepted`. Rejected, not-sent and unknown
  attempts fail the Run, retaining the distinct notification outcome in history.
- Persist failures before attempting later Steps. A persistence error cannot
  invent success or trigger an automatic resend.
- Unselected/unreached notification leaves have no external attempt. A reached
  notification retains the Automation's active slot while awaiting its result.
- Restart interrupts Runs and never resumes notification Steps. Shutdown ordering
  leaves notifications available until admitted Automation sends have drained.

The existing Command-attempt representation is command-specific. Add typed
notification evidence rather than placing notification IDs in Command fields.
Commands retain their existing stable positions and outcome semantics.
Notifications owns the authoritative attempt history and its independent read
APIs/retention. Automation history links to the attempt and records the Step
outcome; pruning a Run does not delete notification history.

## Layout

```text
internal/modules/automations/
  definition.go                   modify — sealed Step variant
  execution*.go                   modify — notification dispatch and reconciliation
  automation-definition.schema.json modify — strict notification definition
  sqlite/                         modify — Step evidence and durable links
  api/                            modify — definition/history DTOs
internal/app/hearthd/              modify — dependency injection and drain order
```

HTTP, MCP and browser consumers must all understand the additive variant using
the integration paths specified below.
Initial browser scope is destination discovery, manual test sends and notification
history plus read-only notification-Step definitions/evidence. Notification-Step
authoring is through API/MCP, not a new browser editor in this slice.

## Deliverables and acceptance

| ID | Outcome | Owner | Depends on | Acceptance |
|---|---|---|---|---|
| A1 | Typed variant and strict validation | automations, schema | N1 | V1 |
| A2 | Execution and evidence integration | execution, sqlite, app | N2, A1 | V2, V3 |
| A3 | Public contract/client support | API, MCP, browser | A2 | V4 |

- V1: Valid top-level and nested notification definitions round-trip. Invalid
  message bounds, destination references and unknown fields fail validation.
- V2: Only selected branches send. Accepted sends permit the next Command;
  rejected/not-sent/unknown sends stop it and retain attributable history.
- V3: Crash between Step-link persistence, dispatch and outcome persistence never
  resends. Concurrent registration cannot change an admitted attempt snapshot.
  Adapter restart preserves destination references. A runtime without registration
  for that destination cannot receive sends; an unknown local route rejects
  without external submission. Partial registration does not remove prior entries.
- V4: HTTP/MCP history and browser rendering distinguish notification attempts
  from Commands and show unknown outcomes truthfully. Existing Command-only,
  delay-only and branching behavior remains intact.

Run focused mise tests while implementing, then `mise run validate`. A deployed
Discord workflow is a final manual integration check with external credentials.

## Resolved integration decisions

- Count Commands and notifications together toward the existing 32 side-effect
  leaf bound. Keep the 64-node and depth-eight bounds. Notification-only Runs are
  valid; Command leaf positions retain their existing Command-only numbering.
- Add a separate `automation_run_notifications` evidence table, following
  `00011_automation_run_delays.sql`: Run-owned reached evidence with Step ID,
  position, reserved attempt ID, timestamps, Step outcome and reason. It cascades
  only with its Run; its attempt ID is a logical link, not a cascading foreign key
  to notification history. Never insert it for an unreached leaf.
- Always return a `notifications` evidence array in Run detail, alongside existing
  Commands, branch decisions and delays. Keep stored old definitions/history
  readable; they produce an empty array. Add the new definition variant to the
  current contract version, shipping Core, MCP and browser together. Older clients
  need upgrading to interpret/edit notification Steps; no compatibility decoder
  or destructive database reset is required.
- A reserved ID whose send was never admitted can have no notification row. Record
  the Step's pre-attempt failure explicitly; never invent a provider outcome.
  Missing destination maps to `destination_not_found`, closed notification
  admission to `notification_admission_closed`, and evidence persistence failure
  follows the existing executor-fault latch. A known unavailable destination has
  a real not-sent attempt and fails with `destination_unavailable`.
- After acceptance, retain the Step outcome in Run evidence. Pruning notification
  history may make its link return 404; render that as expired detail, not a failed
  Step. Notification pruning cannot remove Run evidence and vice versa.
- Extend `internal/modules/automations/api/history.go`, the canonical definition
  and history schemas, and existing `api/mcp_*` mappings. Update
  `web/src/pages/automation-step-tree.ts`, `AutomationStepTree.tsx` and
  `AutomationDetailPage.tsx` for read-only rendering. The browser must prevent an
  unsupported editor round-trip from silently dropping notification nodes.

## Evidence contract and lifecycle checks

Each reached notification evidence entry contains `step_id`, `position`,
`attempt_id`, `status` (`running`, `accepted`, `failed`, `interrupted`),
`started_at`, nullable `completed_at`, and optional `outcome`/`reason_code`.
Position is zero-based notification reach order, independent of Command leaf
positions. Provider outcome is absent for a pre-attempt failure and retained for
a confirmed terminal attempt. `accepted` requires outcome `accepted`; failed
includes rejected/not-sent/unknown or a documented pre-attempt failure.
Startup marks running Step evidence interrupted with `core_restarted` without
inventing provider evidence; standalone notification recovery owns its own rows.

Resolve GetDestination on definition create/replace. During execution, compare
the returned attempt's reserved ID, destination and message with the immutable
Step before adopting it. Any missing/unverifiable terminal evidence after Send
is an executor fault, never acceptance. A notification persistence fault closes
notification admission; Automation's existing fault latch may additionally close
Automation admission if it cannot establish its own progress.

Extend V1 with combined 32-leaf boundary cases and unavailable-but-known references.
Extend V3 with shutdown after link reservation/before Send, crash before attempt
creation, independent pruning in both directions and mismatched attempt evidence.
Extend V4 with an older persisted Run returning `notifications: []` and editor
protection for notification-bearing definitions. Use the existing focused mise
tasks and final `mise run validate`; exercise the browser checks manually against
the running app. No old database reset is part of this additive feature.
