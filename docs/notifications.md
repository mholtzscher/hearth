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
- `get_notification`: required `id` (the attempt ID).

These tools use the same service and public DTOs as HTTP. Failed delivery outcomes
are successful tool results carrying evidence, not transport failures. Admission,
validation and lookup errors use structured `admission_closed`, `invalid_input`
and `not_found` failures; unexpected storage failures are generic `internal_error`.

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

Automation Notification Steps remain a separate follow-up slice.
The reserved internal identity/drain seam is verified with the existing Command
Run harness; this is not yet end-to-end proof of a persisted Notification Step
link or a retained Step outcome after its notification record is pruned. Provider
setup and real Discord smoke verification belong to the Discord adapter work.

Migration 00012 is additive. Its Down removes notification identities and evidence;
binary rollback requires a pre-feature database backup.
