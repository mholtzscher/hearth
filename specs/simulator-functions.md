# Native simulator Adapter Functions

Status: implemented in the working tree. Focused validation recorded below.
Effort: M. Not part of the Adapter Functions product PR.

## Purpose and decision

Extend the config-driven simulator with YAML-declared Adapter Functions so an
agent can validate Core's Function discovery, catalog revision, argument and
result contracts, invocation routing, retained history, and terminal statuses
without a vendor adapter or a real bridge. The simulator registers one immutable
catalog per Session through the same SDK `StartFunctionServer` real Adapters use;
Core stays the authority for discovery, validation, routing, and evidence.

Configuration-only outcomes cannot deterministically test a pending invocation,
so each function may also `hold` until a loopback control release. A full
transport fault lab, per-invocation behavior mutation, and capture history were
rejected as unnecessary: Core's invocation history is authoritative, and the
simulator supplies behavior only.

## Implementation contract

`internal/app/simulator/config.go` gains an optional per-Adapter collection:

```diff
 type ScriptedAdapterConfig struct {
     AdapterID     string                      `yaml:"adapter_id"`
     Devices       []scripted.DeviceSpec       `yaml:"devices"`
     Notifications []scripted.NotificationSpec `yaml:"notifications"`
+    Functions     []scripted.FunctionSpec     `yaml:"functions"`
 }
```

An Adapter is valid when it declares at least one Device, notification
destination, or function. Function-only Adapters are permitted; existing
Device-only and notification-only YAML remains valid.

New types owned by `internal/adapters/scripted/functions.go`:

```go
type FunctionSpec struct {
    Key           string `yaml:"key"`
    Description   string `yaml:"description"`
    TimeoutMS     int    `yaml:"timeout_ms"`
    InputSchema   any    `yaml:"input_schema"`
    OutputSchema  any    `yaml:"output_schema"`
    Outcome       string `yaml:"outcome"`
    ReasonCode    string `yaml:"reason_code"`
    Result        any    `yaml:"result"`
    EchoArguments bool   `yaml:"echo_arguments"`
    Hold          bool   `yaml:"hold"`
}

type FunctionRuntime struct { /* prepared functions + pending hold releases */ }

func ValidateFunctions([]FunctionSpec) error
func NewFunctionRuntime([]FunctionSpec) (*FunctionRuntime, error)
func (*FunctionRuntime) Functions() []adapter.Function
func (*FunctionRuntime) Release(invocationID string) error
func (*FunctionRuntime) Stop()
```

Rules:

- `key` is a unique SDK slug per Adapter; `description` is 1-2000 code points;
  `timeout_ms` defaults to 1000 and must be 100-60000.
- `input_schema` and `output_schema` are required and compiled with
  `contracts/functionschema`, the same bounded profile Core and the SDK use.
- `outcome` is `succeeded`, `failed`, `not_executed`, or `unknown` and defaults
  to `succeeded`. The three non-success outcomes require a `reason_code`
  matching `^[a-z0-9][a-z0-9_.-]{0,127}$` and reject `result` or
  `echo_arguments`.
- `succeeded` returns the configured `result` normalized against the output
  schema, defaulting to `{}`, or echoes the SDK-normalized argument bytes when
  `echo_arguments` is true. The two are mutually exclusive.
- `hold` waits for release, runtime `Stop`, or handler-context cancellation. A
  release before the deadline returns the configured outcome; release after the
  deadline yields `unknown/outcome_timeout` through the SDK; `Stop` yields
  `unknown/adapter_stopping`. No runtime mutex is held while waiting, and
  handlers may overlap.
- The runtime never interprets a function name and adds no vendor behavior.

`internal/app/simulator/run_scripted.go` extends the app-owned `scriptedSession`
seam with `StartFunctionServer`, starts the catalog next to the notification
server, observes its `Wait` in a supervised worker, and closes it before the
Session. A worker-context callback stops held handlers during an ambiguous
function registration, before `StartFunctionServer` returns. Shutdown also calls
`FunctionRuntime.Stop()` before `FunctionServer.Close`, so a held handler honors
app shutdown instead of waiting for its deadline. Server failures report through
a nonblocking first-error channel, so two simultaneous failures cannot block the
worker join. Readiness includes catalog registration. `run_multiple.go`,
`control.go`, and the new `control_functions.go` carry the runtimes to the
control channel.

Control channel:

| Endpoint | Contract |
| --- | --- |
| POST /v1/sim/functions/invocations/{invocation_id}/release | 204 release; 404 unknown, completed, or post-restart |

