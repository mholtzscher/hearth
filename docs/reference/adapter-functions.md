# Adapter functions

For stage-specific persistence classes, joined conflict/context identities and
their consumers, see [persistence error contracts](persistence-error-contracts.md).

Adapter Functions expose Adapter-owned capabilities outside Entity Operations.
Core discovers their schemas, validates values, routes each invocation once, and
retains evidence. Core does not interpret a function's name or vendor fields.
Functions do not replace Commands. Notification sends remain; the Discord
`send_message` function exposes the same webhook send through the Function API
as an alternative invocation path.

Automations can invoke Functions with fixed arguments using a
[Function Step](../guides/automation-function-steps.md). Copy the discovered
`contract_fingerprint` rather than a Session catalog revision into the Step.
Only retained success advances a Run; there is no retry or restart replay.

## Discover, describe, invoke, inspect

The [Pirate Weather Adapter](../guides/pirateweather.md) registers `get_hourly_forecast`
(`hours`: 1–48) and `get_daily_forecast` (`days`: 1–8) for its configured location.
Each requests fresh provider data and returns bounded, unit-explicit periods with
retrieval time and timezone. These read-only Functions never update Entity State;
the Adapter's separate poll loop owns current-condition Observations. No forecast
cache or automatic invocation retry is used.

The Zigbee2MQTT Adapter registers `refresh_entity`. It requests the selected
Entity's readable upstream properties with one QoS 1, non-retained MQTT `/get`.
Success means PUBACK, not a fresh Hearth State or a physical response. Read-only
gettable sensors and Core-disabled Entities can refresh without gaining Commands.
An Entity without readable properties returns `refresh_unsupported`.

These commands are operator examples. Invocation can send a real radio request.
Use the configured Adapter slug and an Entity ID returned by your installation.

```sh
CORE=http://127.0.0.1:8080
ADAPTER=zigbee2mqtt
ENTITY_ID='<canonical Entity ID from GET /v1/entities>'

curl -fsS "$CORE/v1/adapters/$ADAPTER/functions"
curl -fsS "$CORE/v1/adapters/$ADAPTER/functions/refresh_entity"
REVISION=$(curl -fsS "$CORE/v1/adapters/$ADAPTER/functions/refresh_entity" |
  jq -er .catalog_revision)

jq -n --arg revision "$REVISION" --arg entity "$ENTITY_ID" \
  '{catalog_revision:$revision,arguments:{entity_id:$entity}}' |
  curl --fail-with-body -D - -X POST \
    "$CORE/v1/adapters/$ADAPTER/functions/refresh_entity/invocations" \
    -H 'Content-Type: application/json' --data-binary @-

curl -fsS "$CORE/v1/adapters/$ADAPTER/function-invocations?function_key=refresh_entity"
curl -fsS "$CORE/v1/adapter-function-invocations/afi_<returned-UUIDv7>"
```

The POST waits for retained terminal evidence and returns HTTP 200 for every
modeled outcome, with a `Location` header for the invocation detail. A successful
refresh result is `{"entity_id":"ent_...","request_status":"published"}`.
Ordinary device reports still follow the existing Observation path. Refresh
creates no Command, matcher, synthetic Observation, or synthetic State.

The invocation must include the discovered `catalog_revision`. Restarting an
Adapter creates a new Session and catalog revision. A stale revision returns 409
`catalog_changed` without an invocation or dispatch. Rediscover the function and
decide whether a new call is appropriate; do not resubmit automatically.

Collections are paginated; see
[pagination validation](../../internal/modules/adapters/function_pagination.go) for
current limits. Pass the returned `next_cursor` unchanged. A function-list cursor
belongs to one Adapter and catalog revision. A history cursor belongs to one Adapter and optional
`function_key` filter. History pages are not snapshots; a removed function's
retained invocations remain readable.

