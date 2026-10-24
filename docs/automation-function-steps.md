# Automation Function Steps

Use a `function` Step to invoke an Adapter-defined capability from an Automation.
Unlike a Command Step, it targets an Adapter Function rather than an Entity
Operation. Functions can appear in the top-level sequence or any If/Choose arm.

## Author through HTTP or MCP

1. Discover the Function with `GET /v1/adapters/{adapter_id}/functions/{function_key}`
   or MCP `get_adapter_function`.
2. Read its argument schema and copy `contract_fingerprint` into the Step.
3. Create or replace the Automation through the ordinary HTTP/MCP Automation API.

```json
{
  "id": "refresh-weather",
  "kind": "function",
  "adapter_id": "zigbee2mqtt",
  "function_key": "refresh_entity",
  "contract_fingerprint": "<copy from Function discovery>",
  "arguments": {"entity_id": "<canonical Entity ID>"}
}
```

Arguments are fixed JSON objects. There are no expressions, Trigger substitutions,
or variables holding earlier results. All branches are validated when saving,
including branches that a particular Run will not select. A retained offline
catalog can validate a save; executing requires an active Adapter runtime.

Function Steps share the limit of 32 side-effect leaves with Commands and
Notifications. Tree and total definition-size bounds are unchanged. The browser
displays definitions and history read-only; use HTTP or MCP for authoring.

## Adapter restarts and contract changes

The fingerprint binds the Function key, normalized input/output schemas, and
deadline. It does not bind the Session catalog revision. An ordinary Adapter
restart with the same contract therefore needs no Automation update. Changed
schema/deadline contracts fail without dispatch until you inspect the new
descriptor and explicitly replace the fingerprint with the normal Automation
revision check. A catalog change during admission can fail that Run even when a
later catalog is equivalent; there is no automatic rediscovery/retry.

Object-key order and whitespace do not change the fingerprint. Array order,
numeric spelling, and schema annotations do. The outer description does not.
The fingerprint does not prove unchanged vendor behavior or authenticate an Adapter.
One-off Function invocations still require the current `catalog_revision`.

## Outcomes and history

A reached Function Step durably reserves an invocation identity before calling
the Adapter service. Only retained `succeeded` invocation evidence allows the
next Step. `failed`, `not_executed`, and `unknown` all fail the Run. Known
pre-admission failures, such as a missing Function or changed contract, fail with
no fabricated invocation outcome. Unverifiable progress or persistence failure
interrupts only the affected Run. No automatic retry, fallback, replay, or undo
of preceding effects occurs.

Run detail includes a `functions` array of reached execution evidence. Unselected
or unreached Function leaves have no execution row. An `invocation_id` is a
reserved link, not proof that the Adapter received a request. Use the existing
Function invocation detail API to inspect retained arguments and successful
results. Automation evidence survives independently when that detail is pruned
after the Function retention period. Arguments also remain in the Automation
definition and retained Run snapshots: keep secrets in Adapter-held credentials,
not Function arguments. Diagnostics do not log raw arguments/results.

Shutdown lets a reserved call settle before withdrawing its dependencies, but
does not execute the following Step. Restart interrupts unfinished Runs and never
continues their sequences or invokes their Functions again. `unknown` means an
effect might have occurred; repeating the call can repeat that effect.

Start and completion times are diagnostic UTC timestamps, not lifecycle ordering
guards. A backward clock correction between Run start, Step reservation,
Invocation admission, or completion does not invalidate Function evidence or
prevent startup recovery. Reached positions, reserved identities, and retained
outcomes establish progress; Core never clamps timestamps or rewrites terminal
evidence on repeated recovery. Execution deadlines remain enforced.

## Upgrade and rollback

Migration `00018_automation_run_functions.sql` adds reached execution evidence.
Existing definitions and history are not rewritten; older Runs show `functions: []`.
Update strict API clients to understand the new Step variant. Migration 00018 Down
removes Function Step evidence only. Older binaries cannot interpret definitions
or snapshots containing Function Steps, so binary rollback needs a matching
pre-feature database backup rather than migration Down alone.

Migration `00019_function_diagnostic_timestamps.sql` removes the Function Step
timestamp-order constraint without changing retained rows, indexes, or foreign
keys. Its Down restores that constraint and fails atomically if retained evidence
has completion before start; it never rewrites or drops that evidence. Rolling
back to a binary that requires chronological evidence needs a compatible backup.

See [Adapter Functions](adapter-functions.md) for invocation guarantees and
[the implementation contract](../specs/automation-function-steps.md) for details.
