# Scripted scenarios and validation

Resolve repository paths from the worktree root. For implementation details,
consult `internal/adapters/scripted/{spec,runtime,registry}.go` and the
authoritative `entitytypes` schemas.

## Choose Device definitions

The default `configs/scripted.simulator.yaml` covers all built-in Entity types
and separate fault Adapters. For a custom scenario:

1. Run `mise run simulator-stop` before replacing the managed configuration.
   This releases the existing simulator's ports and Adapter sessions while
   retaining SQLite and JetStream data.
2. Create `.data/simulator-stack/` if needed. Copy the default to an unused ignored path, for example
   `.data/simulator-stack/custom.simulator.yaml`, and edit the copy.
3. In the worktree root's `mise.local.toml`, set the following daemon table.
   Record any existing `daemons.simulator` table first so you can restore it;
   preserve all unrelated local settings. This is a local override, not a file
   to commit. Copy the full table because the override must retain port
   allocation, dependency, and readiness settings.

   ```toml
   [daemons.simulator]
   run = "exec go run ./cmd/hearth-simulator -config .data/simulator-stack/custom.simulator.yaml -nats-url nats://127.0.0.1:$SIM_NATS_PORT -control-addr 127.0.0.1:$SIMULATOR_PORT"
   port = { auto = true, base = 8181, stride = 10 }
   proxy = false
   depends = ["sim-core"]
   ready_cmd = "curl -fsS http://127.0.0.1:$PORT/v1/sim/entities >/dev/null"
   ```

4. Run `mise run simulator-start`, rediscover addresses with `mise env --json`,
   and check the control inventory against your scenario. Do not launch a
   second simulator manually.
5. Once the experiment and any failure diagnosis are complete, run
   `mise run simulator-stop` before restoring the original local table. If you
   created `mise.local.toml` solely for this override, remove it. Retain the
   scenario and evidence for inspection. To return to the default stack, run
   `mise run simulator-start` after removing your override or restoring the
   prior local settings.

Subsequent custom-config edits also require stopping and starting the managed stack.

Each Device has a unique slug `binding_key`, `name`, `kind` (`light`, `relay`, or
`sensor`), and `entities`. Each Entity has a Device-local unique `key`, `name`,
built-in `type`, and type-correct `support`. Copy support shapes and canonical
units from `configs/scripted.simulator.yaml` or the type's schemas.

The simulator validates the YAML against Entity-type schemas before connecting
to NATS; a bad Device or Entity fails startup with its actual validation error.

### State sequences

Inside a Device's `entities` list:

```yaml
- key: temperature
  name: Temperature
  type: hearth.temperature/v1
  support: {state: {unit: mCel}, operations: {}}
  initial: 21500
  outputs: {interval: 2s, values: [21500, 21600, 21400]}
```

- State Entities require a valid `initial`, even with an output sequence.
- `interval` must be a positive duration string such as `2s` whenever more
  than one output value needs a ticker. Empty or single-value `values` publish
  once and need no interval.
- With values, `values[0]` publishes at startup, then multi-value sequences
  advance and loop every positive `interval`.
- Without outputs (or with no values), initial publishes once and stays silent.
- Single-value sequences also publish only once; use `[21500, 21500]` for
  repeated identical Observations.
- Clock-skew experiments: `source_time_offset: -24h` backdates `SourceUpdatedAt`
  and `received_time_offset: +2m` shifts `AdapterReceivedAt` on every
  Observation for that Entity. Unset leaves `SourceUpdatedAt` empty and
  `AdapterReceivedAt` at now.
- Temperature uses integer milli-Celsius, not degrees Celsius. Other types have
  their own support, units, ranges, and scalar/object shapes.
- Test threshold crossings, repeated equal values, and boundaries separately.
  Config/control values are schema-validated; this is not malformed-wire
  injection. Core still enforces support-dependent semantic constraints.

### Command behaviors

On a controllable Entity whose support advertises `set`:

```yaml
commands:
  set: {behavior: accept-and-publish, apply_parameters: true}
```

| Behavior | Expected experiment |
| --- | --- |
| `accept-and-publish` (default) | Accept and publish a Command-linked refresh; observed Commands become `satisfied` only with matching evidence |
| `accept-no-publish` | Accept without outcome Observation; observed Commands finish `outcome_timeout` |
| `reject`, optional `reason: simulated rejection` | Command finishes `rejected` with durable failure history |
| `accept-and-publish`, `apply_parameters: false` | Republish unchanged State; test no-op/mismatched evidence |
| `accept-and-publish`, `mark_available: true` | Re-report the Entity available before applying; start it `available: false`, dispatch, expect available State and a `satisfied` Command (rejected with the `reject` behavior) |

