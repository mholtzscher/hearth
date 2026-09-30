---
name: simulator-validation
description: Use when testing or debugging Hearth State, Commands, Entity Events, or recovery with simulated Devices. Drive simulator inputs and verify Core evidence.
---

# Simulator validation

Use Mise Daemons through the repository lifecycle tasks. Requires Mise
2026.9.12+. The stack uses loopback addresses and worktree-assigned ports.
Before validation, state the expected Core-visible result, stimulus, evidence,
and bounded wait budget.
Check the executable on `PATH` with `mise --version`.
If Mise is too old, report the required version and stop.
Upgrade shared tooling only when the user authorizes that change.

## Start

```sh
mise run simulator-start
```

The default uses `configs/scripted.simulator.yaml`. The task checks the required
agent key file before starting daemons. Mise supplies worktree addresses through
CLI flags, creates local storage under `.data/simulator-stack/`, then starts
NATS, Core, simulator, and dashboard in
dependency order. The simulator validates Device schemas on startup and exposes
control only after all Adapters register. Mise Daemons checks control and
dashboard proxy reachability.
`mise daemons start` alone selects this stack but skips the prerequisite check,
storage creation, and frontend dependency installation; use
`mise run simulator-start` instead.
For a custom config, follow the managed override and restoration procedure in
`references/validation.md` before starting. One simulator process runs five
independent Adapters from the default example:
`sim-healthy` covers every built-in Entity type; `sim-unhealthy`, `sim-rejecting`,
`sim-timeout`, and `sim-unavailable` each isolate one fault scenario.

Discover the URLs with `mise env --json`
(`SIM_NATS_PORT`, `SIM_CORE_PORT`, `SIMULATOR_PORT`, `SIM_WEB_PORT`). NATS
monitoring is NATS port + 4000, and WebSocket is NATS port + 1. Do not use
fixed 8080/8181 URLs in this workflow. On failure, inspect `mise daemons logs
simulator`, `mise daemons logs sim-core`, and Core evidence. Keep the stack
available while diagnosing. Stop or restart during validation only when needed
for the scenario or a specific fix.
The WebSocket and monitoring offsets in `configs/nats.simulator.conf` are not
separately reserved by Mise. Mise does not find alternate ports on conflict;
an unmanaged listener on either offset prevents NATS from binding.

## Validate

Read `references/http-control.md` for simulator control APIs and exact
publication-ID matching. For custom Device definitions, Commands, health,
recovery, or output series, read `references/validation.md`. Check Core State,
Command, or Entity Event evidence by ID: a control snapshot or broker
acknowledgment does not prove Core accepted the input. Pause unrelated
tickers for deterministic Command tests and collect fresh evidence within a
deadline. Use the agent-browser skill only for an actual browser check.

## Stop

When validation and diagnosis are complete, stop the stack unless the user
wants it left running:

```sh
mise run simulator-stop
```

This stops only this worktree's four simulator daemons and retains SQLite and
JetStream data for inspection. Removing a worktree does not automatically stop
or prune its daemons; inspect `mise daemons prune --dry-run` before cleanup.
After code changes, prefer `mise run validate`. Report config,
stimulus, expected/observed Core evidence, URLs, and cleanup status.
