# First light vertical slice

**Status:** Ready for task breakdown

**Implementation spec:** [`../../specs/first-light.md`](../../specs/first-light.md)

## Outcome

Observe and control the boolean power entity of one light currently managed by Home Assistant through an HTTP API. The Home Assistant adapter runs separately from the core, and a simulator exercises failure behavior through the same NATS contracts. A browser interface and configurable automations are out of scope.

## Why this slice

The slice must deliver visible household utility and meaningful NATS learning while validating a temporary migration seam. It must not leak Home Assistant concepts into core contracts.

## Resolved shape

- Use Go module `github.com/mholtzscher/hearthd` for the core, Home Assistant adapter, simulator, and thin adapter SDK.
- Fix the implementation stack to official `nats.go`, Echo v5, Huma v2, `modernc.org/sqlite`, Goose, sqlc, `coder/websocket`, `jsonschema/v6`, YAML v3, Google UUID, and OpenTelemetry propagation.
- Keep JSON Schemas plus a tiny Go `embed.FS` wrapper together in importable `contracts/v1`; core and SDK validate the same authoritative embedded schemas at runtime.
- Expose a concrete, stateless thin SDK `Session` with `Connect`, `Register`, durable `PublishObservation`, blocking ephemeral `ServeCommands`, and idempotent `Close` methods.
- Have `PublishObservation` retry one generated envelope through transient disconnects and return only after JetStream acknowledgement or context expiry; do not add a local outbox.
- Keep vendor behavior, discovery, upstream calls, refresh rules, credentials, and checkpoints outside the SDK.
- Serve Commands through a one-shot SDK responder: adapter code calls `Accept` before refresh or `Reject`; after acceptance it publishes the linked refresh Observation through the session.
- Propagate message, correlation, causation, and command IDs in JSON and W3C trace context in NATS headers.
- Use composition roots under `internal/app`, one cohesive `internal/modules/devices` product module, domain-neutral `internal/platform` packages, vendor code under `internal/adapters`, a public `sdk/adapter`, and checked-in `contracts/v1`; expose OpenAPI at runtime without a committed artifact.
- Serve HTTP with Echo v5 and Huma v2, with application assembly constructing the transport and the `devices` module owning operation registration; expose OpenAPI at runtime without committing a generated artifact.
- Bind the unauthenticated development API to loopback only.
- Return Entity metadata and nullable current State from `GET /v1/entities/{entity_id}`.
- Accept `{"operation":"set","value":true}` at `POST /v1/entities/{entity_id}/commands` and wait synchronously for the outcome.
- Return stable JSON error codes mapped to 409 overlap, 503 missing adapter, 502 upstream rejection, 504 outcome timeout, and 500 internal failure.
- Model independently addressable entities grouped by devices.
- Give the Home Assistant adapter instance a configured subject-safe slug.
- Register Device kind `light` and Entity kind `power` with boolean value type, writability, and `set`; reply with canonical IDs only after the binding and mappings commit. Defer manifests, discovery lifecycle, feature negotiation, configuration schemas, and checkpoints.
- Bind one configured Home Assistant light with a stable adapter-scoped binding key and a boolean power entity; preserve canonical IDs when its external identifier changes, and defer brightness, color, transitions, effects, and general discovery.
- Treat the Home Assistant adapter as disposable migration code and delete it after this household completes migration.
- Assign typed, UUIDv7-based canonical and message IDs in the core and map Home Assistant external IDs onto them.
- Encode versioned wire messages with exact envelope fields `id`, `schema`, `emitted_at`, required `correlation_id`, optional `causation_id`, and `data`; validate against JSON Schemas and declare incompatible major versions in both subjects and payload schema IDs.
- Encode all wire times as UTC RFC3339Nano and reject malformed or non-UTC values.
- Have the SDK generate adapter publication IDs and envelope metadata while adapter code supplies domain data and source times.
- Retain adapter observations in a file-backed JetStream Limits stream for seven days or one GiB, discard oldest messages, and consume with explicit acknowledgements, a 30-second ack wait, one pending acknowledgement, and unlimited redelivery; retain processed Observation IDs in SQLite for at least eight days and keep the receipt backing current State until superseded.
- Store canonical state, identity mappings, Observation receipts, and a minimal Command attempt history transactionally in the core's SQLite database using Goose migrations and sqlc-generated queries; keep generated types behind module persistence adapters.
- Record adapter acquisition time as `observed_at`, optional upstream last-change time as `source_updated_at`, and core receive time as `received_at`.
- Order canonical State by `observed_at`, break ties deterministically by receive order, and reject timestamps more than one minute ahead of the core clock.
- Advance State evidence and timestamps for a newer same-value observation while classifying it as unchanged.
- Give each observation an immutable ID and record it transactionally with its disposition and SQLite projection so exact redelivery is a no-op.
- Record and acknowledge stale observations without changing current state.
- Diagnose permanently invalid input in structured logs with safe metadata and acknowledge it while its raw message remains in the seven-day stream; leave transient infrastructure failures unacknowledged for redelivery; acknowledge valid outcomes only after commit.
- Send immediate commands on adapter-scoped Core NATS request/reply subjects with one fixed ten-second end-to-end deadline; never queue them for later delivery.
- Permit one in-flight command per entity and reject overlaps as conflicts.
- Keep Command delivery, outcome waits, and one-active-per-Entity enforcement ephemeral and in memory, while recording each attempt and terminal outcome in SQLite for diagnosis and future history; the record is audit data, not a queue.
- Commit the `requested` record before dispatch, mark it `satisfied` transactionally with the matching linked Observation, and persist terminal failures. After the `requested` record commits, proceed with dispatch and keep the in-memory guard until linked outcome or the ten-second deadline even if the HTTP client disconnects; core restart loses the wait and guard, marks active records `interrupted`, and never redispatches them.
- Hold the command HTTP request until outcome satisfaction or deadline failure.
- Dispatch even when canonical state already matches the target.
- After acceptance, have the adapter refresh upstream state and publish a fresh observation linked to the command ID.
- Call the requested outcome satisfied when that linked post-dispatch canonical observation matches it, without claiming the command caused that outcome.
- Use adapter-scoped version 1 subjects for registration, entity observations, and entity commands.
- Run NATS and Go processes natively through devenv during local development; defer containers until deployment work.
- Expose loopback `/healthz` and `/readyz`; report ready only after SQLite migration, NATS connection, JetStream provisioning, and observation-consumer startup. Adapters retry registration.
- Give the core, Home Assistant adapter, and simulator separate YAML files containing only their owned non-secret configuration; read the Home Assistant token from a separate local secret file.
- Defer adapter and Entity availability; expose last State timestamps and detect a missing adapter during command request/reply.
- Have the disposable Home Assistant adapter fetch a startup/reconnect snapshot, subscribe to state-change events, and explicitly refresh after accepted commands.
- Require the simulator to prove duplicate, stale, malformed, unavailable-adapter, upstream-rejection, no-op-refresh, outcome-timeout, and core-restart-before-ack cases.
- Verify behavior with pure `devices` module tests, temporary-SQLite repository and migration tests, Huma transport tests, SDK tests against in-process NATS, and a small process-level simulator suite.

## Task breakdown

The approved implementation contract, acceptance criteria, test strategy, ordered deliverables, and relative estimates are defined in [`specs/first-light.md`](../../specs/first-light.md). Implementation has not started.
