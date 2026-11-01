# Reusable Automations

Define a household sequence once and call it manually or from another Automation.
An Invoke Step starts a separate child Run and waits for it to succeed before
the parent continues.

## Create a reusable sequence

Use `POST /v1/automations` or the `create_automation` MCP tool. An explicit empty
Trigger array means the definition has no automatic triggers. The `triggers`
member remains required; omission and `null` are invalid.

Replace this example's Entity ID with one returned by your installation:

```json
{
  "name": "Movie night",
  "enabled": true,
  "triggers": [],
  "steps": [
    {
      "id": "ceiling-off",
      "kind": "command",
      "entity_id": "ent_019a1b2c-3d4e-7000-8000-000000000001",
      "operation": "set",
      "parameters": {"value": false}
    }
  ]
}
```

Run it with the dashboard's **Run now** button,
`POST /v1/automations/{automation_id}/runs`, or `start_automation_run`.
The HTTP operation returns `202 Accepted` after admission; read history for the
outcome. A disabled definition remains explicitly callable. Enablement controls
automatic admission.

## Invoke it from another Automation

Add an Invoke Step using the target's returned Automation ID:

```json
{
  "id": "movie-night",
  "kind": "invoke",
  "automation_id": "aut_019a1b2c-3d4e-7000-8000-000000000002"
}
```

The target must exist when the caller is saved. It can have its own Triggers or
an empty Trigger list. Invoke Steps work inside selected branches as well as at
the top level. Targets are static IDs; an Invoke Step accepts no expressions,
arguments, return values, or Condition bypass.

The child's current definition is selected when invocation reaches admission.
Later edits, disablement, or deletion do not change that Run's snapshot. The
child evaluates its own admission Conditions and initializes its own values.
Parent variables, Step outputs, and Trigger matches are not inherited. See
[expression context](automation-expressions.md).

The next parent Step runs only after both child success and the parent's Invoke
completion have been committed. Earlier effects are not undone if a later Step
fails. Command success retains the Operation's existing outcome semantics; it
does not add physical-device confirmation.

## Failures and interruption

| Situation | Parent Invoke outcome |
| --- | --- |
| Target deleted before admission | Failed: `automation_not_found` |
| Target already has an active Run | Failed: `automation_busy` |
| Child Conditions false or unknown | Failed: `conditions_false` or `conditions_unknown`, with a linked child Skip |
| Child Run fails | Failed: `automation_run_failed`, retaining the child's failure code |
| Child Run is interrupted outside shutdown | Failed: `automation_run_interrupted`, retaining the child's failure code |
| Core stops during an active wait | Interrupted: `core_stopping` |
| Startup finds unfinished execution | Interrupted: `core_restarted` |

Busy invocation never joins an existing Run, queues, or retries. The same rule
breaks recursive calls: A → B → A fails when B tries to admit the already-running
A. Two sequential calls can each start a new child after the preceding child
finishes.

Shutdown joins nested execution before closing its resources. Restart never
resumes the parent, even if the child succeeded just before shutdown. A
persistence fault stops downstream work through the existing Run-scoped
`executor_fault` path.

## Read the history

The parent retains `kind: "invoke"` execution evidence with `child_run_id` on
admission, or `skip_id` when child Conditions block admission. The child Run or
Skip has `cause.kind: "invocation"` and records `parent_automation_id`,
`parent_run_id`, and `parent_step_id`. Invoked Runs have no matched Triggers.

The dashboard links target definitions, child history, and the parent Run.
Related histories have independent retention: an expired child link does not
invalidate the parent's retained outcome. Refresh history explicitly to see
new completion evidence.

## Upgrade and rollback

Upgrade Core and its dashboard together. The migration preserves existing
definitions, Runs, Skips, and execution evidence while adding invocation
variants. Older binaries cannot interpret those variants. Rollback requires
the pre-upgrade database backup and its matching binary; the migration refuses
a lossy Down operation.
