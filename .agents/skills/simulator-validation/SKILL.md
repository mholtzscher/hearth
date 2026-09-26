# Simulator validation

Use the Mise/Pitchfork lifecycle tasks, not manual background
processes. Requires Mise 2026.9.12+ and Pitchfork 2.25+. The stack is
loopback-only, uses worktree-assigned ports, and never touches the homelab,
Mosquitto, Docker, or physical devices. Before validation, state the expected
Core-visible result, stimulus, evidence, and bounded wait budget.
If Mise is too old, upgrade the executable on `PATH` (`mise --version`);
`mise.toml` supplies the Pitchfork version.

## Start

```sh
mise run simulator-start
```

The default uses `configs/simulator.scripted.example.yaml`. Mise supplies
worktree addresses through CLI flags, creates local storage under
`.data/simulator-stack/`, then starts NATS, Core, simulator, and dashboard in
dependency order. The simulator validates Device schemas on startup and exposes
control only after all Adapters register. Pitchfork checks control and dashboard
proxy reachability. There is no generated simulator config or Go launcher.
`mise daemons start` alone selects this stack but skips storage creation and
frontend dependency installation; use `mise run simulator-start` instead.
One simulator process runs five independent Adapters from the default example:
`sim-healthy` covers every built-in Entity type; `sim-unhealthy`, `sim-rejecting`,
`sim-timeout`, and `sim-unavailable` each isolate one fault scenario.

Discover the URLs with `mise env --json`
(`SIM_NATS_PORT`, `SIM_CORE_PORT`, `SIMULATOR_PORT`, `SIM_WEB_PORT`). NATS
monitoring is NATS port + 4000, and WebSocket is NATS port + 1. Do not use
fixed 8080/8181 URLs in this workflow. On failure, use `mise daemons logs
simulator` or `mise daemons logs sim-core`, then `mise run simulator-stop`.
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

There is no packaged simulator smoke task at present. Do not assume fixed ports.

## Stop

```sh
mise run simulator-stop
```

This stops only this worktree's four simulator daemons and retains SQLite and
JetStream data for inspection. Removing a worktree does not automatically stop
or prune its daemons; inspect `mise daemons prune --dry-run` before cleanup.
After code changes, prefer `mise run validate`. Report config,
stimulus, expected/observed Core evidence, URLs, and cleanup status.
