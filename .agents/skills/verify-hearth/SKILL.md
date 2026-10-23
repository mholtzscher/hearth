---
name: verify-hearth
description: Verify Hearth end to end through its debug dashboard and HTTP API using worktree-local simulated Devices. Use for live feature checks, durable outcome evidence, and recovery verification without physical hardware.
---

# Verify Hearth

Invoke with `@verify-hearth` in OpenCode. Drive the real dashboard and Core;
the simulator replaces Devices, not Core. Read the [feature index](features/README.md)
and the selected recipe before starting. State the feature ID, entry point,
expected result, and deadline. A passing API journey does not prove its browser
entry point, and simulated outcomes do not establish physical-device behavior.

Use the existing [simulator skill](../simulator-validation/SKILL.md) for stack
mechanics. Read its [control reference](../simulator-validation/references/http-control.md)
when injecting input and its [scenario reference](../simulator-validation/references/validation.md)
for custom presets or advanced recovery. This skill owns the feature map, not a
second lifecycle harness. Repository commands below run from the worktree root.

## Launch

Use a dedicated worktree with no running daemons and no
`.data/simulator-stack/storage`. If either exists, use a fresh worktree or ask
for an explicitly approved reuse scenario; preserve existing data and processes.
One run owns the four simulator daemons in that worktree. Independent runs need
different worktrees; different browser sessions alone do not isolate Core.

Prerequisites: Mise 2026.9.12+, repository tools, `curl`, `jq`, and a
non-whitespace `HEARTH_OPENAI_API_KEY` inherited by Core. Use `mise install`
when repository tools are missing.
The credential is required even when no model turn is sent. Check presence only;
keep its value and the full environment out of artifacts. These recipes incur
no model calls. HTTP and simulator control have no authentication: stay on
loopback, and leave the optional physical adapters stopped.

For browser recipes, load the installed agent-browser skill and its CLI guide
with `agent-browser skills get core` before driving. Read
[browser evidence](references/browser-evidence.md) for guide-path recovery and
HAR inspection. Keep any guide-path override process-local; shared tooling
installation or upgrades need separate authorization.

Run in Bash with `set -euo pipefail`. Keep the printed evidence path; subsequent
shell calls must export the same `EVIDENCE` and source its `run.env`.

```sh
set -euo pipefail
export EVIDENCE="$PWD/.data/verification/$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -p "$EVIDENCE"
mise --version > "$EVIDENCE/mise-version.txt"
git rev-parse HEAD > "$EVIDENCE/revision.txt"
git status --short > "$EVIDENCE/worktree.txt"
mise daemons ls --json > "$EVIDENCE/daemons-before.json"
jq -e 'all(.[]; .pid == null)' "$EVIDENCE/daemons-before.json" >/dev/null
test ! -e .data/simulator-stack/storage
case ${HEARTH_OPENAI_API_KEY:-} in
  *[![:space:]]*) ;;
  *) echo 'HEARTH_OPENAI_API_KEY is required' >&2; exit 1 ;;
esac
# Ownership begins here, including a partially failed start.
touch "$EVIDENCE/owns-stack"
mise run simulator-start > "$EVIDENCE/launch.log" 2>&1
mise env --json | jq '{SIM_NATS_PORT, SIM_CORE_PORT, SIMULATOR_PORT, SIM_WEB_PORT}' \
  > "$EVIDENCE/ports.json"
export CORE="http://127.0.0.1:$(jq -er .SIM_CORE_PORT "$EVIDENCE/ports.json")"
export SIM="http://127.0.0.1:$(jq -er .SIMULATOR_PORT "$EVIDENCE/ports.json")"
export WEB="http://127.0.0.1:$(jq -er .SIM_WEB_PORT "$EVIDENCE/ports.json")"
printf 'export CORE=%q SIM=%q WEB=%q\n' "$CORE" "$SIM" "$WEB" > "$EVIDENCE/run.env"
printf '%s\n' "$EVIDENCE"
```