There is no list endpoint, capture, or behavior mutation. Discover the running
`afi_...` ID from Core's
`GET /v1/adapters/{adapter_id}/function-invocations?function_key=...`, then read
the terminal invocation from
`GET /v1/adapter-function-invocations/{invocation_id}`.

## Layout and deliverables

```text
internal/
  adapters/scripted/
    functions.go                 new: config, validation, runtime, hold/release
    functions_test.go            new: behavior, exact echo, cancellation, concurrency
  app/simulator/
    config.go                    modify: Functions field and validation
    run_scripted.go              modify: Session seam, start/wait/join
    run_multiple.go              modify: carry function runtime to control
    control.go                   modify: pass function runtimes
    control_functions.go         new: release endpoint
    control_functions_test.go    new: 204/404, held invocation
    config_scripted_test.go      modify: function-only and invalid configs
    run_scripted_test.go         modify: start-failure lifecycle
  app/hearthd/
    simulator_functions_integration_test.go  new: real Core + SDK + simulator
configs/scripted.simulator.yaml  modify: five presets on sim-healthy
```

| ID | Outcome | Owner | Depends | Acceptance | Effort |
| --- | --- | --- | --- | --- | --- |
| D1 | Validated function config and runtime | scripted functions + simulator config | none | V1, V2 | M |
| D2 | SDK lifecycle and release control | simulator run/control files | D1 | V3 | M |
| D3 | Real Core discovery, statuses, and held evidence | hearthd integration, example config, skill references | D2 | V4, V5 | M |

## Acceptance and validation

- V1: Old Device-only, notification-only, and mixed examples load unchanged;
  function-only Adapters work; duplicate keys, invalid keys and descriptions,
  bad timeouts, missing or unsupported schemas, invalid results, mixed
  `result`/`echo_arguments`, and missing or invalid reason codes fail before NATS.
- V2: Static success, exact argument echo, all three non-success statuses, two
  independently released holds, duplicate-admission rejection, cancellation, and
  idempotent-while-running release behave as documented.
- V3: Function server start failure aborts before readiness; shutdown calls
  `Stop`, releases a held handler, and joins before `Session.Close`.
- V4: Real Core discovers the scripted catalog, invokes every status, retains
  exact arguments and results, exposes a running held invocation, and records
  its release as terminal succeeded.
- V5: `mise run simulator-start`; the checked-in `sim-healthy` presets load and
  the release control reaches the running invocation.

Focused commands:

```sh
GO_PACKAGES='./internal/adapters/scripted ./internal/app/simulator' mise run --skip-deps test
GO_PACKAGES='./internal/app/hearthd' mise run --skip-deps test -- -run TestSimulatorAdapterFunctionsEndToEnd
```

## Implementation evidence, 2026-10-19

- Focused race tests passed for `./internal/adapters/scripted` and
  `./internal/app/simulator`.
- `TestSimulatorAdapterFunctionsEndToEnd` passed against real Core, NATS, the
  SDK, and the simulator app: discovery, exact argument/result retention, all
  four terminal statuses, and a running held invocation released to succeeded.
- `TestSimulatorFunctionHoldJoinsShutdown` initially failed because a held
  handler ignored app shutdown and blocked `FunctionServer.Close` until its
  deadline. The owner fix added `FunctionRuntime.Stop`; the test now proves
  simulator shutdown joins the held handler before its deadline.
- `TestRunScriptedSessionStopsHeldFunctionDuringStartupCancellation` proves a
  held handler admitted before `StartFunctionServer` returns cannot delay
  shutdown when registration is lost.
- `mise run validate` passed, including race-enabled tests, lint, vet,
  generation, formatting, and module tidiness.
- The managed live script was not run in this environment because
  `$HOME/.local/share/agenix/hearth-openai-api-key` is absent. The script is
  checked for shell/Python syntax but has no live evidence yet. Its release poll
  now returns and checks the HTTP status, so the held path can distinguish the
  404 admission retry from 204.

## Limits and risks

No Discord or Zigbee2MQTT vendor behavior, no malformed wire injection, no
transport-timeout simulation, and no capture retention. An explicit
`failed`/`unknown`/`not_executed` reply proves the simulator reported that
status; it does not establish a transport fault. Core history, not simulator
state, is authoritative evidence, and a control 204 proves release, not Core
completion.

Main risks: a held handler can block shutdown. Context cancellation, `Close`,
and a supervised join mitigate it. Configured outcomes can be mistaken for
transport behavior. Documentation and Core-side evidence mitigate that.
