# Scripted scenarios and validation

Run `mise run simulator-start` as described in the parent skill before using
these recipes. Resolve these repository paths from the worktree root. Consult
`specs/simulator-harness.md` for design intent and
`internal/adapters/scripted/{spec,runtime,registry}.go` plus the authoritative
`entitytypes` schemas for current behavior.

## Choose Device definitions

The default `configs/simulator.scripted.example.yaml` covers all built-in
Entity types and separate fault Adapters. For a custom scenario, copy it to an
ignored file and pass that file to `hearth-simulator -config` with worktree
`-nats-url` and `-control-addr` flags. The public start task always uses the
checked-in example and does not generate or modify a config.

Each Device has a unique slug `binding_key`, `name`, `kind` (`light`, `relay`, or
`sensor`), and `entities`. Each Entity has a Device-local unique `key`, `name`,
built-in `type`, and type-correct `support`. Copy support shapes and canonical
units from `configs/simulator.scripted.example.yaml` or the type's schemas.

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

Use `curl` and `jq` for these examples. Set `run_dir=.data/simulator-stack`:

```sh
run_dir=.data/simulator-stack
core_url="http://127.0.0.1:$(mise env --json | jq -r .SIM_CORE_PORT)"
sim_url="http://127.0.0.1:$(mise env --json | jq -r .SIMULATOR_PORT)"
curl -fsS "$sim_url/v1/sim/entities" >"$run_dir/sim-entities.json"
# Default preset identities; adjust keys for your custom Device list.
power_id=$(jq -er '.[] | select(.binding_key == "simulated-light" and .key == "power") | .entity_id' "$run_dir/sim-entities.json")
events_id=$(jq -er '.[] | select(.binding_key == "simulated-button" and .key == "events") | .entity_id' "$run_dir/sim-entities.json")
curl -fsS "$core_url/v1/adapters/sim-healthy"
curl -fsS "$core_url/v1/entities/$power_id"
```

Never invent canonical Entity IDs or reuse them across databases. Pause output,
set a baseline, and wait for Core to record it before the Command:

```sh
curl -fsS -X POST "$sim_url/v1/sim/entities/$power_id/pause"
curl -fsS -X POST "$sim_url/v1/sim/entities/$power_id/publish" \
  -H 'content-type: application/json' -d '{"value":false}' >"$run_dir/power-baseline-publish.json"
baseline_id=$(jq -er '.observation_id' "$run_dir/power-baseline-publish.json")
for attempt in $(seq 1 30); do
  if curl -fsS --max-time 2 "$core_url/v1/entities/$power_id" \
      | jq -e --arg id "$baseline_id" '.state.observation_id == $id and .state.value == false' >/dev/null; then break; fi
  sleep 1
done
curl -fsS "$core_url/v1/entities/$power_id" | jq -e --arg id "$baseline_id" '.state.observation_id == $id and .state.value == false'
```

If the final assertion fails, stop and diagnose rather than sending the Command.
Pause can race an in-flight publish, so confirm the baseline has settled.
Manual publish does not reposition the sequence index; resume continues at its
next scripted value, not after the manually injected value.

```sh
curl -sS --max-time 60 -D "$run_dir/command.headers" \
  -o "$run_dir/command.json" -X POST "$core_url/v1/entities/$power_id/commands" \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}'
curl -fsS "$core_url/v1/entities/$power_id/commands"
curl -fsS "$core_url/v1/entities/$power_id/state/history?limit=50"
curl -fsS "$core_url/v1/entities/$power_id"
```

Inspect response status/body and get the Command ID from the actual response or
history. Read `GET /v1/commands/<command_id>` for its durable terminal result.
Save evidence including expected failure responses. HTTP timeout, adapter
acceptance, simulator snapshots, and changed State alone do not prove the
intended Command outcome. Keep scripts paused for timeout/rejection checks so
unrelated Observations cannot confuse the experiment.

```sh
curl -fsS -X POST "$sim_url/v1/sim/entities/$power_id/resume"
curl -fsS -X POST "$sim_url/v1/sim/entities/$events_id/pause"
curl -fsS -X POST "$sim_url/v1/sim/entities/$events_id/publish" \
  -H 'content-type: application/json' -d '{"name":"single_press"}'
curl -fsS "$core_url/v1/entities/$events_id/events?limit=50"
curl -fsS "$core_url/v1/entities/$events_id"
```

Control success means publication, not Core acceptance. Save the publish
response's `observation_id` or `event_id` and poll with a deadline for that exact
ID and its recorded disposition. See `http-control.md` for executable examples. For automation changes, configure through Core's
current API, inject input, and inspect Run/Skip and resulting Command history;
a published event alone does not prove execution.

If Device Facts matter, the optional NATS client CLI can subscribe before input:

```sh
nats --server nats://127.0.0.1:4222 sub 'hearth.v1.core.fact.>'
```

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
  configuration. Do not start a second Core or purge streams.
- Control unavailable: inspect `mise daemons logs simulator` for config errors or
  `simulator.control_failed`; a control bind failure now stops the simulator
  with `control channel <addr> failed` instead of running without its API.
- Wrong/missing State: inspect units, support, dispositions, aggregate Adapter
  health, and availability. Simulator current values are not authoritative.
- Unexpected timeout: inspect generic parameter-application limits, matching
  fresh evidence, ticker interference, and durable history before raising limits.
- Unchanging sequence: check pause state, interval, and the single-value rule.

After collecting evidence, use `mise run simulator-stop`; retain data for
inspection unless explicitly choosing to discard this run's artifacts.
