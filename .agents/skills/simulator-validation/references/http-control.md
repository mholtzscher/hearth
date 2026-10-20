# Direct HTTP control of the running simulator

## API at a glance

All simulated Adapters share `http://127.0.0.1:$SIMULATOR_PORT` (from
`mise env --json`). Snapshots include `adapter_id` for disambiguation:

| Method / path | Body | Response / effect |
| --- | --- | --- |
| `GET /v1/sim/entities` | none | JSON **array** of snapshots; discover `entity_id` by `binding_key` + `key` |
| `POST /v1/sim/entities/{id}/pause` | none | Snapshot with `paused: true`; stops future scheduled output |
| `POST /v1/sim/entities/{id}/resume` | none | Snapshot with `paused: false`; continues at the next script index |
| `POST /v1/sim/entities/{id}/publish` (State) | `{"value":25000}` | Snapshot plus **`observation_id`** for this publication |
| Same path (Entity Event) | `{"name":"double_press"}` | Snapshot plus **`event_id`** for this publication |

Snapshots include `entity_id`, `binding_key`, `key`, `entity_type`,
`event_source`, `paused`, and `current`. They are **not Core State**. An event
snapshot may have `current: "double_press"` while Core correctly has
`state: null`. Publish does not change pause state or sequence position.
For multi-adapter configs, snapshots also include `adapter_id`. Discover by
`adapter_id` + `binding_key` + `key`, since binding keys can repeat between
Adapters. Publish, pause, and resume responses include the owning Adapter ID.

On successful publish, exactly one publication-ID field is present in the
same flat object. It is the canonical ID assigned by the SDK and carried on
the wire—not a control-only request ID. Pause/resume/list responses have no
publication-ID fields. Save the response before polling Core; do not infer
identity from values, timestamps, event counts, or history differences.

Success means the publication was broker-acknowledged, **not Core-accepted**.
Core may record a rejected disposition or not yet have processed the report.
Error responses contain no success ID. A transport failure can be ambiguous;
do not assume nothing was published or blindly retry the POST.

Read Core at `http://127.0.0.1:$SIM_CORE_PORT`:

| GET path | JSON shape / evidence |
| --- | --- |
| `/v1/entities/{id}` | Object; `.state.value`, `.state.observation_id`; event `.state == null` |
| `/v1/entities/{id}/state/history?limit=50` | Object with `.items[]`; `observation_id`, `value`, **`applied`** for a changed State, **`unchanged`** for an equal value |
| `/v1/entities/{id}/events?limit=50` | Object with `.items[]`; `event_id`, `name`, successful disposition **`accepted`** |

**State processing is not always a State change.** A changed State is `applied`;
a repeated equal value is `unchanged`. Both records prove processing when their
`observation_id` matches the returned publication ID. For repeated values, look
up each exact ID in history and expect the appropriate disposition—do not require
both to be `applied`, or infer processing from the current value alone.
Entity Events use `accepted`, never State-history `applied`/`unchanged`.
History endpoints are paginated; `limit=50` is suitable for these small paused
experiments, not an unbounded high-volume run.

## Discover and drive State

Run the examples in one noninteractive Bash script with `curl`, `jq`, and
`set -euo pipefail`. Failed requests or assertions must stop later mutations.
Polling allows at most 30 attempts, each with a two-second HTTP timeout and a
one-second sleep, a budget of about 90 seconds. After failure, inspect saved
evidence before sending further input; do not blindly retry a POST.

```sh
set -euo pipefail
run_dir=.data/simulator-stack
core_url="http://127.0.0.1:$(mise env --json | jq -er .SIM_CORE_PORT)"
sim_url="http://127.0.0.1:$(mise env --json | jq -er .SIMULATOR_PORT)"
curl -fsS --max-time 2 "$sim_url/v1/sim/entities" >"$run_dir/inventory.json"
temperature_id=$(jq -er '.[] | select(.adapter_id == "sim-healthy" and .binding_key == "simulated-light" and .key == "temperature") | .entity_id' "$run_dir/inventory.json")
events_id=$(jq -er '.[] | select(.adapter_id == "sim-healthy" and .binding_key == "simulated-button" and .key == "events") | .entity_id' "$run_dir/inventory.json")

curl -fsS --max-time 2 -X POST "$sim_url/v1/sim/entities/$temperature_id/pause"
curl -fsS --max-time 2 -X POST "$sim_url/v1/sim/entities/$temperature_id/publish" \
  -H 'content-type: application/json' -d '{"value":25000}' \
  >"$run_dir/temperature-publish.json"
observation_id=$(jq -er '.observation_id' "$run_dir/temperature-publish.json")

applied=false
for attempt in $(seq 1 30); do
  curl -fsS --max-time 2 "$core_url/v1/entities/$temperature_id" >"$run_dir/temperature-core.json"
  if jq -e --arg id "$observation_id" \
    '.state.observation_id == $id and .state.value == 25000' \
    "$run_dir/temperature-core.json" >/dev/null; then applied=true; break; fi
  sleep 1
done
[ "$applied" = true ] || { echo 'Published Observation not applied before deadline' >&2; exit 1; }
```

