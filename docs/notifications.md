# Core notifications

Core owns destination discovery and durable, immediate notification attempts.
Adapters own credentials and provider requests. No synthetic Devices or Entities,
delivery queue, automatic retry, or receipt reconciliation is involved.

## Discover and test a destination

Configure local keys, display names and secret references in the provider adapter.
It must claim a Session, start its notification server, then register destinations.
Core returns stable `ndst_` identities for each adapter/local-key pair. Repeated
registration updates the name without changing the identity; omitted entries are
not removed. A successor runtime must register its own routes. Moving a key or
adapter creates a different identity, not a migration.

With Core on a trusted loopback listener:

```sh
curl http://127.0.0.1:8080/v1/notification-destinations
curl http://127.0.0.1:8080/v1/notification-destinations/<ndst_id>
curl -X POST http://127.0.0.1:8080/v1/notification-destinations/<ndst_id>/notifications \
  -H 'Content-Type: application/json' \
  -d '{"title":"Freezer","message":"Too warm"}'
curl http://127.0.0.1:8080/v1/notifications?limit=50
curl http://127.0.0.1:8080/v1/notifications/<ntf_id>
```

The send body accepts only optional string `title` (up to 256 Unicode code points)
and required nonblank string `message` (up to 2000). Null, unknown fields and
caller-supplied attempt IDs are rejected. **Every invocation creates a new attempt.**
Do not automatically repeat a send after an ambiguous HTTP/MCP error: another
invocation may deliver another message. An admitted attempt continues after the
client disconnects and can be inspected through history.

Availability is current Core routing evidence, not a provider connectivity probe.
Graceful release, supervisor-owned expiry, unhealthy health, or a successor that
has not registered a route makes it unavailable. Discovery can briefly lag a
transport disconnect. Destination identities remain visible indefinitely.

## Read the evidence

A valid manual send returns HTTP 201 with the persisted terminal attempt, even
when it did not succeed:

| Status | Meaning |
| --- | --- |
| `accepted` | Provider explicitly confirmed acceptance, not human receipt |
| `rejected` | Provider or adapter explicitly refused |
| `not_sent` | No external submission is established |
| `unknown` | Submission may have occurred; acceptance is unproven |

Known unavailable destinations record `not_sent / destination_unavailable`.
Core and the SDK allow concurrent sends without a fixed concurrency limit,
waiting queue or ordering guarantee. Each send retains its own ten-second deadline.
After possible dispatch, timeout records `unknown / outcome_timeout`; late
responses do not revise terminal evidence. An opaque accepted receipt is stored
but never used to query or retry the provider.

The external-work deadline is ten seconds from admission. Outcome persistence
has its own five-second context, so synchronous completion can take longer.
Malformed input is 422, unknown destinations/details are 404, closed admission
is 503, and persistence failure is 500. Storage failure fails only the affected
attempt, without a sticky admission or `/readyz` failure. Readiness continues to
reflect current dependency and worker health. A later independent send can proceed
if its own admission write commits; there are no queued retries or external replay.
An initial write failure never dispatches; an outcome write failure leaves running
evidence for conservative startup recovery, without a cleanup retry. Core never
reports acceptance without durable evidence or revises an earlier attempt merely
because a later send succeeds. Device Command admission is unaffected.

History includes IDs, destination/name snapshots, title/message, status, UTC
timestamps, nullable `completed_at`, and optional reason/receipt. Running records
are visible during execution. Private adapter/runtime/key snapshots and secrets
are not public. Treat messages and receipts as sensitive household data: the
HTTP and MCP listeners are unauthenticated and must remain on trusted networks.

Lists return `{items,next_cursor}` with a null cursor at the end, default to 50
and accept limits 1–200. Destination lists sort by ID; attempts sort newest first
by `(started_at,id)`. `/v1/notifications` accepts optional `destination_id`, which
is bound into its cursor. An unknown canonical filtered ID returns an empty page.
Reuse the opaque cursor with the same filter. Pages are positions, not snapshots.

## MCP

The existing `/mcp` catalog exposes exactly five notification tools:

- `list_notification_destinations`: optional `limit`, `cursor`.
- `get_notification_destination`: required `destination_id`.
- `send_notification`: required `destination_id`, `message`; optional `title`.
- `list_notifications`: optional `destination_id`, `limit`, `cursor`.
- `get_notification`: required `id`, the Attempt ID from `notification_id`.

These tools call the same domain service as HTTP but own independent payloads.
Destination results use `destination_id`. Notification lists omit message text
and provider receipts. Sends return `notification_id`, `destination_id`, actual
`status`, and any reason or receipt; use `get_notification` for message and
completion evidence. Accepted means provider acceptance, not verified delivery.
Non-accepted outcomes are successful tool results carrying evidence, not
transport failures. Admission, validation and lookup errors use structured
`admission_closed`, `invalid_input` and `not_found` failures; unexpected storage
failures are generic `internal_error`. Terminal collection pages omit
`next_cursor`. Tool discovery publishes input schemas only, with no resources.

