---
status: accepted
---

# Scope persistence failures to operations

A write failure can be intermittent. Permanently closing a module's admission or failing readiness until an operator restarts Core can turn a temporary storage problem into an indefinite outage. Fail the affected operation, preserve its evidence boundaries, and permit independent future operations to establish their own durable admission.

Accepted on 2026-10-11 following the Core notifications review.

## Policy

- If an operation's initial record cannot be established, return an error without dispatching external work.
- If its outcome cannot be persisted, return or record a fault without claiming confirmed success or inventing terminal evidence.
- A later independent invocation may proceed only after its own required admission writes succeed. One prior write failure must not permanently close the module's admission.
- An Automation Run whose progress or outcome is uncertain stops advancing. Other Runs remain eligible for admission; unrelated Runs and in-flight device Commands are not cancelled by that Run's fault.
- Never resend a notification, redispatch a Command, resume an uncertain Run, or retry a Step to repair missing evidence. Evidence recovery is separate from external execution.
- Readiness reflects current dependency and worker health. A remembered operation write failure alone is not a permanent readiness failure or a restart requirement.
- Normal shutdown still closes admission and joins work under each module's existing cancellation policy. Required consumer or scheduler termination remains a separate infrastructure failure.

## Consequences

Successful subsequent writes restore operation without an operator restart. Persistent storage failure still prevents every operation whose required writes cannot commit. This policy does not promise delivery or complete history during a storage outage.

Attempt to interrupt only the affected Automation Run in one idempotent database transaction and log the fault. Do not loop, schedule cleanup retries, or busy-wait when that write fails. The user chose a single cleanup attempt over an automatic cleanup retry mechanism.

Previously unfinished evidence may remain unresolved while storage is unavailable. If the scoped interruption write also fails, that Automation can remain busy until startup recovery; other Automations remain eligible. Recovery must preserve terminal outcomes and never replay external work or resume the stopped Run.

This decision supersedes the former permanent persistence-fault admission/readiness latches in Core notifications and Automation execution. Device Commands and Agent turns already treat persistence failures as operation-scoped errors. Notifications now retain admission after write failures; Automations make one scoped interruption attempt without stopping unrelated Runs.

## Rejected alternative

Latching an entire module closed until restart simplifies the failure state, but unnecessarily requires operator intervention after intermittent failures. It can also keep an otherwise recovered Core instance out of service through its readiness endpoint.
