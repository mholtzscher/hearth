# Agent skills and provider continuation

The household agent uses an embedded `hearth-automations` skill through native
ADK skill middleware. The model decides when to load it; it is guidance, not
a write-authorization gate. It describes discovery, clarification, supported
Entity capabilities, trigger routing, validation recovery, and existing
Automations. The Agent can present a draft through `present_automation`, but
cannot call the direct `create_automation` or `replace_automation` MCP tools.
Only an explicit browser Save persists a draft. A model turn is not permission
to save or run an Automation. See [dashboard draft and save behavior](dashboards.md).

## Provider connection

The default `openai` provider uses OpenAI's Responses API. `agent.base_url`, when configured, must
name a compatible API root (for example, `https://api.openai.com/v1`), not a
Chat Completions endpoint. Reasoning and function tools are sent together through
`/responses`. Configure the model and reasoning explicitly for the intended
provider; [Core configuration](../../internal/app/hearthd/config.go) owns defaults.

Hearth retains the full provider output items, including encrypted reasoning,
in private conversation metadata and sends them back with tool results. This
supports continuation after a restart without provider-side conversation storage
or `previous_response_id`. The public history and SSE payloads expose text and
tool activity. The SDK retries a request once after a connection failure or a
408, 409, 429, or 5xx status, honoring `Retry-After`; this happens before any
response streams, so no tool call is announced or run twice. Malformed,
truncated, and other failed provider responses are never retried and fail the
turn before any returned tool calls execute.

Start a new conversation after migrating from Chat Completions: old assistant
messages do not contain the required Responses continuation items.

### OpenCode Go

`agent.provider: opencode-go` selects a separate Chat Completions adapter.
Assembly reads only
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
It uses the same single transient-status retry. Failed, truncated, malformed,
and token-limited responses are not retried and fail before their tool calls
execute. The complete
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
tests are synthetic; they do not establish household-use eligibility or
physical-device behavior.

## Configuration compatibility

The removed `agent.max_steps` key is rejected, including when set to zero or null.
Use `agent.max_iterations`, `--agent-max-iterations`, or
`HEARTHD_AGENT_MAX_ITERATIONS`. This limit counts model-generation cycles, not
the former graph steps; the values are not interchangeable.

## Public turn failures

Blocking HTTP turns return 502 for a provider call failure or nil model response,
503 for caller/Drain cancellation or caller expiry, and a safe internal 500 for
tool transport, trace persistence, or other setup faults. A provider timeout with
an active caller remains a model failure, not caller cancellation.

The SSE message route is mounted directly on Echo and is not in OpenAPI.
It streams the same turn as the blocking route. After headers commit,
unexpected failure ends with opaque `turn.failed` text rather than a new HTTP
status. Expected caller/Drain cancellation retains its cancellation message.
Modeled MCP failures remain recoverable tool observations; transport/session
and trace persistence faults are turn-fatal. Private infrastructure errors do
not become public history or SSE text. See the
[Agent HTTP boundary](../../internal/modules/agent/register.go) and
[stream handler](../../internal/modules/agent/stream.go).