Generic parameter application handles only `{"value": X}`: replace scalar State
or an existing object's `value` member. It does not change `active`/`mode` or
apply XY/HS coordinates. Preconfigure or control-publish full State for these
types and test refresh against it; unchanged simulator output is not necessarily
a Core regression.

For stateless `hearth.enumaction/v1`, use `initial: {}` and supported `trigger`
names. Expect `dispatched`, not `satisfied`, and Core State `null`.
`accept-no-publish` does not imply timeout for acceptance-completed operations.
Synthetic dispatch makes no claim about physical effects. For stateless actions,
prefer `accept-no-publish`; the default `accept-and-publish` additionally emits
an Observation that Core rejects as `invalid_value`. Inspect that with
`state/history?disposition=rejected`; it does not invalidate trigger dispatch.

### Health and availability

Device health defaults to `healthy`. Use
`health: "unhealthy:hearth.external_system_unavailable"` for Adapter-wide
rejection-before-dispatch. One unhealthy Device makes the whole Adapter
unhealthy; isolate it from happy-path Commands.

Entity availability defaults to available. Use `available: false` with a valid
`availability_reason` to test advisory unavailability separately. An
unavailable Entity publishes no initial State; repair it with a
`mark_available` Command and expect State plus a `satisfied` Command.
State does not imply availability. For an unhealthy Device whose Entities
should read effectively unavailable in Core, add
`omit_availability_when_unhealthy: true` so no Entity availability report is
sent (requires unhealthy health; otherwise the report still claims available). The control API cannot change health or availability;
change a copied scenario config and restart the simulator for a new
report. Reason codes must use the `hearth.` or `adapter.` namespace, for example
`adapter.simulated_unavailable`. Preserve identities and data for recovery experiments.

### Entity Events

```yaml
- key: events
  name: Events
  type: hearth.enumevent/v1
  support: {state: {}, operations: {}, events: {names: [single_press, double_press]}}
  outputs: {interval: 2s, values: [single_press, double_press]}
```

Event sources take no initial value. Omit outputs for manual-only events.
Names must be advertised in support. Event-source Core State stays `null`;
verify Entity Event history rather than State history.

## Drive input and assert Core evidence

Run these examples in one noninteractive Bash script with `set -euo pipefail`
so a failed request or assertion stops later mutations. Each ordinary HTTP
request has a two-second timeout; polling allows at most 30 attempts with a
one-second sleep, a budget of about 90 seconds. The synchronous Command request
has a separate 60-second timeout. After a failure, inspect saved evidence before
sending further input. A timed-out POST may have taken effect; do not
blindly retry it.

```sh
set -euo pipefail
run_dir=.data/simulator-stack
core_url="http://127.0.0.1:$(mise env --json | jq -er .SIM_CORE_PORT)"
sim_url="http://127.0.0.1:$(mise env --json | jq -er .SIMULATOR_PORT)"
curl -fsS --max-time 2 "$sim_url/v1/sim/entities" >"$run_dir/sim-entities.json"
# Default preset identities; adjust keys for your custom Device list.
power_id=$(jq -er '.[] | select(.adapter_id == "sim-healthy" and .binding_key == "simulated-light" and .key == "power") | .entity_id' "$run_dir/sim-entities.json")
curl -fsS --max-time 2 "$core_url/v1/adapters/sim-healthy"
curl -fsS --max-time 2 "$core_url/v1/entities/$power_id"
```

Never invent canonical Entity IDs or reuse them across databases. Pause output,
set a baseline, and wait for Core to record it before the Command:

```sh
curl -fsS --max-time 2 -X POST "$sim_url/v1/sim/entities/$power_id/pause"
curl -fsS --max-time 2 -X POST "$sim_url/v1/sim/entities/$power_id/publish" \
  -H 'content-type: application/json' -d '{"value":false}' >"$run_dir/power-baseline-publish.json"
baseline_id=$(jq -er '.observation_id' "$run_dir/power-baseline-publish.json")
for attempt in $(seq 1 30); do
  if curl -fsS --max-time 2 "$core_url/v1/entities/$power_id" \
      | jq -e --arg id "$baseline_id" '.state.observation_id == $id and .state.value == false' >/dev/null; then break; fi
  sleep 1
done
curl -fsS --max-time 2 "$core_url/v1/entities/$power_id" | jq -e --arg id "$baseline_id" '.state.observation_id == $id and .state.value == false'
```

If the final assertion fails, stop and diagnose rather than sending the Command.
Pause can race an in-flight publish, so confirm the baseline has settled.
Manual publish does not reposition the sequence index; resume continues at its
next scripted value, not after the manually injected value.

