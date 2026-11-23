# Scripted simulator

`hearth-simulator` runs YAML-declared Adapters through the production SDK.
Core owns canonical identities, validation, State, and execution history.
The simulator supplies inputs and configured outcomes, not physical-device
behavior or proof of external delivery.

## Configuration

The [minimal example](../../configs/simulator.example.yaml) declares one power
Entity. The [validation inventory](../../configs/scripted.simulator.yaml) includes
multiple Adapters, Entity types, fault cases, notifications, and Functions.
Use `mise run simulator-start` and `mise run simulator-stop` for the managed
worktree-local stack.

The top-level fields are `nats_url`, optional `control_addr`, and `adapters`.
`control_addr` requires a loopback host and an explicit port; omitting it disables
the unauthenticated control server. Managed startup supplies worktree-local
addresses.

Each Adapter needs a unique `adapter_id` and at least one of `devices`,
`notifications`, or `functions`. Device binding keys can repeat across Adapters.
Each Adapter has an independent SDK Session and health state.

### Devices and Entities

Each Device declares `binding_key`, `name`, `kind`, and `entities`.
Supported kinds and the current Entity catalog are validated by
[`DeviceSpec.Validate`](../../internal/adapters/scripted/spec.go) and the
[type registry](../../internal/adapters/scripted/registry.go).

Each Entity declares a unique local `key`, `name`, built-in `type`, and `support`.
State and support schemas validate configured values before connecting to NATS;
Core still enforces the complete Entity contract.

| Field | Behavior |
| --- | --- |
| `initial` | Required schema-valid value for every State Entity, even with an output script; forbidden for event sources |
| `outputs.values` | The first value replaces `initial` at startup; a multi-value list then loops |
| `outputs.interval` | A positive duration such as `5s`, required for multi-value lists; one value publishes once |
| `available`, `availability_reason` | Defaults to available; false requires a valid reason code and suppresses startup publication |
| `source_time_offset`, `received_time_offset` | Shift Observation source/receive timestamps; negative durations simulate old clocks |
| `commands` | Per-operation response behavior; omitted operations default to accept-and-publish |

State Entities publish Observations; event sources publish configured event names
from their support. Without output values, a State Entity publishes `initial`
once; an event source stays silent. Multi-value scripts can publish on later
ticks even when `available: false` suppressed startup. Event-source snapshots
are not canonical State.

Device `health` defaults to `healthy`, or accepts
`unhealthy:hearth.external_system_unavailable` with a valid health reason.
The first unhealthy Device makes its Adapter unhealthy without changing other
Adapters. `omit_availability_when_unhealthy: true` skips that Device's startup
availability reports and requires unhealthy health.

### Commands

Each operation under `commands` accepts:

- `behavior: accept-and-publish` (default): accept and publish a linked
  Observation for a State Entity.
- `behavior: accept-no-publish`: accept without an outcome Observation.
  Observed-outcome Commands reach their deadline without satisfaction.
- `behavior: reject`: reject with optional `reason`.
- `apply_parameters`: defaults to true. Parameters shaped `{"value": X}` replace
  scalar State or an object's existing `value` member if the candidate validates.
  For `hearth.thermostat/v1`, supported setters instead update their corresponding
  State fields: `set_target` (`temperature`) sets `target_temperature`;
  `set_range` (`low`, `high`) sets `target_temperature_low` and
  `target_temperature_high`; `set_mode` (`mode`), `set_fan_mode` (`fan_mode`),
  and `set_humidity` (`humidity`) set `mode`, `fan_mode`, and `target_humidity`.
  Temperatures are integer milli-Celsius. Other parameter shapes leave State
  unchanged. This is not a device physics model.
- `mark_available: true`: report availability before an accepted Command.
  It is invalid with reject behavior.

Pausing an Entity stops script advancement, not Commands or explicit publishes.
Resume continues from the script position rather than restarting the sequence.

### Notifications

Each entry under an Adapter's `notifications` declares `key`, `name`, optional
`outcome`, `reason_code`, and `hold`. Keys are unique per Adapter. Outcome defaults
to `accepted`; the other outcomes are `rejected`, `not_sent`, and `unknown`.
Outcome/reason combinations use the [notification evidence rules](notifications.md).

A held request snapshots behavior at admission. Later behavior updates affect
future requests only; release replies to the same request and never resends.
Captures are bounded in memory per Adapter and reset on restart. The
[notification capture implementation](../../internal/adapters/scripted/notifications.go)
owns the current cap.
Finished entries are evicted first. If all entries are pending, new work returns
`not_sent/adapter_busy` without a capture.

Pagination and truncation metadata matter when asserting absence or receive
counts. Follow the [capture verification procedure](../../.agents/skills/simulator-validation/references/http-control.md#native-notification-controls);
Core's retained Attempt is the outcome authority.

### Functions

Each entry under `functions` declares:

- A unique `key`, a `description`, and required
  `input_schema` and `output_schema` using the [bounded Function schema profile](adapter-functions.md).
- `timeout_ms`: the handler deadline.
- `outcome`: defaults to `succeeded`; alternatives are `failed`, `not_executed`,
  and `unknown`, each requiring a `reason_code`.
- For success, either `result` (defaults to `{}` and must match the output
  schema) or `echo_arguments: true`. Echoed arguments must also match the output
  schema; otherwise the SDK returns `unknown/invalid_response`.
  Non-success outcomes permit neither.
- `hold: true`: wait for release, handler cancellation, or simulator shutdown.

The [Function specification](../../internal/adapters/scripted/functions.go) and
[shared Function contract](adapter-functions.md#schema-and-size-limits) own
current bounds and validation. Each Adapter registers an immutable Session catalog.
Independent held invocations can overlap. Release allows a held handler to
return; the SDK still validates its result and deadline. Cancellation at the
deadline yields
`unknown/outcome_timeout`, and shutdown yields `unknown/adapter_stopping`.
Configured failures do not simulate transport faults.

## Loopback controls

The [HTTP control guide](../../.agents/skills/simulator-validation/references/http-control.md)
lists discovery, publish, pause, notification, and Function controls with
payloads and pagination rules.

Successful publish returns exactly one `observation_id` or `event_id`, assigned
by the SDK. Success proves broker acknowledgment, not Core acceptance. Save the
ID and find that exact publication and disposition through Core. Ambiguous
transport failures must not trigger blind mutation retries.

Function release returns 204 while running and 404 for unknown or completed
invocations, including after restart. A 204 proves release, not terminal success;
read Core's exact invocation ID. Notification release is idempotent while the
request remains captured. Manual notification sends go through Core, not the
control server.

The [HTTP control guide](../../.agents/skills/simulator-validation/references/http-control.md)
contains payloads, pagination rules, and exact-ID verification examples.
