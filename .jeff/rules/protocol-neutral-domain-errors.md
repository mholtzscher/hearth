---
id: protocol-neutral-domain-errors
description: Domain errors expose module decisions rather than transport policy
include: ['internal/modules/*/*.go']
exclude: ['**/*_test.go', '**/*_gen.go', '**/dbsqlc/**', '**/testdata/**']
scope: file
severity: warning
threshold: 0.90
message: Review whether a domain error owns transport status, serialization, or logging policy that belongs at its protocol boundary.
---
# Keep domain error vocabulary protocol-neutral

Source: CODING_STANDARDS.md, "Keep actionable error classes with their module"
and "Map errors at the owning protocol boundary".
This is a focused restatement, not a new module-boundary policy.

Module-owned domain errors expose distinctions callers need. Use sentinels for
actionable classes, and typed errors when callers consume structured information
or a private diagnostic cause. HTTP statuses, MCP codes, wire error serialization,
and diagnostic severity belong to the owning protocol boundary, not domain errors.

## Violations visible in one file

- A domain error stores or selects an HTTP status, MCP protocol error code, or
  transport-specific response payload as part of its domain error contract.
- A domain error chooses a diagnostic log level or acts as a universal error
  taxonomy combining transport, logging, and retry policy for unrelated modules.

## Exceptions and uncertainty

- Domain reason codes, durable record identities, outcome evidence, and private
  diagnostic causes are legitimate structured error information.
- A module may own its retry and lifecycle policy; this rule does not ban domain
  outcomes or turn recorded rejections and timeouts into generic Go errors.
- Ordinary contextual errors and unchanged error propagation are valid. Do not
  demand a new type for each failure or an interface with no current consumer.
- A domain Error() method is not itself transport serialization.
- Imports, names, numeric constants, and context errors alone do not establish
  transport policy. Judge the error's visible role and behavior.
- Choose uncertain if the file does not establish whether the declaration is a
  domain error or a protocol-boundary implementation.
- Files without applicable error declarations are compliant.