```sh
command_status=$(curl -sS --max-time 60 -w '%{http_code}' -D "$run_dir/command.headers" \
  -o "$run_dir/command.json" -X POST "$core_url/v1/entities/$power_id/commands" \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}')
[ "$command_status" = 200 ] || { cat "$run_dir/command.json" >&2; exit 1; }
command_id=$(jq -er '.command_id' "$run_dir/command.json")
curl -fsS --max-time 2 "$core_url/v1/commands/$command_id" >"$run_dir/command-result.json"
jq -e --arg id "$command_id" '.id == $id and .status == "satisfied"' "$run_dir/command-result.json"
curl -fsS --max-time 2 "$core_url/v1/entities/$power_id/commands"
curl -fsS --max-time 2 "$core_url/v1/entities/$power_id/state/history?limit=50"
curl -fsS --max-time 2 "$core_url/v1/entities/$power_id"
```

The Command POST deliberately omits `--fail` so HTTP error responses are saved.
This healthy-Adapter example requires HTTP 200 and a durable `satisfied` result.
For rejection or timeout scenarios, change those assertions to the expected
HTTP status and Command outcome before running. Inspect the saved
response status/body and get the Command ID from the actual response or
history. Read `GET /v1/commands/<command_id>` for its durable terminal result.
Save evidence including expected failure responses. HTTP timeout, adapter
acceptance, simulator snapshots, and changed State alone do not prove the
intended Command outcome. Keep scripts paused for timeout/rejection checks so
unrelated Observations cannot confuse the experiment.

```sh
curl -fsS --max-time 2 -X POST "$sim_url/v1/sim/entities/$power_id/resume"
```

For Entity Event injection, use the exact-ID publication and polling example in
`http-control.md`. Control success means publication, not Core acceptance.
Save the publish response's `observation_id` or `event_id` and poll with a
deadline for that exact ID and its recorded disposition. For automation changes,
configure through Core's current API, inject input, and inspect Run/Skip and resulting Command history;
a published event alone does not prove execution.

If Device Facts matter, the optional NATS client CLI can subscribe before input:

```sh
nats_port=$(mise env --json | jq -er .SIM_NATS_PORT)
timeout 30s nats --server "nats://127.0.0.1:$nats_port" sub 'hearth.v1.core.fact.>'
```

Run this capture in a separate terminal before driving input. `timeout` exits
with status 124 when the 30-second capture window expires; that is expected.
Plain subscriptions are live-only. Facts are at-least-once: deduplicate by fact
`id`. Command lifecycle is not a fact family. Durable HTTP history remains the
authority; README documents JetStream catch-up.

## Core-offline recovery

Use `mise daemons stop sim-core` and `mise daemons start sim-core` for an
in-place Core outage. Do not stop NATS or start a fresh environment.

1. Register while Core is online. Save current event IDs/history and pause
   unrelated output.
2. Stop only Core with `mise daemons stop sim-core`. Keep the simulator and
   NATS/JetStream alive.
3. Publish a bounded number of events through the simulator control API. Count
   only broker-acknowledged reports as expected durable input.
4. Restart Core with `mise daemons start sim-core` using the same
   SQLite/config paths. Wait for readiness, then separately poll for backlog;
   readiness does not wait for backlog completion.
5. Check new reports are recorded once by event ID and event State is null.

This does not prove recovery after simulator restart during outage, NATS process
loss, unacknowledged publication, or stream retention exhaustion. Heartbeat
expiry, takeover, stale-runtime isolation, and malformed/duplicate wire input
belong to deterministic process/transport tests run through repository mise
tasks, not invented YAML options.

## Diagnose

Inspect logs with `mise daemons logs sim-core`, `mise daemons logs simulator`,
`mise daemons logs sim-nats`, or `mise daemons logs sim-web`.

- Startup refusal: inspect occupied ports and saved ownership; never kill other
  sessions or purge a record to force startup.
- Core not ready: inspect `mise daemons logs sim-core`, NATS reachability, migrations, and stream
  configuration. An `agent.api_key_file could not be read` or
  `agent.api_key_file is empty` failure requires the readable, nonempty key file
  configured by `mise.toml`; check its presence without printing the secret.
  Do not start a second Core or purge streams.
- Control unavailable: inspect `mise daemons logs simulator` for config errors or
  `simulator.control_failed`; a control bind failure now stops the simulator
  with `control channel <addr> failed` instead of running without its API.
- Wrong/missing State: inspect units, support, dispositions, aggregate Adapter
  health, and availability. Simulator current values are not authoritative.
- Unexpected timeout: inspect generic parameter-application limits, matching
  fresh evidence, ticker interference, and durable history before raising limits.
- Unchanging sequence: check pause state, interval, and the single-value rule.