Temperature uses **milli-Celsius**: 25°C = `25000`. To test holding while
paused, save the value/Observation, wait the requested duration, and read again;
a single sample cannot prove a held State. Then `POST .../resume` and poll for a
subsequent scripted value/new Observation. Use a deadline and HTTP timeouts.
Resume does not restart the list at index zero. If later input supersedes your
Observation, use State history to find its exact ID and disposition instead
of expecting it to remain current.

## Inject an event and match its exact ID

Pause the source to keep the experiment small; exact-ID matching no longer
requires waiting for event history to settle or comparing before/after lists:

```sh
curl -fsS --max-time 2 -X POST "$sim_url/v1/sim/entities/$events_id/pause"
curl -fsS --max-time 2 -X POST "$sim_url/v1/sim/entities/$events_id/publish" \
  -H 'content-type: application/json' -d '{"name":"double_press"}' \
  >"$run_dir/event-publish.json"
event_id=$(jq -er '.event_id' "$run_dir/event-publish.json")

accepted=false
for attempt in $(seq 1 30); do
  curl -fsS --max-time 2 "$core_url/v1/entities/$events_id/events?limit=50" \
    >"$run_dir/events-after.json"
  if jq -e --arg id "$event_id" \
    '.items[] | select(.event_id == $id and .name == "double_press" and .disposition == "accepted")' \
    "$run_dir/events-after.json" >"$run_dir/new-event.json"; then
    accepted=true
    break
  fi
  sleep 1
done
[ "$accepted" = true ] || { echo 'Published Event not accepted before deadline' >&2; exit 1; }
curl -fsS --max-time 2 "$core_url/v1/entities/$events_id" >"$run_dir/event-entity.json"
jq -e 'has("state") and .state == null' "$run_dir/event-entity.json"
```

A different event with the same name is not a match. If polling fails, inspect
the disposition of this exact `event_id` before attempting another mutation.
Resume sources you paused if keeping the stack running.

For custom Device configuration, Commands, health, and recovery, see
`validation.md`. Use exact evidence paths with the file reader; indexed search
can omit ignored `.data` files.

## Native notification controls

The default `sim-healthy` Adapter registers `accepted`, `rejected`, `not-sent`,
`unknown`, and `held` destinations. Discover Core-issued IDs; keys are not IDs.

| Method / path | Body | Response / effect |
| --- | --- | --- |
| `GET /v1/sim/notifications/destinations` | none | Array with `adapter_id`, `key`, `destination_id`, `behavior` |
| `PUT /v1/sim/notifications/destinations/{id}/behavior` | `{"outcome":"accepted","hold":false}` | Complete replacement; optional `reason_code`; affects future requests only |
| `GET /v1/sim/notifications/requests?adapter_id=sim-healthy&after_sequence=0&limit=1000` | none | Object with `items`, `metadata` array, `has_more` |
| `POST /v1/sim/notifications/requests/{attempt_id}/release` | none | 204; idempotent while captured, 404 after eviction/restart or for unknown ID |

Behavior requires both `outcome` and `hold`; unknown fields, nulls, omitted
required fields, and invalid outcome/reason pairs return 400. Accepted has no
reason. Representative valid pairs are rejected/provider_rejected,
not_sent/adapter_busy, and unknown/provider_error. A held handler snapshots its
behavior before waiting; changing behavior does not release it. Release replies
to that same request, never dispatches or retries a send.

Capture items include exact `attempt_id`, `destination_id`, `local_key`,
`adapter_id`, per-Adapter `sequence`, `received_at`, `deadline`, `behavior`, and
nullable `finished_at`. The SDK message is **`message.Title` / `message.Text`**
(capitalized), unlike Core's `title` / `message` fields. A capture proves handler
receipt, not durable acceptance or external delivery. Finished time does not
encode the actual cancellation outcome; read Core's attempt.

