// Package mcpapi provides domain-neutral typed tools over the official MCP SDK.
// The SDK owns protocol behavior, input schema resolution, argument validation,
// defaults, and decoding. Registration owns a portable schema document without
// compiling another input validator, and compiles an immutable success
// contract; no output schema is advertised.
// Huma supplies schema generation, constraint tags, and native schema hooks only;
// it does not decode, validate, or serve MCP requests.
//
// Register and RegisterWithRequest encode success once, validate a decoded copy,
// and publish the original JSON as structured content and text. Output defaults
// are never inserted. ToolError preserves modeled failures and safe diagnostic
// causes independently of success validation. SDK input rejection remains an
// isError tool result without structured domain content.
//
// Preparation belongs to each registration, with no cross-server cache. Handlers
// and services remain bound to their own server. The package imports no product
// modules and constructs no HTTP API.
// Use internal/mcpecho to mount a Server on an Echo router; raw SDK mechanics
// remain available through Server.Raw.
package mcpapi
