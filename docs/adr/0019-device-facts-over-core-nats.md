# Publish Device Facts over Core NATS

**Status:** Superseded in part by [ADR 0020](0020-durable-device-facts-via-outbox.md), which replaces the live-only delivery mechanism with a transactional outbox, one JetStream stream and a consumer-owned recovery policy. The fact namespace, subject grammar, wire schemas, stable fact identity and Command exclusion decided here still stand.

## Decision that still stands

Hearth publishes versioned **Device Facts** on `hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>` for accepted first-seen Observations (`applied`/`unchanged`) and accepted first-seen Entity Events. Two strict v1 schemas (`observation-fact`, `entity-event-fact`) under `contracts/v1` define the wire contract, and each fact carries a stable `fct_<UUIDv7>` envelope identity that is also its broker message identity, with the durable `obs_`/`evt_` source ID as `causation_id`. A fact is queue-eligible exactly when the devices transaction that establishes the evidence accepts it.

Command lifecycle is excluded from the fact surface: a Command status transition is already authoritative in the SQLite `commands` table and the HTTP Command read APIs, an Observation is not a Command lifecycle substitute because a stateless (`dispatched`) or failure outcome produces no accepted Observation, and a Command fact would publish Core-internal lifecycle rather than observable device evidence. No Command schema, subject, family token, outbox row or sink method exists, and Command lifecycle is readable only through durable SQLite and HTTP history.

## Superseded decision (historical)

The original form of this decision delivered facts over plain Core NATS immediately after the devices transaction committed: no JetStream, acknowledgement, retry, outbox, offset, replay, catch-up or Core-owned retention, no fact HTTP endpoint and no fact stream; a dedicated fact connection that disabled reconnect buffering; both Core connections bounded to one second of socket write; one generation-fenced epoch tracker over both connections that suppressed Observation and Entity Event backlog drained from JetStream after a Core restart or reconnect; and a single bounded dispatcher queue that isolated committed devices work from NATS write latency and dropped rather than carried facts across connection generations. The reasoning at the time was that delivery was required to be live-only, that durability already lived in SQLite and the inbound streams, and that facts were never reconstructed or replayed. [ADR 0020](0020-durable-device-facts-via-outbox.md) supersedes that mechanism, because a queued fact must survive a relay restart, a broker outage and a Core process exit, and because downstream readers — not Core — must own their recovery position.

## Trade-offs that still apply

- External facts over in-process callbacks, so one contract serves automations and language-neutral external consumers while `devices` imports neither.
- Entity-first subjects over family-first subjects, because the canonical Entity is the stable routing identity and one Entity subscription covers every family.
- Variant in the subject and the payload over payload-only routing, so subscribers filter event names and dispositions without decoding unrelated messages.
- A stable `fct_` identity over reusing the source ID, because each durable fact keeps one identity that is also its broker deduplication key while the durable source identity stays explicit.
- Two schemas and two families over one generic union, so invalid family/data combinations stay unrepresentable and consumers validate only their family.
- Observation and Entity Event families only over a Command lifecycle family, because accepted State and event evidence is Core-verified data and Command status already has an authoritative durable HTTP/SQLite history that Observations cannot substitute for.

## Consequences

Consumers must tolerate duplicates and stay idempotent on the stable fact identity, and must not sort by UUID or envelope timestamp to invent a global order. Command lifecycle has no fact at all, so a consumer that needs a Command outcome must read durable HTTP/SQLite history and must not infer it from Observation facts. Security is unchanged and this decision widens no deployment boundary: anyone with broker access may read canonical State values or forge a fact, so facts are unsigned and the expected future policy reserves the Core fact namespace for Core and denies it to Adapters and subscribers.