`after_sequence` is exclusive and requires `adapter_id`. Limit defaults to 100,
maximum 1,000. Without an Adapter filter, pages aggregate all Adapters but their
sequence numbers are still independent. For complete receive counts, query one
Adapter and follow pages until `has_more == false`.

Each metadata entry describes that Adapter's whole retention window:
`oldest_sequence`, `latest_sequence`, `highest_evicted_sequence`,
`highest_dropped_sequence`, `dropped_count`, and sticky `truncated`.
Latest includes overflow admissions; dropped includes both evictions and requests
that could not be captured, including SDK receipts before the registration reply
supplies Core's destination ID. These pre-Attach receipts return not_sent/adapter_busy
and advance latest/dropped metadata without an unidentified capture.
Pending entries are not evicted. When all 1,000
entries are pending, new work returns not_sent/adapter_busy without a capture.
Oldest alone cannot expose holes behind a pending entry. Before a scenario, save
latest as its cursor; absence/count assertions require no pagination remaining,
latest not moving backwards, and highest dropped sequence at or below that
cursor. Otherwise evidence is incomplete, not proof of no send. Simulator restart
resets captures and metadata; Core's history persists.

Use Core for authoritative notification evidence:

| GET path | Evidence |
| --- | --- |
| `/v1/notification-destinations/{id}` | Core identity, name, current availability |
| `/v1/notifications?destination_id={id}&limit=200` | Attempt `items`, optional `next_cursor`; follow cursors for counts |
| `/v1/notifications/{attempt_id}` | Exact attempt ID, destination, message, terminal `status` and `reason_code` |
| `/v1/automations/{id}/history` | Run/Skip summaries with exact `cause.fact.event_id`; paginated |
| `/v1/automations/{id}/history/{entry_id}` | Run detail under `run`; `notifications` links attempt IDs, Command `steps` retain verified IDs |

There is no simulator send endpoint. Manual sends go through
`POST /v1/notification-destinations/{id}/notifications` with `title` / `message`.
Automation sends use notification Steps. Match the returned publication Event ID
to the Run cause, its notification attempt ID to Core and the capture, and its
verified Command ID to `/v1/commands/{id}`. The repeatable native scenario is in
`validation.md`.

## Native function controls

The default `sim-healthy` Adapter registers `inspect` (echo arguments),
`fail`, `not-executed`, `unknown`, and `hold` Functions. Core discovers the
catalog; the simulator supplies only behavior.

| Method / path | Body | Response / effect |
| --- | --- | --- |
| `GET /v1/adapters/sim-healthy/functions` | none | Object with an `items` array and optional `next_cursor`. Each item has `adapter_id`, `function_key`, `catalog_revision`, schemas, `timeout_ms`, and `callable` |
| `GET /v1/adapters/sim-healthy/functions/{key}` | none | One descriptor with the current `catalog_revision` |
| `POST /v1/adapters/sim-healthy/functions/{key}/invocations` | `{"catalog_revision":"afc_...","arguments":{...}}` | Terminal invocation; 200 for every modeled outcome |
| `GET /v1/adapters/sim-healthy/function-invocations?function_key=hold` | none | History, including `running` records while a handler is held |
| `GET /v1/sim/functions/invocations` | none | Array of `adapter_id` and `invocation_id` for held handlers |
| `GET /v1/adapter-function-invocations/{id}` | none | Exact retained invocation evidence |
| `POST /v1/sim/functions/invocations/{invocation_id}/release` | none | 204 release; 404 unknown, completed, or post-restart |

The discovery collection returns `{"items":[...],"next_cursor":"..."}`. Pass
the cursor unchanged when it is present. The simulator pending list proves the
handler is admitted; Core can record `running` before dispatch reaches the
adapter.

`inspect` echoes the SDK-normalized argument bytes as its result. `fail`,
`not-executed`, and `unknown` return their configured reason codes. A `hold`
invocation blocks until released or its `timeout_ms` deadline. Release after the
deadline cannot change the retained outcome; Core records
`unknown/outcome_timeout`. Release is idempotent while the handler is running
and 404 once it returns. A control 204 proves release, not Core completion: poll
the exact `afi_...` ID for terminal evidence. A stale `catalog_revision` returns
409 `catalog_changed`; rediscover the function after a simulator restart.
