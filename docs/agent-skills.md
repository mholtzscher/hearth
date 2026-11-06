# Agent automation skill and real-model evaluation

The household agent uses an embedded `hearth-automations` skill through native
ADK skill middleware. The model decides when to load it; it is guidance, not
a write-authorization gate. It describes discovery, clarification, supported
Entity capabilities, trigger routing, validation recovery, and reading back a
saved Automation. A model turn is not permission to run the Automation as a test.

## Provider connection

The default `openai` provider uses OpenAI's Responses API. `agent.base_url`, when configured, must
name a compatible API root (for example, `https://api.openai.com/v1`), not a
Chat Completions endpoint. Reasoning and function tools are sent together through
`/responses`. Model and reasoning defaults remain unchanged; configure both
explicitly to use a reasoning-capable model.

Hearth retains the full provider output items, including encrypted reasoning,
in private conversation metadata and sends them back with tool results. This
supports continuation after a restart without provider-side conversation storage
or `previous_response_id`. The public history and SSE payloads expose text and
tool activity. Automatic SDK retries are disabled. Failed or incomplete provider
responses fail the turn before any returned tool calls execute.

Start a new conversation after migrating from Chat Completions: old assistant
messages do not contain the required Responses continuation items.

### OpenCode Go

`agent.provider: opencode-go` selects a separate Chat Completions adapter with
`deepseek-v4.1-flash` as its default model. Assembly reads only
`HEARTH_OPENCODE_GO_API_KEY`; it never falls back to the OpenAI credential.
The default API root is `https://opencode.ai/inference/go/openai/v1`.
An explicit `agent.base_url` must name a compatible Chat Completions API root.
Reasoning effort is omitted by default. Explicit supported values for the initial
DeepSeek target are `low`, `high`, and `max`, not OpenAI's `none` or `medium`.

Hearth sends `User-Agent: hearth/1.0` and a request-scoped `x-opencode-session`
equal to the canonical conversation ID. Concurrent conversations never share a
mutable session header. The adapter retains `reasoning_content` alongside native
tool calls for every assistant response, including final replies, and restores it
after a database reopen. Reasoning never enters public history or SSE payloads.
Automatic SDK retries remain disabled. Failed, truncated, malformed, and
token-limited responses fail before their tool calls execute. The complete
assistant batch still commits before any tool runs.

The first user message privately pins its provider outside the recent-history
budget. Missing legacy metadata means OpenAI. Switching provider requires a new
conversation; the rejected turn does not append history or contact a model.
Reading old conversations and their view artifacts remains supported. Changing a
model within one provider is not a guarantee of continuation compatibility.

