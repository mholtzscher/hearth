# Serve MCP from the core HTTP server at `/mcp`

Hearth exposes Model Context Protocol as a same-process Streamable HTTP endpoint
mounted on the Core Echo server at `/mcp`. The official Go MCP SDK handles the
protocol behind a typed registration wrapper. MCP shares domain services, trust
posture, and process lifecycle with HTTP rather than introducing another server
or deployment.

Considered alternatives:

- A separate MCP port or process would split operations and lifecycle without a
  durability, isolation, routing, or observability gain.
- Stdio transport conflicts with the `hearthd` daemon and NATS lifecycle.
- Reimplementing MCP on Echo/Huma would duplicate the official SDK's protocol
  responsibility.

**Consequences:** MCP inherits the unauthenticated trusted-network posture with zero new config keys; blocking command tools hold connections up to operation deadlines, which is safe because Core commits before dispatch. Generic SDK registration and prepared-contract mechanics live in `internal/platform/mcpapi`; module adapters own tool arguments, results, and domain failure mapping.

The [protocol-specific contract cutover](../../specs/mcp-tools-and-contract-preparation.md)
retains this listener and transport decision. It separates module HTTP and MCP
adapters, removes resources and advertised output schemas, and retains
server-side success validation. One catalog still serves the endpoint and agent.
