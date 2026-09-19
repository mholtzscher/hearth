// Package agent is an experimental spike: a server-side household agent built
// on Eino's ReAct loop with conversation history in Core's SQLite.
//
// A conversation is an append-only stream of Eino schema.Message JSON blobs
// (user, assistant with tool calls, tool results), rebuilt on every turn. The
// agent's tools are the MCP catalog itself: MCPTools dials the application's
// MCP server over an in-memory transport, so the agent and every other MCP
// client share one tool definition. Household effects stay durable in the
// commands tables exactly as REST and MCP see them: this history owns the
// conversation only, never the outcome.
//
// Deliberate spike decisions, each a graduation question:
//   - History uses plain database/sql, not the sqlc pipeline the devices and
//     automations modules use; the store is three statements.
//   - Model credentials come from the environment (OPENAI_API_KEY,
//     OPENAI_MODEL, OPENAI_BASE_URL) with no config-file keys; application
//     assembly skips the /v1/agent routes when no key is present.
//   - Only assistant-final text plus the tool-call trace cross the HTTP
//     boundary; full message JSON stays server-side.
//   - Eino is outside the fixed implementation stack docs/architecture.md
//     names, and checkpoint state is not persisted: a dropped turn leaves the
//     persisted user message and whatever streamed messages completed, and the
//     next turn rebuilds from those rows.
package agent
