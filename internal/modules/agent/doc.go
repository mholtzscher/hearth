// Package agent is Core's household agent: a server-side ReAct loop over Eino
// with conversation history in Core's SQLite. Core always constructs it, so it
// is a required module beside devices and automations rather than an
// environment-gated spike.
//
// A conversation is an append-only stream of Eino schema.Message JSON blobs
// (user, assistant with tool calls, tool results), rebuilt on every turn. The
// agent's tools are the MCP catalog itself: MCPTools dials the application's
// MCP server over an in-memory transport, so the agent and every other MCP
// client share one tool definition. Household effects stay durable in the
// commands tables exactly as REST and MCP see them: this history owns the
// conversation only, never the outcome.
//
// Deliberate decisions, each still a graduation question:
//   - History uses plain database/sql, not the sqlc pipeline the devices and
//     automations modules use; the store is three statements.
//   - Model selection, endpoint, reasoning level, and the API key file come
//     from the required `agent` Core configuration block. The key is read from
//     its own local secret file, and Core always constructs the agent, so the
//     module owns no environment gate and no disabled mode.
//   - Only assistant-final text plus the tool-call trace cross the HTTP
//     boundary; full message JSON stays server-side.
//   - Eino is outside the fixed implementation stack docs/architecture.md
//     names, and checkpoint state is not persisted: a dropped turn leaves the
//     persisted user message and whatever streamed messages completed, and the
//     next turn rebuilds from those rows.
package agent
