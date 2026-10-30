# Persistence error contracts

Notifications and Adapter Functions use module-owned persistence identities for
specific workflow decisions, **not** as a universal wrapper around every database
call. Their absence does not prove an error is unrelated to storage. Preserve the
operation stage and original chain; unknown failures still select a safe internal
fallback, never permission to retry effects.

## Producers

| Boundary | Notifications | Adapter Functions |
| --- | --- | --- |
| Constructor recovery | Joins `ErrPersistence` with the recovery cause | Joins `ErrFunctionPersistence` with the recovery cause |
| Registration/catalog write | Service passes repository result through | Service passes result through; SQLite catalog persistence adds `ErrFunctionPersistence` |
| Management/history reads | Raw operational/context errors pass through; stored destination/attempt corruption retains `ErrPersistence` with a private cause | Raw operational/context errors pass through; unusable retained catalogs/descriptors/invocations retain `ErrFunctionPersistence` |
| Initial effect admission | Known invalid input/missing destination stay domain outcomes; other failures join `ErrPersistence`, including reserved attempt-ID collisions | Lookup, preparation and admission failures retain existing identities; raw database/context failures do not automatically gain `ErrFunctionPersistence` |
| Terminal write | Joins `ErrPersistence` with the original cause, including completion conflict | `InvocationError` retains the committed Invocation ID and joins `ErrFunctionPersistence` with the original cause |

Read and registration failures do not need a universal persistence class: current
consumers preserve expected domain failures and use their existing internal
fallback otherwise. Do not wrap every repository call merely to make the table
symmetric, or flatten a context, identity conflict, missing resource or private
corruption cause into text.

## Load-bearing consumers

| Consumer | Required distinction |
| --- | --- |
| Function HTTP/MCP mappings | `ErrFunctionPersistence` outranks joined client classes; optional committed Invocation identity remains in the safe internal result |
| Function Step reconciliation | Persistence-classified Invocation failure cannot be adopted as success; raw admission faults still require authoritative evidence |
| Notification HTTP diagnostics | Persistence stays diagnostic after caller cancellation; an ended owner's exact single-cause cancellation remains silent |
| Notification Step reconciliation | Attempt-ID, input and completion conflicts prohibit adopting an existing row; missing evidence plus persistence is not a truthful no-attempt rejection |
| Notification Step authoritative reread | An ordinary ambiguous Send error can resolve through matching retained terminal evidence; persistence does not universally forbid that reconciliation |

Notifications and Functions intentionally have different authoritative-evidence
rules. A shared classifier must not erase them. Protocols retain their own status
and code tables; this document creates no mapping or retry policy.

## Conflicts and context

- Reserved Notification identity collision remains `ErrAttemptIDConflict`
  alongside `ErrPersistence`. Matching message bytes authorize neither adopting
  the old attempt nor sending again.
- Conflicting completion remains `ErrCompletionConflict` alongside `ErrPersistence`
  at Send. [Exact duplicates](notifications.md#completion-conflicts) are distinct.
  Neither Send nor an Automation Step adopts conflicting terminal evidence.
- Reserved Function identity collision remains `ErrInvocationIDConflict`, not
  an idempotency key. Its caller must neither redispatch nor adopt an old Invocation.
- Joined errors preserve their members for `errors.Is`/`errors.As`. A persistence
  fault joined with cancellation is more than ordinary caller cancellation and
  remains diagnostic at the existing owner.
- Dependency cancellation with an active caller does not prove the request ended.
  Existing detached effect lifetimes remain; no generic deadline-to-504 or
  cancellation-to-retry policy is implied.

## Durable guarantees and validation

An initial write failure never dispatches. Terminal write failure never invents
success: an uncommitted Notification outcome remains unresolved for startup
recovery; Function errors retain the committed identity. Terminal conflicts instead
preserve established terminal rows. Independent future operations may proceed when
their own writes succeed. No sticky admission/readiness failure, automatic retry,
resend, or replay follows from these classes.

Stage-scoped service matrices pin raw versus classified failures, context identity,
dispatch counts and terminal Invocation identity. Real SQLite suites inject
initial/terminal faults, verify absent or retained rows, restart without replay and
reject reserved collisions. Protocol and Automation owner matrices pin precedence
and reconciliation distinctions. Synthetic service faults characterize the seam;
they are not database-outage reproductions. See [testing](testing.md).
