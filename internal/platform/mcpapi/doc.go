// Package mcpapi provides domain-neutral typed tools over the official MCP SDK.
// The SDK owns protocol behavior, sessions, transports, and request contexts.
// Register derives per-server input and success contracts from concrete types.
// Hearth owns exact argument decoding, bounded optional-property defaults, and
// validation against the complete portable advertised input schema. Custom input
// decoders run before that shared validation; handlers run only after both pass.
// No output schema is advertised.
// Huma supplies schema generation, constraint tags, and native schema hooks only;
// it does not decode, validate, or serve MCP requests.
//
// Register encodes success once, validates a decoded copy,
// and publish the original JSON as structured content and text. Output defaults
// are never inserted. ToolError preserves modeled failures and safe diagnostic
// causes independently of success validation. Every modeled failure publishes
// matching JSON text and structured content. Shared input rejections use
// invalid_arguments; unexpected faults remain private, and genuine protocol
// failures stay on the SDK error channel. EnvelopeValidator preserves original
// request-size bounds before parsing. JSONObject preserves opaque input objects.
//
// Preparation belongs to each registration, with no cross-server cache. Handlers
// and services remain bound to their own server. Complete registration before
// serving requests. The package imports no product
// modules and constructs no HTTP API.
// Use internal/mcpecho to mount a Server on an Echo router; raw SDK mechanics
// remain available through Server.Raw.
package mcpapi
