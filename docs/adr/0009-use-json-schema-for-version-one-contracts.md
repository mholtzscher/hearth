# Use JSON Schema for version one contracts

Version 1 messages across the NATS adapter boundary will be readable JSON checked against versioned, language-neutral JSON Schemas. This gives non-Go adapters and operators an inspectable contract at the cost of generated-binary efficiency and stricter Protobuf tooling; sharing only Go structs would make the first implementation simpler but would turn an implementation package into the compatibility boundary.
