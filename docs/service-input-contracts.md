# Service input error contracts

Devices and Adapters validate independently callable service and repository entry
points. HTTP/MCP validation does not replace those checks. Canonical UUID parsing
and Adapter-slug validation own identity shape; operation-specific error classes
own distinctions that current callers actually use. There is no universal
invalid-identity sentinel or requirement that every parse error acquire one.

## Scoped classes

| Entry point | Invalid input contract |
| --- | --- |
| Device/Entity/Command direct reads and Entity enablement | Reject malformed identities before storage; return contextual canonical-parser errors, not not-found |
| Device/Entity metadata patches | Keep `ErrInvalidMetadataPatch` for malformed identity or patch content |
| Device/Entity/Command collection queries | Keep `ErrInvalidPage` for invalid page/filter/position combinations |
| Adapter management read | Reject malformed slug with `ValidateAdapterID` before storage |
| Adapter collection/history queries | Keep `ErrInvalidPage` for invalid parent/page/position input; reuse `ValidateAdapterID` for slug shape |
| Function input and Notification input | Keep their existing module-specific invalid-input classes; management reads do not acquire those unrelated classes |

Missing canonical resources retain their not-found identities. Operational and
context errors retain their original chains; do not translate an unknown failure
into invalid input by inspecting text. Direct callers needing a shape-specific
branch can run `ParseDeviceID`, `ParseEntityID`, `ParseCommandID`,
`adapters.ParseRuntimeID` or `adapters.ValidateAdapterID` first. The service still
rechecks safety, and repository checks remain enforced independently.

## Current consumers

- HTTP handlers parse path/parent/filter identities before service calls, and MCP
  typed arguments/SDK schemas validate independently. They retain their intentional
  400/422 and text-only/structured-result differences. This contract creates no
  new public status or code.
- Automation Command reconciliation reads by the validated reserved Command ID;
  missing or operational failures remain unverifiable progress, not a request to
  retry. Its validation-specific Devices seams keep their existing actionable
  `ErrInvalidCommand`/not-found distinctions.
- Devices registration and ownership read Adapter data through their existing
  Adapter-reader seam with validated routing identities. Missing Adapter and
  operational failures retain distinct handling; no new service parse class is
  needed by those consumers.
- Owned enablement's NATS owner validates the wire/route before calling the service.
  The service rechecks Adapter, Runtime and Entity identities. Expected owner,
  missing-resource and fencing failures keep existing response decisions;
  unexpected errors do not gain a new reply policy.

## Verification boundary

Service matrices check malformed IDs never reach storage and canonical IDs retain
missing/operational/context identities. Adapter edge cases use the same public
predicate as repository guards. Existing page, patch, owner-scope, real-SQLite and
registered HTTP/MCP suites retain their own complete matrices. These tests pin a
latent internal consistency contract; they do not establish a current public
endpoint regression, since those entry points already validate identities.
