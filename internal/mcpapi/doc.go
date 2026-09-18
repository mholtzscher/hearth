// Package mcpapi is a thin, typed façade over the official Model Context
// Protocol Go SDK (github.com/modelcontextprotocol/go-sdk/mcp).
//
// The SDK owns protocol behavior: Streamable HTTP transport, JSON-RPC framing,
// JSON Schema derivation, argument decoding, input and output validation, and
// protocol errors. This package owns registration ergonomics only, mirroring
// Huma's typed call sites so one Hearth query or action becomes one typed MCP
// Tool without JSON-RPC plumbing in the handler.
//
// It also owns one piece of result mapping the SDK cannot express: a domain
// [ToolError] becomes an isError result carrying both human text and
// machine-readable fields, so an agent can branch on failure codes instead of
// parsing prose. Because a typed handler's inferred output schema describes
// success only, [Register] advertises the union of that schema and the
// structured failure object, so every result this package emits validates
// against the schema it published.
//
// It is deliberately small. Anything the wrapper does not cover (resources,
// prompts, sampling, elicitation, sessions, notifications) stays reachable
// through [Server.Raw], so no second MCP framework is built here. The SDK is
// imported only by this package.
//
// # Advertised schemas are portable across clients
//
// The SDK derives argument and result schemas from Go types, and that derivation
// emits two shapes strict MCP clients reject or silently drop: a boolean
// subschema (`true` for an unconstrained value, which the Inspector reports as
// an error) and an array-valued `type` (`["null", "string"]` for a nullable
// value, which a client reading `type` as one string cannot represent).
// [Register] therefore derives the argument schema itself when a Tool supplies
// none, and rewrites that schema, an explicit [Tool.InputSchema] override, and
// the advertised result union into a portable equivalent: an explicit union of
// every JSON type for the always-true schema, an impossible typed schema for the
// always-false one, and one `anyOf` type assertion per member of a union. Each
// rewrite preserves what validates, including that an unconstrained value stays
// unconstrained across every JSON type.
//
// Because the argument schema is always advertised as a decoded JSON document,
// the SDK derives nothing behind the wrapper's back: it resolves and enforces
// exactly the schema a client reads.
//
// # Automatic input rejection stays a tool result
//
// The SDK validates arguments before the typed handler runs and reports a
// failure as an isError CallToolResult, not as a JSON-RPC protocol error
// (go-sdk v1.8.0 mcp/server.go, toolForErr: `errRes.SetError(...); return
// &errRes, nil`). This package does not rewrite that result into a protocol
// error. The SDK attaches a rejection and a handler error through the same
// unexported result field with no origin marker, so a receiving middleware
// could only guess which is which, and MCP's own contract tells clients to read
// tool failures from isError results so the model can self-correct. Pinned-SDK
// deviation from the input-validation row of specs/mcp.md §5 is therefore
// intentional and its shape is pinned by a test.
//
// Package mcpapi has no Echo dependency. Use internal/mcpecho to mount a
// [Server] onto an Echo router.
package mcpapi