The Discord Adapter registers `send_message`. It sends a one-off message to a
configured destination through the same webhook path as notification sends:
one POST, one embed, mention parsing disabled, no retry. Arguments are
`destination` (a configured local key), `text` (required), and `title`
(optional). Function discovery advertises the configured keys in the
`destination` enum; use one of those values. A notification destination display
name is not accepted. A successful result is
`{"destination":"<key>","receipt":"<discord-message-id>","status":"sent"}`.
Local validation failures report `not_executed`, provider refusals report
`failed`, and ambiguous outcomes report `unknown`; all carry the same bounded
reason codes as notification attempts. Arguments and successful results are
retained in Function history under the rules above. An ambiguous outcome is
not permission to retry: inspect history first, since a repeat creates another
invocation and may duplicate the message.

```sh
CORE=http://127.0.0.1:8080
ADAPTER=discord-main

curl -fsS "$CORE/v1/adapters/$ADAPTER/functions"
REVISION=$(curl -fsS "$CORE/v1/adapters/$ADAPTER/functions/send_message" |
  jq -er .catalog_revision)

jq -n --arg revision "$REVISION" \
  '{catalog_revision:$revision,arguments:{destination:"household",text:"Too warm",title:"Freezer"}}' |
  curl --fail-with-body -D - -X POST \
    "$CORE/v1/adapters/$ADAPTER/functions/send_message/invocations" \
    -H 'Content-Type: application/json' --data-binary @-
```

## HomeKit bridge

The [HomeKit Adapter](../guides/homekit.md) registers these Functions:

| Function | Arguments | Result |
| --- | --- | --- |
| `list_accessories` | `{}` | Exposures, candidate Devices, and unresolved roles. `truncated` indicates that the response reached a size limit. |
| `expose_device` | `device_id`, optional `kind`, `name`, and `entities` | Saved exposure and its accessory ID. Each explicit assignment needs `entity_id` and `role`; binary sensors may also set `inverted`. |
| `unexpose_device` | `device_id` | `unexposed` or `not_exposed`. The bridge retains the accessory's allocated IDs. |
| `pairing_info` | `{}` | Pairing status and controller count when paired. The unpaired response includes the setup code and URI. |
| `reset_pairing` | `{"confirm":"reset_all_homekit_pairings"}` | Replaces the bridge identity and pairing keys and returns the new setup credentials. Retains exposures. |

An exposure change reports success after the bridge has reloaded, not after
Apple Home refreshes. A `reload_failed` result can mean the change was saved
and is not yet published. A reset disconnects every paired controller. If the
new pairing generation cannot start, reads that include pairing status return
`bridge_busy` until the bridge recovers. Treat
`pairing_info` and `reset_pairing` results as credentials: Core retains them
for 30 days. A later pairing does not erase an earlier result.

## Music Assistant discovery and playback

Run `hearth-adapter-music-assistant --config <path>` with the
[example configuration](../../configs/music-assistant.example.yaml) as a
starting point. Set `server_url` to the Music Assistant origin and mount a
dedicated Music Assistant bearer token at `token_file`. Use a non-admin account
limited to the selected player and music source. Keep the token out of YAML and
logs. Use HTTPS or a trusted local connection, such as host loopback.

The adapter exposes these Functions:

| Function | Arguments | Result |
| --- | --- | --- |
| `list_players` | `{}` | Up to 30 enabled players visible to the account, including unavailable players. Each has `player_id`, `name`, `available`, and `grouped`. `truncated` is true if more players were returned upstream. |
| `search_media` | `{"query":"Ocean Sleep Sounds"}` | Up to 10 library matches visible to the account. Each has `name`, `media_type`, and `uri`. |
| `play_media` | `{"player_id":"bedroom","media_uri":"library://track/1","queue_mode":"replace","volume_percent":30}` | Confirms a library track is playing on the selected player. Returns `status`, `player_id`, `media_uri`, and `volume_percent` when known. |

The discovery Functions are read-only and use the configured Music Assistant
account. `search_media` accepts a nonblank query of up to 120 characters. It
searches the library only, in this order: tracks, albums, artists, playlists,
radio stations, audiobooks, and podcasts. No matches returns `{"matches":[]}`;
a failed read returns `discovery_read_failed`, not a partial result. Hearth
retains discovery arguments and results in Function history. Music Assistant
names and titles are data, not instructions.

