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

Supply literal `arguments` or mutually exclusive `arguments_expr` source that
resolves to an object. The
Adapter, Function key and fingerprint remain static. Saving validates references
in all branches and validates literal arguments; reached expression arguments
are validated before reservation. A retained offline catalog can validate a save;
executing requires an active Adapter runtime. See
[Automation expressions](automation-expressions.md) for values and bounds.

### Save-time failures

Both binding forms translate only missing Adapter/Function, changed declared
contract, or invalid concrete input into `FunctionReferenceError`. HTTP retains
422/`invalid_function_reference` and MCP retains `invalid_automation`, with the
existing safe Step diagnostics. Expression arguments are not evaluated at save
time and require no dummy object to validate a reference.

`ValidateContractReference` and `ValidateContractInput` read retained discovery.
Their concrete producers can reject malformed identity/fingerprint, missing
Adapter/Function, changed fingerprint, corrupt discovery, storage or context
failure; input validation additionally rejects invalid argument objects. Runtime
availability and public/internal admission are not save-time validity checks.
An inactive runtime is returned as `Callable: false`, not an unavailable error.
The existing producer tests verify that offline discovery and closed admission
still permit valid saves.

At the Automation collaborator seam, unexpected runtime availability, catalog
revision or admission errors remain operational, not invalid-definition reasons.
These classes are not currently produced by retained-discovery validation; their
tests are synthetic boundary controls, not evidence of a production outage.
Persistence and context causes outrank joined input classes. Operational failures
retain private Step context and original identities, with safe HTTP
500/`internal_error` or modeled MCP `internal_error`; causes and argument values
never become response diagnostics. Expected request cancellation keeps its
existing diagnostic policy. Runtime invocation classifications are unchanged.

Function Steps share the limit of 32 side-effect leaves with Commands and
Notifications and data Steps. Definitions allow 131,072 encoded bytes. The browser
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

A reached Function Step resolves and validates arguments before reserving an
invocation identity and asking the Adapter service to execute. A rejection at
that first gate records a value failure without an invocation ID. Only retained
`succeeded` invocation evidence with a usable copied output allows the
next Step. `failed`, `not_executed`, and `unknown` all fail the Run. Known
pre-admission failures, such as a missing Function or changed contract, fail with
no fabricated invocation outcome. Unverifiable progress or persistence failure
interrupts only the affected Run. No automatic retry, fallback, replay, or undo
of preceding effects occurs.

Run detail includes Function rows in its single `executions` timeline. Unselected
or unreached Function leaves have no execution row. An `invocation_id` is a
reserved link, not proof that the Adapter received a request. Use the existing
Function invocation detail API for independent invocation evidence. Resolved
arguments and verified copied outputs remain in the Automation execution after
that invocation is pruned. Only a successfully copied output becomes
`steps.<id>.output`. If copying fails, the provider outcome remains succeeded but
the Step and Run fail; the provider is never retried. Arguments also remain in
definitions and retained revisions: keep secrets in Adapter-held credentials,
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

Current definition/history schemas are v1. Migration 23 preserves post-22
revisions and executions, keeps stored literal arguments unchanged and marks
old values `legacy_unavailable`; it never invents historical outputs. See
[upgrade and rollback](automation-expressions.md#unified-history-and-upgrade).

Migration `00019_function_diagnostic_timestamps.sql` removes the Function Step
timestamp-order constraint without changing retained rows, indexes, or foreign
keys. Its Down restores that constraint and fails atomically if retained evidence
has completion before start; it never rewrites or drops that evidence. Rolling
back to a binary that requires chronological evidence needs a compatible backup.

See [Adapter Functions](adapter-functions.md) for invocation guarantees and
[the implementation contract](../specs/automation-function-steps.md) for details.
