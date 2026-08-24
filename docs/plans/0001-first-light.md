# First light vertical slice

**Status:** Complete

**Implementation spec:** [`../../specs/first-light.md`](../../specs/first-light.md)

## Outcome

Observe and control the boolean power entity of one light currently managed by Home Assistant through an HTTP API. The Home Assistant adapter runs separately from the core, and a simulator exercises failure behavior through the same NATS contracts. A browser interface and configurable automations are out of scope.

## Why this slice

The slice must deliver visible household utility and meaningful NATS learning while validating a temporary migration seam. It must not leak Home Assistant concepts into core contracts.

## Resolved shape

- Use Go module `github.com/mholtzscher/hearth` for the core, Home Assistant adapter, simulator, and thin adapter SDK.
- Fix the implementation stack to official `nats.go`, Echo v5, Huma v2, `modernc.org/sqlite`, Goose, sqlc, `coder/websocket`, `jsonschema/v6`, YAML v3, Google UUID, and OpenTelemetry propagation.
- Keep JSON Schemas plus a tiny Go `embed.FS` wrapper together in importable `contracts/v1`; core and SDK validate the same authoritative embedded schemas at runtime. For Entity types, pair semantic schemas with a versioned language-neutral manifest and conformance examples; generate bindings, codecs, behavior, typed SDK facades, tests, and core catalog assembly with no handwritten per-type Go.
- Preserve the concrete, stateless SDK `Session` with `Connect`, generic `Register`, durable `PublishObservation`, blocking ephemeral `ServeCommands`, and idempotent `Close`; add typed routing and generated power/v1 and brightness/v1 facades that hide raw JSON and operation-name switching from Go adapter consumers.
- Have `PublishObservation` retry one generated envelope through transient disconnects and return only after JetStream acknowledgement or context expiry; do not add a local outbox.
- Keep vendor behavior, discovery, upstream calls, refresh rules, credentials, and checkpoints outside the SDK.
- Serve Commands through independent, potentially concurrent SDK handler invocations with one-shot responders: adapter code calls `Accept` before refresh or `Reject`; after acceptance it publishes the linked refresh Observation through the session. Adapters must be concurrency-safe and may serialize internally only when their vendor protocol requires it.
- Propagate message, correlation, causation, and command IDs in JSON and W3C trace context in NATS headers.
- Use composition roots under `internal/app`, one cohesive `internal/modules/devices` product module, domain-neutral `internal/platform` packages, vendor code under `internal/adapters`, a public `sdk/adapter`, and checked-in `contracts/v1`; expose OpenAPI at runtime without a committed artifact.
- Serve HTTP with Echo v5 and Huma v2, with application assembly constructing the transport and the `devices` module owning operation registration; expose OpenAPI at runtime without committing a generated artifact.
- Bind the unauthenticated development API to loopback only.
- Return Entity metadata and nullable current State from `GET /v1/entities/{entity_id}`.
- Accept `{"operation":"set","parameters":{"value":true}}` at `POST /v1/entities/{entity_id}/commands` and wait synchronously for the outcome.
- Return stable JSON error codes mapped to 503 missing adapter, 502 upstream rejection, 504 outcome timeout, and 500 internal failure.
- Model independently addressable entities grouped by devices.
- Give the Home Assistant adapter instance a configured subject-safe slug.
- Register Device kind `light` and Entity type `hearth.power/v1` with persisted names and support `{"state":{},"operations":{"set":{}}}`; operation-key presence means support. Return canonical IDs only after the binding, normalized `support_json`, and mappings commit. Return schema-defined permanent rejections for invalid descriptors, immutable Entity-type changes, and identity conflicts. Keep JSON Schemas authoritative for support, State, and parameters; carry generic JSON through transport/persistence while erasing typed definitions behind the concrete catalog. Resolve Commands against current support but preserve active outcomes through immutable callbacks, normalized parameters, and absolute deadlines. Defer runtime type and manifest loading, discovery lifecycle, feature negotiation, configuration schemas, and checkpoints.
- Bind one configured Home Assistant light with a stable adapter-scoped binding key and a boolean power entity; preserve canonical IDs when its external identifier changes. Keep the generated brightness/v1 type as catalog/tooling validation, but defer wiring brightness, color, transitions, effects, and general discovery into the first-light adapter.
- Treat the Home Assistant adapter as disposable migration code and delete it after this household completes migration.
- Assign typed, UUIDv7-based canonical and message IDs in the core and map Home Assistant external IDs onto them.
- Encode versioned wire messages with exact envelope fields `id`, `schema`, `emitted_at`, required `correlation_id`, optional `causation_id`, and `data`; validate against JSON Schemas and declare incompatible major versions in both subjects and payload schema IDs.
- Encode all wire times as UTC RFC3339Nano and reject malformed or non-UTC values.
- Have the SDK generate adapter publication IDs and envelope metadata while adapter code supplies domain data and diagnostic source times.
- Retain adapter observations in a file-backed JetStream Limits stream for seven days or one GiB, discard oldest messages, and consume with explicit acknowledgements, a 30-second ack wait, one pending acknowledgement, and unlimited redelivery; retain processed Observation IDs in SQLite for at least eight days and keep the receipt backing current State until superseded.
- Store canonical state, identity mappings, Observation receipts, and a minimal Command attempt history transactionally in the core's SQLite database using Goose migrations and sqlc-generated queries; keep generated types behind module persistence adapters.
- Record adapter acquisition time as `adapter_received_at`, optional upstream last-change time as `source_updated_at`, JetStream's server-assigned durable-receipt time as core-owned `observed_at`, and an internal SQLite logical sequence as `receive_order`.
- Order canonical State only by tie-free `receive_order`; use timestamps for operators/history. Accept an `adapter_received_at` more than one minute ahead of `observed_at` and log clock skew.
- Advance State evidence and timestamps for every first-seen, valid, correctly owned Observation; use the Entity-type catalog to classify an equivalent value as `unchanged` and a different value as `applied`.
- Give each observation an immutable ID and record it transactionally with its disposition and SQLite projection so exact redelivery is a no-op.
- Diagnose permanently invalid input in structured logs with safe metadata and acknowledge it while its raw message remains in the seven-day stream; leave transient infrastructure failures unacknowledged for redelivery; acknowledge valid outcomes only after commit.
- Send immediate commands on adapter-scoped Core NATS request/reply subjects with an operation-defined end-to-end deadline; the first `hearth.power/v1` `set` definition uses ten seconds. Never queue Commands for later delivery.
- Allow Commands for one Entity to overlap without a guard, queue, or supersession policy; give each an independent ID, durable record, in-memory waiter, and deadline, and allow only its own linked Observation satisfying the catalog outcome policy to complete it.
- Accept that interleaved Commands may both be satisfied at different receive orders, one may time out after another changes the Entity, and a successful outcome may be immediately superseded; canonical State continues to follow core receive order.
- Keep Command delivery and outcome waits ephemeral and in memory while recording each attempt and terminal outcome in SQLite for diagnosis and future history; the record is audit data, not a queue.
- Commit the `requested` record before dispatch, mark only that Command `satisfied` transactionally when its linked Observation satisfies the catalog outcome policy, and persist terminal failures. After the `requested` record commits, proceed with dispatch and retain that lifecycle until linked outcome or the operation-defined deadline even if the HTTP client disconnects; core restart loses active waits, marks active records `interrupted`, and never redispatches them.
- Hold the command HTTP request until outcome satisfaction or deadline failure.
- Dispatch even when canonical state already matches the target.
- After acceptance, have the adapter refresh upstream state and publish a fresh observation linked to the command ID.
- Call the requested outcome satisfied when that linked post-dispatch canonical Observation satisfies the registered operation's catalog outcome policy, without claiming the Command caused that outcome.
- Use adapter-scoped version 1 subjects for registration, entity observations, and entity commands.
- Run NATS and Go processes natively through devenv during local development; defer containers until deployment work.
- Expose loopback `/healthz` and `/readyz`; report ready only after SQLite migration, NATS connection, JetStream provisioning, and observation-consumer startup. Adapters retry transient registration failures but stop on permanent rejection.
- Give the core, Home Assistant adapter, and simulator separate YAML files containing only their owned non-secret configuration; read the Home Assistant token from a separate local secret file.
- Defer adapter and Entity availability; expose last State timestamps and detect a missing adapter during command request/reply.
- Have the disposable Home Assistant adapter subscribe before fetching each startup/reconnect snapshot, buffer and reconcile intervening state-change events by Home Assistant `last_updated`, handle concurrent WebSocket requests by request ID, and explicitly publish each accepted Command's own linked refresh.
- Require the simulator to prove duplicate, delayed-source-time, future-clock-skew, malformed, unavailable-adapter, upstream-rejection, no-op-refresh, overlapping-opposite-command, outcome-timeout, and core-restart-before-ack cases.
- Verify receive-order projection and source-time diagnostics with pure `devices` module tests, plus temporary-SQLite repository/migration tests, Huma transport tests, SDK tests against in-process NATS, and a small process-level simulator suite.

## Task breakdown

The approved implementation contract, acceptance criteria, test strategy, ordered deliverables, and relative estimates are defined in [`specs/first-light.md`](../../specs/first-light.md). The complete first-light slice, including the unified Entity-support amendment, runtime OpenAPI checks, recovery coverage, simulator matrix, and disposable Home Assistant adapter, is implemented.
