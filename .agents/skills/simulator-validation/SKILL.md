---
name: simulator-validation
description: >-
  Validate Hearth changes against scripted simulated Devices using an automated
  local NATS/JetStream, hearthd, and optional dashboard stack in Herdr, without
  hardware. Use whenever the user mentions the simulator, simulated or scripted
  Devices, local-stack or local-loopback validation, configuring output series
  on an interval, exercising Commands or Entity Events without hardware, or
  testing recovery with synthetic evidence. Covers scenario setup, startup,
  assertions, diagnostics, and cleanup. Do not use for pure unit tests or real
  Zigbee hardware, homelab/shared brokers, or live Zigbee2MQTT; use
  real-device-validation for those.
---

# Simulator validation

Use the lifecycle tasks below. They perform setup; do not reconstruct it with
manual config generation, shell background jobs, or separate Herdr calls.

The primary workflow is **start → drive the simulator HTTP API → inspect Core
evidence → stop**. Scripts handle setup; your experiment determines the inputs
and assertions. Keep planning brief.

## Safety and prerequisites

- Run from this worktree inside Herdr (`HERDR_ENV=1`). Otherwise stop and ask
  the user to run validation inside Herdr; do not control a focused session
  from outside it.
- Use the mise toolchain (`mise install` if needed). Startup uses Python's
  standard library, Bash, Herdr, mise-managed Go, and `nats-server`. Docker,
  Mosquitto, NATS client CLI, and Tailscale are not required. `--dashboard`
  additionally uses mise-managed Node/pnpm and installs missing dependencies.
- All services are local and loopback-only. Never run `real-device-start`,
  physical adapters, `mise run brokers`, or `brokers-down` for this workflow.
- Startup refuses occupied ports and an existing ownership record; do not
  kill another stack, purge data, or delete the record to bypass these checks.
- Before validating, state the expected externally visible result, stimulus,
  evidence to collect, and bounded wait budget. Simulator snapshots alone do
  not prove Core accepted input.

## Start

Default: the Device list from `configs/simulator.scripted.example.yaml`—one
scripted light (power and temperature) and an event source:

```sh
mise run simulator-start
```

Add the dashboard only when needed:

```sh
mise run simulator-start -- --dashboard
```

Choose a broader preset or supply your own Device definitions:

```sh
mise run simulator-start -- --preset full
mise run simulator-start -- --devices .data/my-simulator-devices.yaml
mise run simulator-start -- --devices .data/my-simulator-devices.yaml --dashboard
```

`--devices` and `--preset` are alternatives. A custom file contains **only a
YAML sequence of Devices**, not a complete simulator config. The launcher owns
transport addresses, Adapter ID, and the control listener. For example, create
an ignored `.data/my-simulator-devices.yaml` with file tools:

```yaml
- binding_key: test-light
  name: Test light
  kind: light
  entities:
    - key: power
      name: Power
      type: hearth.power/v1
      support: {state: {}, operations: {set: {}}}
      initial: false
      commands: {set: {behavior: accept-and-publish, apply_parameters: true}}
```

For complete Device/type examples, read
`configs/simulator.scripted.example.yaml` or
`configs/simulator.full.example.yaml`, copying only the list beneath `devices:`.
For value sequences, failure behaviors, health, events, and assertion recipes,
read `references/validation.md` relative to this skill directory.

**Full preset warning:** it intentionally contains an unhealthy Device, which
makes the entire Adapter unhealthy and blocks Command dispatch. Startup treats
this as a valid ready scenario, not a startup failure. Use a focused custom
Device list without the unhealthy Device for happy-path Commands.

Allow startup enough time for compilation and optional dependency installation
(e.g. a 240-second tool budget). The script:

1. Checks local ports and Herdr context before creating services.
2. Validates the generated simulator configuration, including the Device list
   against the authoritative Entity-type schemas, before creating a run
   directory or tab; an invalid `--devices` file fails immediately and names
   the offending Device and Entity.
