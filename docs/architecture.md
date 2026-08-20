# Architecture

**Status:** Evolving; only constraints backed by accepted decisions are stated here.

## Accepted constraints

- One trusted Hearthd deployment owns one household; version 1 has no separate Site or Home object.
- Home Assistant must ultimately be removable from operation, but specialist protocol services may remain.
- Home Assistant connectivity runs in a disposable migration adapter process separate from the core, communicates across a NATS contract, and is deleted after this household completes migration.
- Core models and wire contracts contain no Home Assistant identifiers, service-call vocabulary, payloads, or lifecycle assumptions.
- The core, simulator, and first-party adapters use module path `github.com/mholtzscher/hearthd`. The fixed implementation stack is Go, official `nats.go`, Echo v5, Huma v2, `modernc.org/sqlite`, Goose, sqlc, `coder/websocket`, `jsonschema/v6`, YAML v3, Google UUID, and OpenTelemetry propagation; wire contracts remain language-neutral.
- A thin Go adapter SDK exposes a session facade for registration, durable Observation publication, and ephemeral command serving. `PublishObservation` retries one generated envelope through transient disconnects and returns only after JetStream acknowledges persistence or the call context expires. The SDK has no local outbox or other durable state.
- The SDK propagates explicit message, correlation, causation, and command IDs in JSON and W3C trace context in NATS headers.
- One cohesive `devices` product module owns first-slice identity, reconciliation, state projection, and command behavior; those concerns split only after distinct rules and change pressure justify new seams.
- Devices group independently addressable entities; state reads and commands target entities.
- Hearthd assigns immutable canonical IDs to devices and entities; adapter-specific external IDs are mappings and ownership can change without changing canonical identity. Canonical and message IDs are UUIDv7 values with type prefixes such as `dev_`, `ent_`, `obs_`, and `cmd_`.
- The first light is bound through explicit configuration and an idempotent registration handshake using minimal descriptors: Device kind `light`; Entity kind `power`, boolean value type, writable, operation `set`. The core replies with canonical IDs only after transactionally committing the binding, Device, Entity, and external mappings. General discovery, manifests, feature negotiation, configuration schemas, and checkpoints are deferred.
- Each adapter instance has a configured subject-safe slug. Each configured external object has a stable adapter-scoped binding key so registration preserves canonical IDs when external identifiers change; ambiguous binding/external-ID conflicts reject registration until explicitly reconciled.
- Milestones must provide household value and meaningful NATS learning.
- A production use of NATS must solve a concrete durability, isolation, routing, or observability problem.
- Version 1 wire contracts use a compact Hearthd envelope and versioned JSON checked against language-neutral JSON Schemas. Its exact top-level fields are `id`, `schema`, `emitted_at`, required `correlation_id`, optional `causation_id`, and `data`. Canonical schema JSON and a tiny `embed.FS` wrapper live together in importable `contracts/v1`; the core and SDK validate the same embedded schemas at runtime. Incompatible major versions appear in both the subject prefix and each stored payload's schema identifier.
- Wire timestamps are UTC RFC3339Nano strings; malformed or non-UTC values are rejected at the contract edge.
- The SDK generates adapter publication IDs and envelope metadata while adapters supply domain data and source times.
- Adapter observations are retained in a file-backed JetStream Limits stream for seven days or one GiB, whichever is reached first, discarding oldest messages. The core uses one explicit-ack durable consumer with a 30-second acknowledgement wait, one maximum pending acknowledgement, and unlimited redelivery. Processed observation IDs remain in SQLite for eight days so any retained redelivery stays idempotent.
- Each observation records `observed_at` (adapter acquisition time), optional `source_updated_at` (upstream last-change time), and core-assigned `received_at`. Canonical State is ordered by `observed_at`, with receive order as a deterministic tie-breaker. Observations more than one minute ahead of the core clock are rejected.
- A newer same-value observation advances canonical State's observation ID and timestamps and is classified as unchanged rather than duplicate or stale.
- The core owns canonical state and external-ID mappings transactionally in SQLite; NATS KV is not used for these models in the first slice.
- SQLite migrations use Goose and query code is generated in one platform database package with module-organized query sources; generated persistence types do not cross the `devices` module seam.
- Every observation has an immutable ID that the core records transactionally with its disposition and state projection, making exact JetStream redelivery a no-op.
- A valid stale observation is recorded and acknowledged without changing current state.
- Permanently invalid observations are diagnosed in structured logs with safe payload metadata and acknowledged; raw input remains in the seven-day stream. Transient infrastructure failures remain unacknowledged for redelivery. Valid applied, duplicate, and stale observations are acknowledged only after the SQLite transaction commits.
- Immediate commands use adapter-scoped Core NATS request/reply with a fixed ten-second end-to-end deadline and are not queued for later adapter recovery.
- Only one command may be in flight per entity; an overlapping HTTP request is rejected as a conflict rather than queued.
- Command lifecycle and one-in-flight-per-Entity enforcement are in memory only. After dispatch, the core keeps the guard until linked outcome or the ten-second deadline even if the HTTP client disconnects; a core restart loses the wait and guard. Commands have no SQLite audit record or recovery behavior.
- `GET /v1/entities/{entity_id}` returns Entity metadata and current State; a registered but never-observed Entity returns `state: null` rather than 404 or synthetic state.
- `POST /v1/entities/{entity_id}/commands` accepts `{"operation":"set","value":true}` for the first power Entity and waits synchronously for outcome satisfaction or deadline failure.
- Command failures use stable JSON error codes and status mappings: overlap 409, missing adapter 503, upstream rejection 502, outcome timeout 504, and internal failure 500.
- During native development the unauthenticated HTTP API binds only to loopback; network exposure requires a later authentication and deployment decision.
- Each first-slice process owns a separate YAML file for non-secret configuration; the Home Assistant adapter reads its token from a separate local secret file.
- The first slice does not model adapter or Entity availability; reads expose the last State and observation time, while command request/reply detects a missing adapter.
- The disposable Home Assistant adapter obtains a current snapshot at startup and reconnect, subscribes to state-change events, and explicitly refreshes current state after accepted commands.
- A command is dispatched even when canonical state already matches its requested target.
- After upstream acceptance, the adapter actively refreshes state and publishes a fresh observation linked to the command ID, including when no state-change event occurred.
- A command outcome is satisfied only after the linked post-dispatch canonical observation matches the requested value; this claims that the outcome was observed, not that the command caused it.
- First-slice NATS subjects are adapter-scoped and include canonical entity IDs where available: `hearth.v1.adapter.<adapter>.register`, `hearth.v1.adapter.<adapter>.observation.<entity>`, and `hearth.v1.adapter.<adapter>.command.<entity>.set`.

- Echo v5 and Huma v2 provide HTTP transport; application assembly constructs them, while the `devices` module owns operation registration and transport mapping. OpenAPI is exposed at runtime and is not committed as a generated artifact.
- Local development runs Go binaries and NATS natively through devenv; container packaging is deferred until deployment work.
- The core exposes loopback `/healthz` and `/readyz`; readiness requires migrated SQLite, NATS connectivity, provisioned JetStream resources, and an active observation consumer. Adapters retry registration until the core is available.

## Undecided

Exact message and HTTP contracts, production deployment topology, automation semantics, frontend architecture, native adapter scope, and all later roadmap technology choices remain proposals until reviewed.
