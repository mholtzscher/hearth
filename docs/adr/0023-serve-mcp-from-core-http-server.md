# Serve MCP from the core HTTP server at `/mcp`

Hearth will expose Model Context Protocol as a same-process Streamable HTTP endpoint mounted on the core Echo server at `/mcp`, using the official Go MCP SDK for protocol handling behind a thin Huma-style `mcpapi` registration wrapper — because MCP must share the services, trust posture, and process lifecycle of the existing HTTP API rather than become a second server with its own auth, config, and deployment story.

**Considered Options:** a separate MCP port/process (rejected: splits ops surface and lifecycle for no durability, isolation, routing, or observability gain — and every production NATS/transport use must justify itself on those grounds); stdio transport (rejected: conflicts with the `hearthd` daemon + NATS lifecycle); reimplementing MCP on Echo/Huma (rejected: the official SDK owns the protocol).

**Consequences:** MCP inherits the unauthenticated trusted-network posture with zero new config keys; blocking command tools hold connections up to operation deadlines, which is safe because Core commits before dispatch. All SDK contact stays behind `internal/mcpapi` so SDK churn touches one package.