3. Creates a unique ignored `.data/simulator-validation.*` run directory with
   generated configs, dedicated SQLite and JetStream storage, and logs.
   The similarly named `.data/simulator-validation.json` is an ownership record,
   not a run directory.
4. Creates a dedicated owned Herdr tab without changing focus, with a pane per
   service; persists ownership in `.data/simulator-validation.json`.
5. Starts NATS → waits for JetStream → starts Core → waits for readiness →
   starts the simulator → checks control inventory and online Adapter runtime.
6. Optionally starts the dashboard and checks its local proxies.

Default endpoints:

| Service | Address |
| --- | --- |
| Core HTTP | `http://127.0.0.1:8080` |
| Simulator control | `http://127.0.0.1:8181` |
| NATS | `nats://127.0.0.1:4222` |
| NATS websocket / monitoring | `ws://127.0.0.1:4223` / `http://127.0.0.1:8222` |
| Optional dashboard | `http://127.0.0.1:5173` |

On success, immediately relay **Ready for validation**, relevant URLs, the tab
ID, and the run/config paths printed by the task. Paths are relative to this
worktree; `.data/simulator-validation.json` also records the absolute run path
and pane IDs. On failure, inspect its logs
and use the printed cleanup command; do not blindly rerun into an existing tab.

## Validate through HTTP

Read `references/http-control.md` for the compact endpoint/payload reference,
State pause/publish/resume flow, and exact publication-ID matching examples. Use
this path to control running services and validate a specific change. Publish
returns `observation_id` or `event_id`; match it in Core rather than comparing
history counts. Simulator snapshots and broker acknowledgment are not Core acceptance.

For custom Device definitions, Commands, health, or recovery experiments, read
`references/validation.md`. Pause unrelated tickers and collect fresh Core
history with a deadline; don't substitute a generic smoke result for your
requested experiment.

### Optional default smoke check

For a generic baseline check only (not direct-HTTP skill evaluation), a
prepackaged proof is available for the **running default preset**:

```sh
mise run simulator-smoke
```

It sets the default light false → true, checks durable Command satisfaction and
matching applied State evidence, publishes a manual button event, and verifies
Core acceptance with null State. It saves raw responses and a machine-readable
summary in the run directory, fails nonzero on mismatch, and restores the
original ticker pause states. It does not start or stop services. **Open the
exact `summary.json` path printed by the task with the file reader** to report
concrete IDs; do not search for it with repository-indexed find/grep, which omit
ignored `.data` files. Do not repeat the same HTTP proof manually.

The smoke check is not a substitute for feature-specific or custom-Device
assertions. For those, read `references/validation.md`. Discover canonical
Entity IDs through `GET /v1/sim/entities`, inject synthetic input through the
control API, and assert outcomes through Core's State, Command, and Entity Event
history APIs. Pause output tickers for deterministic Command tests.

Use the `agent-browser` skill only for actual browser checks. A working HTTP
endpoint is not evidence that the dashboard renders correctly.

The saved lifecycle record includes pane IDs and launch commands for deliberate
in-place Core/simulator restarts during recovery experiments. Load the `herdr`
skill before manual process control. Keep the same run directory and broker
alive for Core-offline recovery; a new `simulator-start` creates fresh data and
is not a recovery test.

## Stop

```sh
mise run simulator-stop
```

Stop verifies ownership before closing only this run's Herdr tab and its pane
processes. Configs, logs, SQLite, and JetStream data remain available as evidence.
It is safe to repeat after successful cleanup. If ownership changed, stop fails
closed: inspect the record and tab instead of forcing closure or deleting data.

After code changes, prefer `mise run validate`; use repository mise tasks for
focused checks. The lifecycle safety tests run with
`mise run simulator-start-test` and are included in `validate`. The broader Go
suite still requires Docker for its real-Mosquitto tests; the simulator stack
does not.

Report scenario/config, exact stimulus, expected versus observed outcomes with
IDs, passed/failed/blocked checks, evidence paths, and cleanup status. Separate
simulator evidence from automated-test results and real-device claims.
