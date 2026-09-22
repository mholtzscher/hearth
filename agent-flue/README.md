# Flue household-agent spike

This is an isolated evaluation of Flue 2.1 as Hearth's agent runtime. It does
not replace, proxy, or modify the required Go/Eino agent. Hearth remains the
owner of devices, automations, Commands, and their durable outcomes; Flue only
owns its conversation and execution records.

## Run

Start `hearthd` normally so its streamable HTTP MCP endpoint is available at
`http://127.0.0.1:8080/mcp`. Then:

```sh
cp agent-flue/.env.example agent-flue/.env
# Set OPENAI_API_KEY in agent-flue/.env.
mise run flue-agent-dev
```

Send a first message. The instance ID (`spike-1` here) is the durable
conversation address; Flue creates it on first contact.

```sh
curl -i -X POST http://127.0.0.1:5174/agents/household/spike-1 \
  -H 'content-type: application/json' \
  -d '{"kind":"user","body":"List the lights and report their current state."}'
```

The response is `202 Accepted` and includes the conversation stream URL. Read
that URL directly, or inspect the conversation with:

```sh
curl 'http://127.0.0.1:5174/agents/household/spike-1?view=history'
```

Flue stores its records in `.data/flue-agent.db`, separate from Hearth's
database. Delete it only when intentionally resetting spike history.

To exercise the built artifact, supply provider credentials through the process
environment rather than `.env` and use the loopback-only wrapper:

```sh
mise run flue-agent-build
OPENAI_API_KEY="$(cat .data/agent-api-key)" pnpm --dir agent-flue run start
```

Do not run Flue's generated `dist/server.mjs` directly: its Node listener binds
all network interfaces. `server.mjs` is the supported spike entry point and
always binds `127.0.0.1`. There is no authentication on the Flue routes.

## Evaluation scope

Compare the Flue and Go agents with the same prompts:

1. Read current entity state without taking action.
2. Resolve an ambiguous entity name before acting.
3. Execute a command and report Hearth's returned Command ID and status.
4. Ask for an unavailable or unsupported operation.
5. Stop Flue during model output and after a read-only tool starts, restart it,
   and inspect recovery.
6. Stop Flue around command submission. Check Hearth command history for
   duplicate intent; Flue recovery does not make remote MCP effects exactly
   once.
7. Send concurrent messages to one instance and confirm accepted ordering.

Record startup time, idle memory, turn latency, model/tool traces, and any
differences in answer quality. Do not enable a Flue sandbox for this agent: it
needs Hearth's bounded MCP tools, not host filesystem or shell access.

## Deliberate omissions

- The existing dashboard contract is not adapted. This first spike uses Flue's
  native API so runtime behavior can be evaluated before writing compatibility
  code.
- Flue health is not part of `hearthd` readiness.
- The MCP catalog is not authenticated today and assumes Hearth's existing
  trusted-network boundary. The spike adapts the full tool catalog, including
  automation definition mutations and manual Automation Runs; on an untrusted
  network that boundary is not sufficient.
- Conversation retention is not implemented. The database is disposable spike
  state.