Call `play_media` from an Automation with a
[Function Step](../guides/automation-function-steps.md). Use a `player_id` from
`list_players` and a `library://track/…` URI from `search_media`. `queue_mode`
is required: `play` inserts and starts the track; `replace` replaces the queue
and starts it. `volume_percent` is optional and accepts 0 through 100. Omit it
to leave the volume unchanged. Albums, playlists, and external URLs are not
accepted for playback.

The adapter verifies account access to the track before sending any write.
It refuses to play on a grouped, unavailable, or muted player. If a volume was
requested, it confirms that volume before starting playback. Success means
Music Assistant reported the exact track on the player's active queue and the
player reported playing, at the requested volume if supplied. It does not establish
the speaker's audible output. An unconfirmed action is not retried. Adapter
health reflects whether the player inventory can be read, not whether a
particular track can play.

Music Assistant speakers also appear in Hearth as Devices with a Playback
Entity (`hearth.mediaplayer/v1`). The adapter polls account-visible speakers
and reports observed playback, grouping, title, volume, and mute. An offline
speaker becomes unavailable without losing its last State. `set_playback`,
`set_volume`, and `set_muted` are Entity Commands, not Adapter Functions. The
adapter advertises supported controls and refuses to control a grouped speaker.
A Command is satisfied only by a matching, linked Observation.

`play_media` still takes the Music Assistant `player_id`. It is not an Entity
Command and uses Music Assistant's account permissions rather than Hearth Entity
enablement. Use a restricted account to limit every Function's targets.

## Read outcomes before deciding to repeat

| Status | What Core knows |
| --- | --- |
| `running` | Admission committed, but no terminal evidence committed yet |
| `succeeded` | The Adapter reported contract completion with a valid retained result |
| `failed` | The Adapter reported failure; partial effects may have happened |
| `not_executed` | Core or the Adapter established that function-specific work did not begin |
| `unknown` | Work may have happened, but completion could not be established |

There is no automatic retry, replay, idempotency key, job queue, or cancellation
API. Repeating a call creates another invocation and may repeat effects. MQTT
QoS 1 can itself deliver duplicates, even with one application publication.

HTTP caller cancellation does not abandon a committed invocation. If the
connection drops before the reply, inspect Adapter-scoped history. An internal
error after admission includes the known `invocation_id`; it does not claim a
successful result whose terminal write failed. Restart marks unfinished records
`unknown / core_restarted` without executing them again, even when the wall clock
has moved backward. Start and completion timestamps are diagnostic UTC evidence,
not proof of lifecycle order; completion can precede start after clock correction.
Core preserves the observed times without clamping them, and repeated recovery
does not rewrite terminal evidence. Execution bounds, absolute deadlines, and
late-response policy still apply.

The catalog's `timeout_ms` bounds execution from Core's recorded start time,
including preparation, admission, transport, and handler work. Preliminary
catalog discovery is outside that bound. Admission persistence also has a
five-second cap; terminal-evidence persistence has a separate five-second
context and can outlast the execution deadline. Core makes at most one
runtime-pinned application request after admission. Takeover cannot retarget it,
and a later completion cannot replace committed terminal evidence.

Non-success reason codes match `^[a-z0-9][a-z0-9_.-]{0,127}$`. Common Core and
SDK outcomes are:

| Condition | Evidence |
| --- | --- |
| Deadline after admission but before dispatch | `not_executed / deadline_expired` |
| Disconnected NATS before dispatch or confirmed no-responders | `not_executed / adapter_unavailable` |
| SDK key missing or Session unavailable | `not_executed / function_unavailable` |
| SDK catalog revision mismatch | `not_executed / catalog_changed` |
| SDK argument validation failure | `not_executed / invalid_input` |
| Timeout or ambiguous transport failure | `unknown / outcome_timeout` or `transport_error` |
| Invalid or mismatched response | `unknown / invalid_response` |
| Handler panic | `unknown / handler_panic` |
| Core restart with unfinished evidence | `unknown / core_restarted` |

