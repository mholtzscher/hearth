# Adapter health and projected Entity availability

Adapter health is authoritative in the Adapters module. Entity availability is
a separate Devices-owned projection delivered over NATS JetStream. SDK claim,
heartbeat, release, and availability-report contracts are unchanged.

## What an operator sees

`GET /v1/adapters/{adapter_id}` can show unhealthy while an Entity still shows
available. Commands still check authoritative runtime and health in their own
transaction, so the Command can fail as `adapter_unhealthy` before the display
catches up. Unknown health keeps its existing active-runtime eligibility rules.
There is no canonical-health overlay and no bounded catch-up latency guarantee.

Entity history records effective changes when Devices applies them. Its existing
receive-order cursors remain the ordering authority. An obsolete revision makes
no change. Revision gaps are allowed and do not fabricate skipped Entity history.
Source observation times remain diagnostic. Adapter history retains every
committed authoritative health transition.

Reports are admitted only for the current healthy runtime and are stamped with
the authoritative health revision. A report ahead of local health is retained
but not yet eligible. Once that healthy snapshot arrives, it can make the Entity
available without another report. Older health updates cannot erase newer reports.
An accepted request retry retains its original receipt and generation.

## Delivery and readiness

Core publishes immutable full snapshots on
`hearth.v1.core.adapter-health.<adapter_id>` through `HEARTH_ADAPTER_HEALTH_V1`.
The single Devices durable is `hearthd-device-availability-v1`. This is an internal
WorkQueue feed, not a supported multi-consumer event feed. A second independent
consumer requires a new retention decision.

The file-backed stream has no age expiry, a one-GiB capacity, a 16-KiB message
limit, and DiscardNew behavior. A full stream rejects publication while SQLite
retains the outbox row. Correct PubAck precedes deletion; Devices commits its
projection and all availability effects before acknowledgement. Broker duplicate
suppression is only an optimization. Semantic duplicates remain safe after its
two-minute window, even with fresh notification IDs.

Adapter health and Device Facts share the [platform relay](outbox-relays.md).
Both use batches of at most 64, five-second I/O bounds, a one-second retry delay,
and drain in their original worker. Their tables, transactions, and streams
remain separate.

Public `/readyz` checks resource compatibility, current relay health, and consumer
activity separately from the source dependencies used by lease expiry. A full
stream, corrupt outbox row, or stopped health consumer can fail public readiness
without preventing canonical lease expiry. Readiness does not promise convergence.
A terminated consumer requires Core restart; reconnect does not restart it.

## Recovery

Every Core startup queues a fresh notification of each immutable current head
before runtime ingress opens. This does not advance health revisions, restamp
snapshot evidence, change Adapter history, or replay external actions. Duplicate
snapshots add no Entity history. Older pending outbox rows remain ordered.

NATS reconnect resumes pending SQLite outbox publication and durable consumption.
It does not queue fresh snapshots or repair manually deleted delivery state.
Startup synchronization repairs current projection state, not skipped historical
transitions. Do not purge the queue to clear work. If delivery state is manually
deleted while Core is running, restart Core to initiate synchronization, even if
NATS has reconnected since the deletion.

Shutdown joins admitted public work, stops the existing health supervisor and
joins runtime ingress, drains and joins the sole relay, then drains and joins
the health consumer before closing SQLite or NATS. A timeout ends a wait, not
ownership of unfinished work. Pending
outbox rows and unacknowledged messages remain recoverable.

## Upgrade boundary and rollback

Migration `00015_adapter_health_projection.sql` adds health heads, publication
outbox, local projection, report revisions, and effective availability. Existing
IDs, history rows, timestamps, and cursors are untouched. Old Entity history
retains its original meaning; only new effective changes have application-time
semantics. Do not interpret old timestamps as processing times.

Matching reports from the current healthy legacy runtime are backfilled to
revision one. Startup establishes immutable revision-one legacy heads without
inventing Adapter history. Before their first projection, Entity availability can
be unknown. Eligible legacy reports survive that baseline, avoiding a resend
solely for this upgrade. Bootstrap reuses equivalent retained Entity transitions
instead of duplicating them just to initialize the cache.

Back up SQLite and broker state together before upgrading. Restore them
consistently, or recreate this internal stream and durable before starting an
older restored database. Never deliver queued higher revisions from a newer
database into a rolled-back one. Migration Down discards new publication and
projection data and cannot restore the previous same-transaction history
semantics. It is not a lossless binary rollback.
