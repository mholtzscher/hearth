# Ingress disposition contracts

Observation, Entity Event and availability ingress deliberately retain different
failure policies. They share transport/lifecycle mechanics, not recovery meaning.
The #242 investigation confirmed the consequences with isolated corruption and
the user approved preserving the policies below. No automatic repair, quarantine,
new readiness gate or retry policy is introduced.

## Per-consumer matrix

| Input/result | Observation | Entity Event | Availability request/reply |
| --- | --- | --- | --- |
| Malformed envelope, identity or route | Ack permanent invalid input; no projection | Ack permanent invalid input; no record | Discard without reply |
| Stored corruption | Projection errors remain unacknowledged | Only `ErrEntityEventDescriptorCorrupt` selects Term; other record faults select delayed Nak | Unexpected storage/receipt faults receive no reply |
| Transient storage failure | No Ack/Nak/Term; broker AckWait redelivery | Nak with `EntityEventRedeliveryDelay` | No reply; transport does not retry the request |
| Context cancellation/deadline returned by storage | Same unacknowledged projection-failure path | Same delayed Nak path (unless descriptor-corruption identity also matches) | Same no-reply fallback, not an invented rejection |
| Successful commit, including domain rejection or duplicate | Ack after projection commits | Ack after recording commits | Accepted receipt or documented domain rejection |
| Ack/Term/Nak/reply publication fails | Safe diagnostic; committed evidence is not undone | Safe diagnostic; committed evidence is not undone | Safe diagnostic; no substitute reply or automatic retry |

Missing broker metadata remains unacknowledged before either durable decoder.
Availability's known fenced/unhealthy/invalid/unknown-Entity/wrong-owner cases keep
their explicit rejection responses. Context results in the table describe returned
dependency errors, not a guarantee of delivery during shutdown. Stop/close still
joins callbacks through the existing platform primitives.

## Observation blocking and repair

The Observation durable has `MaxAckPending: 1`, a 30-second AckWait and unlimited
redelivery attempts within the bounded stream's retention. A valid Observation
whose stored Entity support cannot be decoded is not invalid wire input. Its
projection error holds the pending slot, so later reports can remain queued. This
is an accepted operator-repair tradeoff, not a claim that ingress is progressing.

The isolated SQLite/NATS test corrupts `entities.support_json`, verifies zero
Observation rows, one pending acknowledgement and one queued message, then restores
only that fixture's valid support. The original message redelivers with the same
ID at the unchanged AckWait, commits as applied, and releases the next report,
which commits as unchanged. No republish or new identity is used.

This fixture does not run the health supervisor or lease-expiry loop. It preserves
an active runtime row; it does not establish that a production Session remains
active throughout an outage. Fencing, retention expiry and other concurrent
changes can affect subsequent processing. Terminating valid State evidence instead
would remove this durable consumer's automatic redelivery opportunity; that policy
change is not authorized.

## Entity Event termination

The existing real-corruption test uses a single pending slot and the broker's Term
advisory to distinguish termination from Ack. It verifies no corrupt-report row,
no redelivery, a later valid committed report and an active consumer. Term does not
delete raw bytes from the bounded LimitsPolicy stream, but repairing the descriptor
does not automatically replay the terminated report. No disposition row or new
manual replay workflow is invented.

## Availability no-reply and readiness

The new request/reply fixture first accepts a real availability request, corrupts
its retained receipt timestamp, and repeats the same request twice. Each callback
reaches the real service and returns a storage error; each requester times out
without a reply. Restoring the captured receipt bytes only in that fixture lets
the same request succeed again. A timeout alone would not prove the storage path,
so the test observes the callback result as well as the actual NATS response.

`RuntimeReadiness.Check` remains healthy during both selected ingress faults: it
checks infrastructure, configured resources and consumer activity, not per-report
progress or availability receipt integrity. This is not full production Core or an
HTTP `/readyz` test; outer readiness composition can fail for independent reasons,
including outbox relay faults. No sticky operation-failure gate is added.

## Evidence scope and operational cost

Tests inject deliberate database corruption; they do not measure its production
likelihood or demonstrate an actual spontaneous outage. The head-of-line and
no-reply consequences are concrete, while occurrence frequency remains unknown.
New quarantine, automated data repair, replay or progress-gating machinery would
add storage/lifecycle and operator contracts, so the approved resolution uses
documentation and regression tests only. Fixture SQL is not a supported production
repair procedure. Investigate a real incident with backups and validated evidence;
do not clear pending messages or rewrite receipts blindly.

See [platform ownership](platform.md), [testing](testing.md), and the owner matrices
in `internal/modules/devices/nats`. Complete malformed/commit/Ack matrices stay at
their owner; the selected assembly tests add only corruption, blocking and readiness
evidence rather than duplicating those matrices.
