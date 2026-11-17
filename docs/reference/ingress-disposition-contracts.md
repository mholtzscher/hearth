# Ingress disposition contracts

Observation, Entity Event and availability ingress deliberately retain different
failure policies. They share transport/lifecycle mechanics, not recovery meaning.
None automatically repairs corruption, quarantines input, or gates readiness on
per-report progress.

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

The Observation durable serializes pending acknowledgements and permits
redelivery within the bounded stream's retention; the
[resource configuration](../../internal/modules/devices/nats/jetstream.go)
defines its pending limit and AckWait. A valid Observation
whose stored Entity support cannot be decoded is not invalid wire input. Its
projection error holds the pending slot, so later reports can remain queued. This
is an accepted operator-repair tradeoff, not a claim that ingress is progressing.

Repair may allow the original message to redeliver with the same identity;
fencing, retention expiry and other concurrent changes can still affect processing.
Terminating valid State evidence instead would remove this durable consumer's
automatic redelivery opportunity.

## Entity Event termination

Descriptor corruption terminates the affected report so later history can proceed.
Term does not delete raw bytes from the bounded LimitsPolicy stream, but repairing
the descriptor does not automatically replay the terminated report. There is no
disposition row for that report or supported manual replay workflow.

## Availability no-reply and readiness

An unexpected availability storage or receipt fault produces no reply. The requester
can time out even when a previously accepted receipt exists. A timeout alone does
not identify the failed boundary; inspect safe service diagnostics and retained
evidence before deciding how to recover.

`RuntimeReadiness.Check` checks infrastructure, configured resources and consumer
activity, not per-report progress or availability receipt integrity. These faults
alone do not fail that check. Outer readiness composition can fail for independent
reasons, including outbox relay faults. Readiness is not proof that every ingress
message can make progress.

## Evidence scope and operational cost

The [assembly corruption tests](../../internal/app/hearthd/ingress_corruption_integration_test.go)
and [Entity Event boundary tests](../../internal/modules/devices/nats/entity_event_integration_test.go)
exercise selected blocking, no-reply, termination and readiness consequences.
They do not measure production likelihood or establish that a Session stays active
through an outage. Fixture SQL is not a supported production repair procedure.
Investigate a real incident with backups and validated evidence; do not clear
pending messages or rewrite receipts blindly.

See [platform ownership](platform.md), [testing](../guides/testing.md), and the owner matrices
in `internal/modules/devices/nats`. Complete malformed/commit/Ack matrices stay at
their owner; the selected assembly tests add only corruption, blocking and readiness
evidence rather than duplicating those matrices.
