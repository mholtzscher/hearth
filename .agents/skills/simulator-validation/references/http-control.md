# Direct HTTP control of the running simulator

Use `mise run simulator-start` for setup and `mise run simulator-stop` for
cleanup. Between them, make direct HTTP requests for your experiment. You do
not need `simulator-smoke` or the Go implementation to discover this API.

## API at a glance

All control endpoints are at `http://127.0.0.1:8181`:

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

Read Core at `http://127.0.0.1:8080`:

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

Bash examples (`curl` and `jq`), with `run_dir` set to the printed run directory:

```sh
core_url=http://127.0.0.1:8080
sim_url=http://127.0.0.1:8181
curl -fsS --max-time 2 "$sim_url/v1/sim/entities" >"$run_dir/inventory.json"
temperature_id=$(jq -er '.[] | select(.binding_key == "simulated-light" and .key == "temperature") | .entity_id' "$run_dir/inventory.json")
events_id=$(jq -er '.[] | select(.binding_key == "simulated-button" and .key == "events") | .entity_id' "$run_dir/inventory.json")

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