## Recovery, shutdown and retention

Before serving, startup marks unfinished attempts `unknown / core_restarted`
without sending them again. Recovery failure prevents startup. Shutdown closes
public notification admission before joining admitted Automation Runs. Internal
send remains open for already-reserved identities until Automation drain finishes;
then notification admission closes and every attempt joins before NATS/SQLite
close. Provider adapters should close their notification subscription and join
handlers before releasing their Session.

The shared startup/hourly history worker deletes terminal attempts strictly older
than thirty days from completion, in batches of 500. Active records are never
pruned; pruned detail returns 404. Retention is eligibility, not an exact TTL.
Notification history does not cascade to or from Automation history.

In the browser, open **Destinations**, select a destination, enter an optional
title and required message, then choose **Send test message**. Inspect the returned
attempt or open **History for this destination**; **Notifications** lists standalone
history across destinations, including non-accepted outcomes. An ambiguous send
error is never retried automatically: check history before sending again.

## Validation boundaries

The constructor requires a repository and sender before attempting recovery.
Missing collaborators are internal construction failures, not invalid notification
input. Recovery failures retain `ErrPersistence` and the original cause.

The Service supplies send/completion times and deadlines. Core supplies recovery
and retention times. Existing Service and repository checks reject invalid
operation metadata as internal precondition failures. Repository checks remain
independent of Service validation. Malformed send input, completion evidence,
identifiers, and pagination or batch limits keep `ErrInvalidInput`; invalid
metadata does not bypass those checks. No clock fallback, dispatch retry, or
history repair is added.

## Completion conflicts

Repository completion writes only an existing running attempt. Missing attempts
return `ErrAttemptNotFound`. Repeating the exact outcome, reason, opaque receipt
and completion instant returns the retained attempt without changing it; equal
instants in different time zones are the same completion.

Any mismatch against already-terminal evidence returns `ErrCompletionConflict`
and no Attempt. This includes a changed receipt, reason, outcome or timestamp, and
a stale completion after startup recovery established `unknown / core_restarted`.
Established terminal evidence is never overwritten. Completion inputs are still
validated before duplicate comparison.

The Service preserves that conflict alongside `ErrPersistence`, does not adopt
the earlier attempt as the proposed send's outcome, and never retries completion
or provider dispatch. HTTP keeps its safe 500 fallback and MCP keeps
`internal_error`; no completion API or new public 409 code is introduced. This
repository contract is not send idempotency: reserved attempt-ID collisions still
reject, and every public send uses a fresh identity. Duplicate completions are
synthetic regression cases; current production Send makes one completion call.

## Automation notification Steps

Create or replace an Automation through HTTP (`POST /v1/automations`,
`PUT /v1/automations/{id}`) or MCP (`create_automation`, `replace_automation`).
Insert a fixed-destination notification leaf at top level or inside If/Choose sequences:

```json
{
  "id": "notify-freezer",
  "kind": "notification",
  "destination_id": "<discovered ndst_ identity>",
  "title": "Freezer warning",
  "message": "The freezer has been too warm for ten minutes."
}
```

The destination must exist when saving but may be unavailable. Title is required
but may be empty. Supply each literal string or its mutually exclusive `title_expr`
or `message_expr` alternative. Message bounds match manual sends and are checked before reservation.
See [Automation expressions](automation-expressions.md). Only reached leaves send.
The retained revision owns bindings and destination identity; resolved strings
commit with the execution reservation;
notifications snapshots routing when admitting the send. Acceptance permits the
next Step; rejected, not-sent or unknown outcomes stop the Run. Neither restart
nor an ambiguous result retries or resumes a Step.

HTTP/MCP Run detail includes notification rows in its single `executions` timeline.
Each reached entry exposes `step_id`, global zero-based `sequence`, `kind`,
`phase`, timestamps, and kind-owned evidence with reserved `attempt_id`, resolved
`inputs`, and optional authoritative `outcome`/`reason_code`. A pre-reservation
value failure has neither an attempt ID nor provider outcome. A reserved link may
never have admitted a send.
Startup interruption records `core_restarted` without inventing provider evidence.

The browser renders notification definitions and immutable Run evidence read-only.
Enablement replacement preserves the complete definition, including nested leaves;
notification authoring remains API/MCP-only. Attempt links open independent
notification history. A missing/pruned attempt returns 404 and does **not** turn
a retained accepted Step into a failure. Unknown means submission may have
occurred, not proof of non-delivery. Provider setup is documented in the
[Discord adapter guide](discord.md); real Discord smoke verification remains an
explicit operator-only check requiring credentials.

Migration 00012 is additive. Its Down removes notification identities and evidence;
binary rollback requires a pre-feature database backup.
Migration 00013 adds Run-owned notification Step evidence; pruning a Run removes
only its evidence, never notification attempts. Current migration 23 preserves
post-22 executions/revisions and labels earlier values unavailable; it does not
invent historical notification inputs. Use a matching backup for binary rollback.
