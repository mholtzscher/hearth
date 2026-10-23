# Hearth verification map

Read [the skill](../SKILL.md) for launch, doctor, evidence, and cleanup. These
recipes cover the real debug dashboard and Core HTTP API against the native
scripted simulator, not physical adapters or a mocked Core.

## Baseline preconditions

- A run-owned, fresh worktree stack passed Doctor; `$EVIDENCE/run.env` names its
  URLs and, for browser work, its unique browser session.
- Default `configs/scripted.simulator.yaml`, with no local daemon overrides.
  Five Adapters isolate healthy, unhealthy, rejecting, timeout, and unavailable
  scenarios. The unhealthy fixture does not make Core itself unready.
- No physical adapters or pre-existing Automations. Missing preset data is a
  setup failure, not a skipped assertion or a reason to substitute hardware.
- Use Bash, `curl`, `jq`, and agent-browser for browser entry points. The shipped
  CLIs start services rather than expose an interactive UI, so no PTY is required.

## Driving conventions

Run shared setup and the selected recipe in one Bash script/shell. In later
tool calls, set `EVIDENCE` to the same absolute directory and source `run.env`
again. Snapshot refs are temporary; take a new snapshot after navigation.

```sh
set -euo pipefail
source "$EVIDENCE/run.env"
# Select a unique preset Entity; canonical IDs belong to this database only.
entity_id() {
  jq -er --arg adapter "$1" --arg binding "$2" --arg key "$3" '
    [.[] | select(.adapter_id == $adapter and .binding_key == $binding and .key == $key)]
    | if length == 1 then .[0].entity_id else error("expected one preset Entity") end
  ' "$EVIDENCE/inventory.json"
}
# Poll reads only: <=30 attempts, 2s/request plus 1s sleep (~90s ceiling).
# A failed final assertion remains a failure; diagnose before further input.
wait_core() {
  local route=$1 output=$2 predicate=$3
  shift 3
  for attempt in $(seq 1 30); do
    if curl -fsS --max-time 2 "$CORE$route" > "$output" &&
        jq -e "$@" "$predicate" "$output" >/dev/null; then return 0; fi
    sleep 1
  done
  echo "Core evidence deadline: $route" >&2
  return 1
}
```

Pause each driven Entity before injecting deterministic input. Save the
publication ID and match it in Core, rather than inferring acceptance from a
value or a simulator snapshot. `applied`/`unchanged` describe State;
`accepted` describes Entity Events. Follow `next_cursor` for a complete history
when a bounded first page does not contain the exact ID. These paused recipes
generate only a few records; pagination is not a no-effects shortcut.

Save each mutation's body and response, issue it once, and record HTTP status.
Use two-second read timeouts and the recipe's longer mutation timeout. A lost
response may hide a completed effect. UI actions need before/after snapshots
and screenshots; a screenshot of the final value alone omits the action.
Browser Doctor starts the HAR capture. Cleanup saves it before closing the
browser; [inspect that HAR](../references/browser-evidence.md#inspect-the-har-after-cleanup)
before reporting a browser journey passed. A positive receipt match alone does
not prove there was only one submission.

## Proof and skip reporting

In `$EVIDENCE/result.md`, record revision/dirty state, feature/sub-feature ID,
entry point, stimulus, expected result, returned IDs, observed authoritative
evidence, artifacts, and cleanup. Distinguish **passed**, **failed**, **blocked**,
and **not run**. For blocked checks, name the attempted route and missing
prerequisite. Do not claim an alternate entry point or the entire map from one
convenient journey.

## Features

| Feature | Initial coverage | Recipe |
| --- | --- | --- |
| Inventory and State | Device → Entity navigation, Entity jump/filter, exact Observation evidence | [inventory-state.md](inventory-state.md) |
| Commands | Dashboard sender, HTTP sender, exact-ID durable audit | [commands.md](commands.md) |
| Entity Events/recovery | Event history, null State, Core-offline backlog | [entity-events.md](entity-events.md) |
| Notifications | Dashboard/API manual send, durable attempt and simulator receipt | [notifications.md](notifications.md) |
| Adapter Functions | HTTP catalog, revision-bound invocation and retained result | [adapter-functions.md](adapter-functions.md) |

Not mapped in this initial set: agent/model turns, Automation authoring,
conditions/branching/delays, Device Facts/NATS viewers, renaming/enablement,
full fault matrices, alternate MCP/SDK entry points, real hardware/provider
compatibility, deployment/container paths, and retention exhaustion. Advanced
simulator scenarios remain in the existing simulator skill. Each mapped recipe
still needs its own live pass before claiming coverage.