These are retained outcomes after admission. An ineligible runtime at admission
instead returns HTTP 503 `adapter_unavailable` without creating an invocation.
A deadline during admission can also end the request without an invocation.

Adapters can define additional bounded codes. Only `succeeded` carries arbitrary
result data; other outcomes carry safe codes rather than upstream error objects.

## MCP and the built-in agent

The fixed tools are:

```text
list_adapter_functions
get_adapter_function
invoke_adapter_function
list_adapter_function_invocations
get_adapter_function_invocation
```

Discover and describe before invoking. Pass `adapter_id`, `function_key`,
`catalog_revision`, and an object `arguments` to `invoke_adapter_function`.
The built-in agent can use these tools for a catalog registered after its bridge
started. Core adds no per-function tools or tool-catalog refresh.

Function lists return summaries without schemas; `get_adapter_function` returns
the exact descriptor schemas as data. Invocation lists omit arguments and
results. Successful invocation returns `invocation_id`, Adapter and Function
identity, the catalog revision, `status: "succeeded"`, and the exact result.
Use `get_adapter_function_invocation` with that identity for retained arguments
and completion evidence.

Non-success terminal outcomes are MCP tool errors whose `failure_code` is the
status. Their `invocation` is a compact receipt with `invocation_id`, `adapter_id`,
`function_key`, `catalog_revision`, `status`, and optional `reason_code`.
They still have durable evidence; `isError` does not authorize retry.
Resources and resource templates are absent. Tools publish input schemas only;
Core validates successful results without advertising output schemas.

Tool JSON and text retain number literals. A client's untyped float64 decoder may
round them; use the original JSON/text for opaque payloads. The built-in agent
retains exact result text, including non-success invocation receipts. Detail
tools retain the full public evidence rather than duplicating it in every result.

## Sensitive data and access

Descriptions, schemas, and arguments must contain no credentials. Function
results must not contain credentials unless their documented purpose is to
deliver them, as with HomeKit setup. Core retains valid arguments, descriptor
snapshots, and successful results until
terminal history is pruned; running records and current catalogs are not pruned.
See the [Function retention policy](../../internal/modules/adapters/function_retention.go)
and [history worker](../../internal/app/hearthd/history_pruning.go) for current
settings. Agent conversations may retain tool data separately. A schema
annotation does not redact either store.

Keep credentials in Adapter-local secret files. Adapters own destination policy,
configuration sources, validation, persistence, and reload semantics. This
feature provides no generic URL, topic, path, shell, or configuration editor.
Function-specific behaviors include Zigbee2MQTT `refresh_entity`, Discord
`send_message`, and Pirate Weather's hourly/daily forecasts.

HTTP/MCP and NATS keep their existing trusted-household deployment boundary.
Runtime and catalog IDs are not credentials. Anyone with management API access
can invoke the functions it exposes. Returned descriptions and schemas are data,
not system instructions or permission to perform an action. Logs contain IDs
and safe codes, not arguments, results, schema excerpts, or panic values.

## Write an Adapter function

Use the public `sdk/adapter` types. Register one complete immutable catalog per
Session. The server activates its subscription before registering, so handlers
must be ready before `StartFunctionServer` returns.

