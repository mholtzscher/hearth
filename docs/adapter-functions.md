# Adapter functions

Adapter Functions expose Adapter-owned capabilities outside Entity Operations.
Core discovers their schemas, validates values, routes each invocation once, and
retains evidence. Core does not interpret a function's name or vendor fields.
Functions do not replace Commands. Notification sends remain; the Discord
`send_message` function exposes the same webhook send through the Function API
as an alternative invocation path.

## Discover, describe, invoke, inspect

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

Collections default to 50 items and accept limits from 1 through 200. Pass the
returned `next_cursor` unchanged. A function-list cursor belongs to one Adapter
and catalog revision. A history cursor belongs to one Adapter and optional
`function_key` filter. History pages are not snapshots; a removed function's
retained invocations remain readable.

The Discord Adapter registers `send_message`. It sends a one-off message to a
configured destination through the same webhook path as notification sends:
one POST, one embed, mention parsing disabled, no retry. Arguments are
`destination` (a configured local key), `text` (required), and `title`
(optional). A successful result is
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
`unknown / core_restarted` without executing them again. Recovery rejects a clock
earlier than retained running start times rather than writing contradictory
completion evidence. Correct the clock before restarting in that case.

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

Non-success terminal outcomes are MCP tool errors whose `failure_code` is the
status and whose `invocation` contains the complete retained public record.
They still have durable evidence; `isError` does not authorize retry. GET resources
are poll-only:

```text
hearth://adapter/{adapter_id}/functions{?limit,cursor}
hearth://adapter/{adapter_id}/function/{function_key}
hearth://adapter/{adapter_id}/function-invocations{?function_key,limit,cursor}
hearth://adapter-function-invocation/{invocation_id}
```

Tool JSON and text retain number literals. A client's untyped float64 decoder may
round them; use the original JSON/text for opaque payloads. The built-in agent
retains exact result text, including complete non-success invocation evidence.

## Non-secret data and access

Descriptions, schemas, arguments, and results must contain no credentials. Core
retains valid arguments, descriptor snapshots, and successful results for thirty
days after completion. The shared startup/hourly worker prunes terminal records
in batches of 500 after the notifications pass. Running records and current
catalogs are not pruned. Agent conversations may retain tool data separately.
A schema annotation does not redact either store.

Keep credentials in Adapter-local secret files. Adapters own destination policy,
configuration sources, validation, persistence, and reload semantics. This
feature provides no generic URL, topic, path, shell, or configuration editor.
Two function-specific behaviors ship: Zigbee2MQTT `refresh_entity` and Discord
`send_message`.

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

## Schema and size limits

Both Core and SDK use `contracts/functionschema` and `jsonschema/v6`. Values have
object roots, at most 16 container levels, and at most 1024 members or items per
container. The boundary rejects duplicate keys, invalid UTF-8, trailing JSON,
unsupported schemas, and invalid values without coercion. Normalization sorts
keys, removes insignificant whitespace, and preserves number literals.
Numeric literals in schemas, arguments, and results must have finite float64
magnitude and an explicit decimal exponent between -10000 and 10000 inclusive.
The float64 conversion is only a range check: accepted literals retain their exact
bytes, including integers above 2^53 and tiny values that underflow float64.
For example, `9007199254740993` and `1e-10000` are supported; `1e400` is not.

| Value | Limit |
| --- | --- |
| Catalog | 1 through 64 distinct slug keys, each at most 63 characters |
| Description | 1 through 2000 Unicode code points, not blank |
| Deadline | Required timeout of 100 through 60000 milliseconds |
| Normalized schema | 16 KiB, 256 schema nodes, 16 schema levels |
| Registration envelope | 256 KiB |
| Normalized arguments or successful result | 64 KiB each |
| HTTP body or invocation envelope | 128 KiB |

The root schema must declare `type: "object"`. Supported keywords are `$schema`,
`title`, `description`, `type`, `properties`, `required`, `additionalProperties`,
`items`, `minItems`, `maxItems`, `uniqueItems`, `minLength`, `maxLength`, `minimum`,
`maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf`, `enum`, and
`const`. Nested types may include `null`. Schema annotations allow 2000 code
points; `enum`, `required`, and `type` arrays allow 64 entries.

The optional `$schema` must identify draft 2020-12. References, definitions,
composition, patterns, custom formats, defaults, and network/file loading are
unsupported. Unknown keywords reject at schema positions; property names and
literal `const`/`enum` objects remain data. For full wire and validation rules,
read the [implementation contract](../specs/adapter-functions.md).

## Upgrade and rollback

Back up SQLite and retain a consistent broker recovery strategy. Upgrade Core
before a Functions-enabled Adapter. Migration `00016_adapter_functions.sql` adds
catalog and invocation tables after the health foundation's migration. Old
Adapters may continue without registering Functions. Existing Device/Entity
contracts and the [health projection](adapter-health-projection.md) remain intact.

Core-only restart retains active catalogs. Interrupted invocations become
unknown and never replay. Function readiness checks admission, not whether any
function is registered or any Adapter is online. Storage faults affect their
own operation and do not latch readiness closed.

Migration Down drops Function catalogs and history. It is destructive, not a
lossless rollback. A binary rollback requires a compatible pre-feature database
backup and Adapter version. Never describe re-upgrading as restoring dropped
invocation evidence.
