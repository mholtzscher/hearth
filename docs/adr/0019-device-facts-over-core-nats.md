# Publish Device Facts over plain Core NATS

Hearth will publish versioned **Device Facts** on `hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>` using plain Core NATS after the devices-owned SQLite transaction commits: accepted first-seen Observations (`applied`/`unchanged`), accepted first-seen Entity Events, and every actual durable Command status transition including startup interruption. Three strict v1 schemas (`observation-fact`, `entity-event-fact`, `command-fact`) under `contracts/v1` define the wire contract, each publication gets a fresh `fct_<UUIDv7>` envelope identity with the durable `obs_`/`evt_`/`cmd_` source ID as `causation_id`, and the `devices.DeviceFactSink` seam is called only after repository success, never inside a transaction.

Facts are an at-most-once, live-only notification surface: no JetStream, acknowledgement, retry, outbox, offset, replay, catch-up or Core-owned retention, and no fact HTTP endpoint. A dedicated fact connection disables reconnect buffering, while both Core connections bound socket writes to one second so the dispatcher's synchronous generation check and shutdown join cannot wait on either connection's mutex for the client's one-minute default. One generation-fenced epoch tracker over both connections suppresses Observation and Entity Event backlog that JetStream drains after a Core restart or reconnect, so it can never appear as a live fact. A single bounded dispatcher queue isolates committed devices work from NATS write latency and drops rather than carrying facts across connection generations. Durable SQLite and the HTTP read APIs remain authoritative, and a missing fact proves nothing about the underlying activity.

## Trade-offs

- External Core NATS facts over in-process callbacks, so one contract serves automations and language-neutral external consumers while `devices` imports neither.
- Plain NATS over JetStream, an outbox or publication metadata, because delivery is required to be live-only and durability already lives in SQLite and the inbound streams; facts are never reconstructed or replayed.
- Entity-first subjects over family-first subjects, because the canonical Entity is the stable routing identity and one Entity subscription covers every family.
- Variant in the subject and the payload over payload-only routing, so NATS subscribers can filter event names, dispositions and statuses without decoding unrelated messages.
- A new `fct_` identity per publication over reusing the source ID, because each Command transition is a distinct message while durable source identity stays explicit.
- Three schemas and three sink methods over one generic union, so invalid family/data combinations stay unrepresentable and consumers validate only their family.
- Every Command transition over terminal-only facts, so external consumers see the complete durable lifecycle instead of a selectively lossy projection.
- Combined connection epochs over a fixed age window, because no arbitrary TTL or Adapter-clock dependency is needed; slow live processing stays eligible while outage backlog is suppressed.
- A separate no-buffer connection over disabling buffering on the shared connection, so fact reconnect behavior changes without regressing existing Command and request/reply recovery.
- One bounded dispatcher worker over synchronous NATS writes on devices paths, so a socket stall cannot consume Command deadlines or delay durable acknowledgements.

## Consequences

Consumers must tolerate duplicates and permanent loss, stay idempotent on the durable source ID and variant, and must not sort by UUID or envelope timestamp to invent a global order. There is no automatic catch-up, and an HTTP recovery read must not become one. Freshness assumes synchronized Core and NATS clocks with no skew tolerance; a suppressed fresh report is logged as a safe clock-skew diagnostic and durable history stays correct. Readiness gains the dedicated fact connection and an active dispatcher but never requires a subscriber. Security is unchanged: anyone with broker access may read canonical State values and Command parameters or forge a fact, so facts are unsigned and this decision widens no deployment boundary; the expected future policy reserves `hearth.v1.core.fact.>` publish for Core and denies it to Adapters and subscribers.