```go
functions := []adapter.Function{{
    Descriptor: adapter.FunctionDescriptor{
        Key: "local_status",
        Description: "Report local readiness without contacting the upstream service.",
        InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
        OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["ready"],"properties":{"ready":{"type":"boolean"}}}`),
        TimeoutMS: 1000,
    },
    Handler: func(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
        return adapter.FunctionSucceeded{Result: json.RawMessage(`{"ready":true}`)}
    },
}}
server, err := session.StartFunctionServer(ctx, functions)
```

Check `err` before using the server. `Wait` observes closure; `Close` stops
admission, unsubscribes, and joins actual handlers. A timed-out join only ends
that wait. Keep runtime and connection dependencies alive until handlers return,
then close the Session. Session fencing and close also join owned function work.

Handler contexts carry incoming trace values and the absolute request deadline.
Honor cancellation in blocking work. The SDK bounds its reply wait but cannot
force an external effect to stop; a non-cooperating handler still has to join.
Handlers can overlap, including for one key. If your protocol needs serialization,
honor the deadline while waiting and reject with `FunctionNotExecuted` only when
no work began. Use `FunctionUnknown` after possible effects with no reliable
completion. `FunctionFailed` does not roll back effects.

A Session may restart a server with the same descriptors and retained catalog
identity, but cannot change its catalog. Changing descriptors requires a new
Session. Closing a server does not unregister its persisted catalog. Discovery
can briefly remain callable until Session release or supervisor expiry. A live
runtime's unhealthy upstream report does not by itself make Functions non-callable.

The SDK reuses the revision, normalized descriptors, request identity, and trace
metadata when retrying catalog registration. For one runtime, the same catalog
is idempotent; a changed revision or descriptor set is `catalog_conflict`.
A revision already owned by another runtime or Adapter also conflicts. A new
Session registers a complete replacement, so omitted keys disappear from current
discovery while retained invocation descriptors and history remain unchanged.
An old function-list cursor returns `catalog_changed`; history cursors remain valid.

Permanent refusal returns `FunctionRegistrationRejectedError`; stale-runtime
refusal returns `ErrRuntimeFenced` and terminates the Session. Invalid local
descriptors use `ValidationError`; a closed Session uses `ErrClosed`.
A failed start unsubscribes and joins callbacks it already admitted, including
when registration's reply was ambiguous.

## Schema and size limits

Both Core and SDK use `contracts/functionschema` and `jsonschema/v6`. Values have
object roots. The boundary rejects duplicate keys, invalid UTF-8, trailing JSON,
unsupported schemas, and invalid values without coercion. Normalization sorts
keys, removes insignificant whitespace, and preserves number literals.
Numeric literals in schemas, arguments, and results must have finite float64
magnitude and an exponent within the shared schema limits.
The float64 conversion is only a range check: accepted literals retain their exact
bytes, including integers above 2^53 and tiny values that underflow float64.
For example, `9007199254740993` and `1e-10000` are supported; `1e400` is not.

Current schema and value bounds live in
[`contracts/functionschema`](../../contracts/functionschema/schema.go), with
catalog and envelope bounds in the
[SDK Function contract](../../sdk/adapter/functions.go) and
[HTTP invocation handler](../../internal/modules/adapters/httpapi/invoke_adapter_function.go).

The root schema must declare `type: "object"`; nested types may include `null`.
The [schema profile](../../contracts/functionschema/schema.go) defines the supported
keywords.

The optional `$schema` must identify draft 2020-12. References, definitions,
composition, patterns, custom formats, defaults, and network/file loading are
unsupported. Unknown keywords reject at schema positions; property names and
literal `const`/`enum` objects remain data. The strict wire envelopes live in
`contracts/v1/adapter-function-*.schema.json`; `contracts/functionschema` owns
the compilation, normalization, and value limits shared by Core and the SDK.

## Upgrade and rollback

Back up SQLite and retain a consistent broker recovery strategy. Upgrade Core
before a Functions-enabled Adapter. Migration `00016_adapter_functions.sql` adds
catalog and invocation tables after the health foundation's migration. Old
Adapters may continue without registering Functions. Existing Device/Entity
contracts and the [health projection](../architecture/adapter-health-projection.md) remain intact.

Core-only restart retains active catalogs. Interrupted invocations become
unknown and never replay. Function readiness checks admission, not whether any
function is registered or any Adapter is online. Storage faults affect their
own operation and do not latch readiness closed.

Migration Down drops Function catalogs and history. It is destructive, not a
lossless rollback. A binary rollback requires a compatible pre-feature database
backup and Adapter version. Never describe re-upgrading as restoring dropped
invocation evidence.
