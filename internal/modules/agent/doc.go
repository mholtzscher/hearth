// Package agent is Core's household agent: an Eino ADK ChatModelAgent and Runner
// with conversation history in Core's SQLite. Core always constructs it, so it
// is a required module beside devices and automations rather than an
// environment-gated spike.
//
// A conversation is an append-only stream of Eino schema.Message JSON blobs
// (user, assistant with tool calls, tool results), rebuilt on every turn. The
// household tools are the MCP catalog itself: MCPTools dials the application's
// MCP server over an in-memory transport, so the agent and every other MCP
// client share one tool definition. Household effects stay durable in the
// commands tables exactly as REST and MCP see them: this history owns the
// conversation only, never the outcome. An inline embedded automation skill is
// available only to this agent, not external MCP clients. Model/tool hooks commit
// native assistant batches and call results; dangling-call repair adds explicit
// unknown-outcome placeholders to model input only, never execution evidence.
// The provider boundary uses OpenAI Responses with stateless context and no
// automatic retries. Native output items, including encrypted reasoning, remain
// private message metadata; public history exposes text and tool activity.
//
// Deliberate decisions, each still a graduation question:
//   - Conversation history uses the sqlc pipeline the devices and automations
//     modules use: generated statements in sqlite/dbqueries with generated Go
//     in sqlite/dbsqlc, keeping the public service types unchanged.
//   - Model selection, endpoint, reasoning level, and generation-cycle limits
//     come from the required agent Core configuration block. Assembly reads the
//     key from HEARTH_OPENAI_API_KEY; the module owns no environment gate.
//   - Only assistant-final text plus the tool-call trace cross the HTTP
//     boundary; full message JSON stays server-side.
//   - Eino is outside the fixed implementation stack docs/architecture.md
//     names, and checkpoint state is not persisted: a dropped turn leaves the
//     persisted user message and whatever streamed messages completed, and the
//     next turn rebuilds from those rows.
package agent