The task installs frontend dependencies when absent and supervises NATS,
Core, simulator, and Vite in order. Core initializes/migrates its own SQLite;
the checked-in scripted preset registers seed Devices, destinations, and
Functions automatically. No login, manual SQL, or automation seeds are needed.
Data lives in `.data/simulator-stack/storage/{hearthd.db,nats/}`. Mise allocates
ports per worktree; the NATS websocket +1 and monitor +4000 offsets can still
collide. Diagnose bind failures rather than killing an unrelated listener.

On a failed start, inspect the saved log, record the failure, and run Cleanup
for this run's partial stack before retrying. Product-code repairs require a
separate scope.

## Doctor

Run before the first drive, after a managed restart, and after surprising
behavior. The checks below require the four expected daemon records in this
worktree, live PIDs, matching assigned ports, and no physical adapter. Inspect
the captured running command lines against the daemon commands
in [mise.toml](../../../mise.toml); these start this checkout via `go run` and
Vite. Record source revision and dirty state, not an invented binary version.
Runtime source or config changes after launch invalidate that build provenance.
Stop and relaunch after such changes; skill-only edits do not change the app.

```sh
source "$EVIDENCE/run.env"
mise daemons ls --json > "$EVIDENCE/daemons-doctor.json"
jq -e --arg root "$PWD" --slurpfile ports "$EVIDENCE/ports.json" '
  {"sim-nats": ($ports[0].SIM_NATS_PORT | tonumber),
   "sim-core": ($ports[0].SIM_CORE_PORT | tonumber),
   "simulator": ($ports[0].SIMULATOR_PORT | tonumber),
   "sim-web": ($ports[0].SIM_WEB_PORT | tonumber)} as $expected
  | [.[] | select(.name as $name | $expected | has($name))] as $daemons
  | (($daemons | map(.name) | sort) == ($expected | keys | sort))
    and all($daemons[]; .root == $root and .source == ($root + "/mise.toml")
      and .status == "running" and .pid != null and .port == $expected[.name])
    and ([.[] | select(.name == "zwave-adapter")] | length == 1)
    and all(.[] | select(.name == "zwave-adapter"); .pid == null)
' "$EVIDENCE/daemons-doctor.json" >/dev/null
jq -er '.[] | select(.name == "sim-nats" or .name == "sim-core" or
  .name == "simulator" or .name == "sim-web") | .pid' \
  "$EVIDENCE/daemons-doctor.json" | while read -r pid; do
    kill -0 "$pid"
    ps -p "$pid" -o pid,ppid,lstart,args
  done > "$EVIDENCE/processes.txt"
curl -fsS --max-time 2 "$CORE/readyz" > "$EVIDENCE/core-ready.json"
curl -fsS --max-time 2 "$WEB/readyz" > "$EVIDENCE/proxy-ready.json"
curl -fsS --max-time 2 "$WEB/nats-monitor/varz" > "$EVIDENCE/nats-varz.json"
curl -fsS --max-time 2 "$SIM/v1/sim/entities" > "$EVIDENCE/inventory.json"
jq -e 'any(.[]; .adapter_id == "sim-healthy" and
  .binding_key == "simulated-light" and .key == "power")' \
  "$EVIDENCE/inventory.json" >/dev/null
curl -fsS --max-time 2 "$CORE/v1/adapters/sim-healthy" > "$EVIDENCE/adapter.json"
test -s .data/simulator-stack/storage/hearthd.db
test -d .data/simulator-stack/storage/nats
```

Core readiness and control inventory are separate gates: a listening broker or
HTTP process alone is insufficient. For browser recipes, use a unique session,
check the proxy setting, and start one HAR capture before the first mutation:

