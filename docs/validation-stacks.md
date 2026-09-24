# Worktree-local validation stacks

Mise 2026.9.12 or later and Pitchfork 2.25.0 are required. Upgrade the **mise
executable** on `PATH` first (`mise --version`); the `pitchfork` version is
declared in `mise.toml`. These tasks manage the validation stacks through
Pitchfork.

```sh
mise run simulator-start
mise daemons ls --json
mise daemons logs simulator
mise run simulator-stop
```

`configs/simulator.scripted.example.yaml` supplies
five Adapters in one process: `sim-healthy` covers every built-in Entity type,
while `sim-unhealthy`, `sim-rejecting`, `sim-timeout`, and `sim-unavailable`
isolate distinct fault scenarios. To use another scenario, run the simulator
directly with `-config PATH` and the worktree's `-nats-url` and `-control-addr`
flags. The public task deliberately uses the one checked-in default.

The simulator group starts local NATS/JetStream, Core, one multi-Adapter simulator,
and Vite. Frontend dependencies are installed on first use. Each linked worktree gets Mise's
deterministic ports, its own `.data/validation-stack/storage` (SQLite and
JetStream), and its own daemon IDs. Run `mise env --json` to find assigned
ports (`SIM_NATS_PORT`, `SIM_CORE_PORT`, `SIMULATOR_PORT`, `SIM_WEB_PORT`).
NATS WebSocket is at NATS port + 1 and HTTP monitoring at NATS port + 4000.
They are configured in `configs/nats.validation.conf`, not separately reserved by
Mise; an unmanaged listener on either port will make NATS fail to bind.
Mise does not search for free ports on conflict. The dashboard uses the
worktree-local Core and monitoring endpoints. All listeners bind loopback.

The simulator exposes control only after all its Adapters have registered and
initialized; Pitchfork's `ready_cmd` checks that control API. The dashboard
readiness check covers its API and NATS monitoring proxies. Use simulator
control and Core history for scenario-specific assertions. Perform direct HTTP
checks against the resolved worktree ports.

For real devices, first review the real-device validation safety rules and
obtain an approved homelab hostname. Starting a stack does not authorize
commands to physical hardware.

```sh
mise run real-device-start -- HOMELAB
mise daemons logs real-adapter
mise run real-device-stop
```

This starts **only local** Core, Zigbee2MQTT adapter, and dashboard daemons;
it never starts a local broker or changes the shared homelab. It checks for a
remote Core on `:8081`, confirms broker and Z2M reachability, and requires a
fresh healthy adapter runtime rather than accepting stale SQLite health.
Its dashboard is loopback-only. It does **not** publish a Tailscale Serve
route: the fixed `:8088` route cannot safely represent several worktree
dashboards. Tailnet publication is not supported by this workflow. Do not run two real-device adapters
against the shared physical system at the same time; distinct local ports do
not isolate MQTT topics or NATS messages.

`mise daemons start` without a group selects the simulator stack but does not
create its storage directory or install frontend dependencies. Prefer
`mise run simulator-start`; it prepares those prerequisites, then delegates
startup to Pitchfork. `simulator-stop` stops the four worktree daemons directly;
no simulator launcher record or generated config is needed. Real-device startup
still builds the Go helper and records its host and prior
adapter runtime ID in `.data/validation-stack.json`, which Pitchfork cannot
derive. On startup failure, inspect daemon logs and use the matching stop
task. Stopping preserves SQLite and JetStream evidence.
Removing a worktree does not
automatically stop or prune its daemon state; review `mise daemons prune
--dry-run` before cleanup.

Ignored artifacts from the initial pilot are not used by these tasks and
remain intact for inspection; renaming the paths does not migrate or delete
that data.