OpenCode's [Go guide](https://opencode.ai/v2/docs/console/go#where-can-i-use-it)
describes coding-agent traffic as the intended use. Household-assistant
eligibility remains unconfirmed. Do not disguise Hearth as another client.
The [public catalog](https://opencode.ai/inference/go/v1/models) advertised the
selected model's Chat Completions API, tool support, and `reasoning_content`
field when this integration was developed. Deterministic protocol and restart
tests are synthetic. The live result below covers one read-only scenario;
the recorded OpenAI A5 evaluation does not cover Go.

### Recorded Go validation: 2026-11-05

Source revision `be9a010` passed a read-only journey through the production agent
HTTP API with `opencode-go`, `deepseek-v4.1-flash`, reasoning `low`, and four
iterations. An isolated worktree used simulated Devices and explicit Core CLI
flags. The first successful turn called `list_devices` once and returned the
first Device's canonical ID. After a managed Core restart with a new process ID,
the same conversation returned that ID without another tool call. Public history
was unchanged across restart. SQLite retained native tool/result identity and
private reasoning on all three assistant messages, without exposing that
reasoning in public history. Command, Automation, and dashboard lists remained
empty. Owned daemons and listeners were stopped; runtime data was retained.

The earlier Go turn failed before any tool ran. A diagnostic request reported
HTTP 429, upstream overloaded. The user then approved one controlled retry in a
fresh conversation, which produced the passing result. This is not a reliability
rate or evidence of automatic retries. An earlier launch used OpenAI because
temporary shell environment overrides did not reach the daemon supervisor; it
is excluded from Go acceptance. Explicit flags and persisted provider metadata
established the target for the Go run.

The successful conversation was
`aconv_01a1addbe119775bae37aed705855e80`. Evidence is retained in the local
`test-agent-opencode-go-live` worktree under
`.data/verification/go-deepseek-live/`, including request/status/response
records, process identities, SQLite message snapshots, and cleanup results.
Raw message evidence includes private provider reasoning and is not a public
attachment. No credentials are recorded. Browser behavior, Automation authoring,
physical Devices, other Go models, and household-use eligibility were not
established by this check.

## Opt-in A5 evaluation

### Recorded acceptance — 2026-11-01

A5 passed on `53a3d45` with `gpt-5.4-2026-03-05`, reasoning `medium`,
`max_iterations=10`, and the Responses API. The agent asked which Office fan
before saving, accepted `Use Office Plug Fan.`, loaded the skill, and created
and read back one enabled Automation. Authoring issued no Commands. Single and
double button Events produced two successful Runs and exactly six satisfied
Commands for the selected fan and two lamps; unrelated targets and non-default
lamp settings stayed unchanged.

The assistant reviewed the clarification through the terminal and accepted the
script's `human_clarification_review` prompt; this was not an independent human
review. The trace loaded the skill alongside `list_entities`, then called
`list_devices`, so it did not strictly follow the skill's Device-first order.

Four earlier attempts remain part of the evidence: two `gpt-5.6-luna`/`none`
attempts saved without clarification; a `low` reasoning experiment and a
GPT-5.4/`medium` Chat Completions experiment failed at the model boundary. The
latter's provider diagnostic required Responses for reasoning with tools.
All five isolated stacks were stopped and their data retained. Production
defaults remain `gpt-5.6-luna`/`none`; this pass establishes the configured
GPT-5.4 Responses scenario, not default-model acceptance or a reliability rate.

### Dashboard demonstration

[Watch the agent create an Automation](media/agent-automation-demo.mp4).

Recorded from the real dashboard at `004cd6d` with isolated simulated Devices,
`gpt-5.4-2026-03-05`, and medium reasoning. The video shows skill loading,
fan clarification, creation, readback, and the enabled Automation. Core history
confirms one save and no authoring Commands. This recording covers authoring;
button execution is covered by the A5 evaluation above. The final frame is held
for six seconds for readability; the interaction plays at its recorded speed.

### Scope and prerequisites

This is an isolated, billable real-model journey, not a CI test. It uses the
production agent HTTP API and Core reads with synthetic Devices. A single pass
does not establish reliability or physical-device behavior. Do not point it at
a household, shared stack, or physical adapter. An incomplete or failed attempt
is evidence; investigate it rather than rerunning until green.

Prerequisites: a dedicated fresh worktree, Mise 2026.9.12+, repository tools,
Python 3.9+, `curl`, `jq`, and a non-whitespace `HEARTH_OPENAI_API_KEY` inherited
by Core. Never print or capture the credential or the complete environment. Do
not change runtime source or configuration after launch. Ensure no daemon in
this worktree is running and `.data/simulator-stack/storage` does not exist;
use a different fresh worktree rather than deleting retained data. Check the
`verify-hearth` skill's launch and Doctor steps, including process identities,
worktree ports, and a stopped `zwave-adapter`. You own cleanup once launch starts.

### Configure the managed fixture

The checked-in `testdata/agent-skills-eval.simulator.yaml` has one manual-only
button, two plausible Office fans, two lamps with non-default settings, and two
unrelated lights. There are no output tickers. Follow the simulator-validation
skill's `references/validation.md` custom configuration procedure: stop any
owned stack before changes, preserve any existing `[daemons.simulator]` local
table and all unrelated local settings, copy the fixture to
`.data/simulator-stack/agent-skills-eval.simulator.yaml`, and add this complete
table to `mise.local.toml`:

```toml
[daemons.simulator]
run = "exec go run ./cmd/hearth-simulator -config .data/simulator-stack/agent-skills-eval.simulator.yaml -nats-url nats://127.0.0.1:$SIM_NATS_PORT -control-addr 127.0.0.1:$SIMULATOR_PORT"
port = { auto = true, base = 8181, stride = 10 }
proxy = false
depends = ["sim-core"]
ready_cmd = "curl -fsS http://127.0.0.1:$PORT/v1/sim/entities >/dev/null"
```

For example, after recording the preconditions and preserving any local table:

```sh
mkdir -p .data/simulator-stack
cp testdata/agent-skills-eval.simulator.yaml .data/simulator-stack/agent-skills-eval.simulator.yaml
mise run simulator-start
```

After start, run the `verify-hearth` Doctor with its default-preset inventory
assertion replaced by this fixture's `sim-agent-eval` inventory. Discover only
selected ports, never the full environment:

```sh
mise env --json | jq '{SIM_NATS_PORT, SIM_CORE_PORT, SIMULATOR_PORT, SIM_WEB_PORT}'
export CORE="http://127.0.0.1:$(mise env --json | jq -er .SIM_CORE_PORT)"
export SIM="http://127.0.0.1:$(mise env --json | jq -er .SIMULATOR_PORT)"
```

Verify the live daemon command lines and local configuration correspond to this
worktree and preset, the expected Core database is in use, no physical adapter
is running, and no source/config changes occurred since startup. Endpoint syntax
alone cannot prove a process belongs to this worktree. Do not substitute the
standard preset or fixed ports.

### Run once and review

Use a **new** evidence directory outside `.data/simulator-stack/storage`; the
script refuses existing directories. Supply the actual nonsecret model name,
reasoning effort and iteration limit from the running Core configuration,
including defaults when unset (see `internal/app/hearthd/config.go`). Do not put
credentials in this argument or in other artifacts. The script never reads them.
For the stock simulator configuration, model defaults are selected in Core;
inspect the current source and settings rather than assuming a model name.

```sh
python3 scripts/agent-skills-eval.py \
  --core "$CORE" --sim "$SIM" \
  --db "$PWD/.data/simulator-stack/storage/hearthd.db" \
  --evidence "$PWD/.data/verification/agent-skills-$(date -u +%Y%m%dT%H%M%SZ)-$$" \
  --model-settings 'model=ACTUAL_MODEL reasoning_effort=ACTUAL_EFFORT max_iterations=ACTUAL_LIMIT'
```

Replace the placeholder settings. At the prompt, examine the first reply and
answer `y` **only** if it asks which of the two Office fans to use, without
claiming to have saved an Automation. This human check is intentional; a
substring cannot reliably establish that semantic clarification occurred.
Rejecting or interrupting stops the journey before the second message. The
script uses the exact original request and then `Use Office Plug Fan.` It
requires a fresh Core inventory, no existing conversation, Automation, Command,
or button Event, and the expected initial states. Missing registration or
pagination fails closed. Reads have a two-second request timeout and a 30-second
poll budget; each model turn has a 180-second timeout. Each mutation is issued
once. An HTTP timeout may hide a completed write: inspect retained history and
never blindly repeat it.

The result requires evidence of both fan names in first-turn tool results,
accepted human clarification, no authoring Commands, one enabled Automation,
a paired native skill load and saved-definition readback. For each injected
`single` and `double` Event it matches the exact accepted Event ID to one
successful Run, three verified Command IDs, the exact selected power targets,
`set` values and six satisfied outcomes. Production Entity reads and Command
history establish the target and unchanged unrelated fan, light, brightness and
color settings. The behavioral routing check is via these two exact events,
not an exhaustive proof of the definition for all other circumstances.

Inspect `result.json`, numbered `http-*.json` request/status/raw response
records, `raw-messages-first.json` and `raw-messages-final.json` (which include
verbatim SQLite `schema.Message` JSON with tool arguments and full results),
`automation.json`, Event and Run records, revision, dirty status, diff, and
operator-supplied model settings. These artifacts may contain household-like
conversation data; handle accordingly. HTTP transcript and read-only SQLite
are evidence; simulator snapshots are only discovery/stimulus. If a turn fails,
`raw-messages-on-failure.json` is attempted. An uncertain POST, missing readback,
or unavailable model blocks acceptance; retain the failure and diagnose before
any new mutation. The script does not alter config, launch services, clean up
Automations, or retry.

### Cleanup

After inspection, including on partial launch or failure, stop only the stack
owned by this run:

```sh
mise run simulator-stop
```

Check daemon state, retain SQLite, JetStream, logs and evidence, then restore the
prior `[daemons.simulator]` table (or remove `mise.local.toml` only if you
created it solely for this override). Do not delete another operator's local
settings. Record cleanup and any unresolved ambiguity with the result. A new
fresh-baseline attempt needs a fresh worktree, not removal of this one's data.