```sh
export AGENT_BROWSER_SESSION="$(agent-browser session id --scope worktree --prefix "verify-hearth-$(basename "$EVIDENCE")")"
printf 'export AGENT_BROWSER_SESSION=%q\n' "$AGENT_BROWSER_SESSION" >> "$EVIDENCE/run.env"
agent-browser open "$WEB/#/devices"
agent-browser wait --text 'Simulated light'
agent-browser snapshot > "$EVIDENCE/browser-doctor.txt"
agent-browser find role button click --name 'Connection settings' --exact
agent-browser wait '#base-url'
agent-browser get value '#base-url' > "$EVIDENCE/browser-base-url.txt"
test -z "$(cat "$EVIDENCE/browser-base-url.txt")"
agent-browser find role button click --name 'Connection settings' --exact
if [ ! -f "$EVIDENCE/owns-browser-har" ]; then
  agent-browser network har start
  touch "$EVIDENCE/owns-browser-har"
fi
```

Require `Hearth Debug`, `readyz: ready`, the owned URL, and seeded inventory.
Fresh sessions have an empty **hearthd base URL**, selecting the same-origin
proxy. A saved remote base URL would invalidate the target; never switch to a
household instance to get a passing result. See managed logs with
`mise daemons logs sim-core -n 100`. Other daemon names are `simulator`,
`sim-nats`, and `sim-web`.

## Drive and evidence

Use [features/README.md](features/README.md) for shared shell setup and feature
selection. Browser actions use real accessible names and fresh snapshot refs;
avoid coordinates and internal React setters. API actions use production `/v1`
routes. Simulator controls supply external stimulus only.

Keep evidence under `$EVIDENCE`, outside runtime storage: action transcript,
request bodies/statuses/responses, exact returned IDs, authoritative follow-up
reads, browser snapshots, and screenshots with app identity. Record the expected
and observed result and each checked entry point in `result.md`. An ambiguous
POST is a failure to confirm, not permission to retry. Inspect retained history
before any new mutation.
Cleanup saves the browser HAR before closing the session. Then follow
[HAR inspection](references/browser-evidence.md#inspect-the-har-after-cleanup)
to extract mutation bodies, statuses, and returned IDs and check submission counts.

## Cleanup

On success or failure, close only the named browser session and stop only the
stack whose launch this run recorded. If launch failed before `run.env` existed,
skip sourcing it and browser closure, but still stop the partial owned stack.

```sh
cleanup_failed=0
if [ -f "$EVIDENCE/run.env" ]; then source "$EVIDENCE/run.env"; fi
if [ -n "${AGENT_BROWSER_SESSION:-}" ]; then
  if [ -f "$EVIDENCE/owns-browser-har" ]; then
    agent-browser network har stop "$EVIDENCE/dashboard.har" \
      > "$EVIDENCE/browser-har-stop.txt" 2>&1 || cleanup_failed=1
  fi
  agent-browser close > "$EVIDENCE/browser-close.txt" 2>&1 || cleanup_failed=1
fi
if [ -f "$EVIDENCE/owns-stack" ]; then
  mise run simulator-stop > "$EVIDENCE/stop.log" 2>&1 || cleanup_failed=1
fi
mise daemons ls --json > "$EVIDENCE/daemons-after.json"
jq -e 'all(.[]; .pid == null)' "$EVIDENCE/daemons-after.json" >/dev/null || cleanup_failed=1
test -s "$EVIDENCE/revision.txt"
test -s "$EVIDENCE/daemons-after.json"
find "$EVIDENCE" -maxdepth 1 -type f -print
test "$cleanup_failed" = 0
```

Attempt stack teardown even if browser closure fails; report either cleanup
error. Confirm the recorded PIDs exited and owned listeners closed. Simulator
pause/behavior overrides disappear when that process stops. Preserve SQLite,
JetStream, and proof artifacts as the lifecycle tasks intend; a subsequent
fresh-baseline run needs a fresh worktree, not deletion of retained evidence.
Remove only run-created disposable scratch files. Report evidence paths and
coverage gaps; one exercised feature proves that path, not the whole map.
