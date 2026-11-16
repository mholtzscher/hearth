# Scripted fault injection (legacy simulator replacement)

Goal: express every legacy `scenario:` behavior as scripted Devices plus
Go-harness faults, then delete `internal/adapters/simulator` and the
scenario config path. See `specs/simulator-harness.md` for the base model.

## New scripted YAML surface

Device-level (`DeviceSpec`):

- `omit_availability_when_unhealthy: true` — when the Device health parses to
  unhealthy, `Initialize` reports health but skips Entity availability reports
  for that Device, so Core materializes effective `unavailable` (legacy
  `adapter-unhealthy` returned before any availability report). Validation:
  the flag requires health that parses to unhealthy, else config load fails.
  Inert-flag misconfiguration is rejected, not silently kept.

Entity-level (`EntitySpec`):

- `source_time_offset: -24h` — shifts `SourceUpdatedAt` to now plus offset on
  every Observation for the Entity. Unset leaves `SourceUpdatedAt` zero, as
  today. Any `time.ParseDuration` value, negative included.
- `received_time_offset: +2m` — shifts `AdapterReceivedAt` to now plus offset
  on every Observation for the Entity. Unset means now, as today.
- Offsets apply uniformly to initial, ticker, and command-linked
  Observations. Legacy skewed only the initial Observation, but no scenario
  reads later ones, and uniform application keeps one code path.

Command-level (`CommandBehavior`):

- `mark_available: true` — when the Command is accepted (any accept
  behavior), re-report the Entity available before applying parameters and
  accepting (legacy `entity-unavailable` repaired availability on dispatch,
  before applying). Validation: rejected with the `reject` behavior, where it
  would be silently inert. Mirror legacy error handling for a failed
  re-report.

## Changed defaults (no flags)

- `Initialize` skips the initial publication for Entities marked
  `available: false`. An unavailable Entity has no known State; publishing its
  configured `initial` materializes a State row the legacy path never wrote
  (legacy `entity-unavailable` publishes nothing until the repairing
  Command). Nil or true `available` still publishes exactly once.

## What stays Go-harness-only (never YAML)

These are Core/test faults, not Device behavior; the migrating tests keep
their existing mechanisms and only swap adapter construction for scripted
Devices:

- Command interrupt mid-lifecycle (`InterruptActiveCommands`).
- Restart-before-ack redelivery (failing projector + `ackWait`).
- Raw wire `duplicate` / `malformed` injection (JetStream-level publishes).
- Dependency `Now` skew for outcome deadlines.
- Lease expiry, fencing takeover, graceful release (Session-level flows).

No ack-delay or ack-drop knob: no legacy scenario delays an ack, and
accept-without-outcome already exists as `accept-no-publish`.

## Migration map (legacy scenario -> scripted)

| Legacy scenario              | Scripted Devices                                         |
| ---------------------------- | -------------------------------------------------------- |
| happy                        | power Device, default `accept-and-publish`               |
| adapter-unhealthy            | `health: "unhealthy:hearth.external_system_unavailable"` + `omit_availability_when_unhealthy: true` |
| entity-unavailable           | `available: false` + `availability_reason: adapter.hearth-simulator.entity_unavailable`; `set` with `mark_available: true` |
| upstream-rejection           | `set: {behavior: reject, reason: "simulated upstream rejection"}` |
| outcome-timeout              | `set: {behavior: accept-no-publish}`                     |
| no-op-refresh                | `initial: false`, no outputs, default behavior           |
| overlapping-opposite-command | default behavior (runtime mutex + per-publish IDs)       |
| interrupted-command          | `accept-no-publish` adapter-side; interrupt stays in test |
| delayed-source-time          | `source_time_offset: -24h`                               |
| future-clock-skew            | `received_time_offset: +2m`                              |
| restart-before-ack           | default behavior adapter-side; redelivery stays in test  |
| entity-events                | `hearth.enumevent/v1` Entity with both names; emit via control publish (stdin loop deleted, not ported) |

External IDs are unchanged: scripted builds `binding_key + "." + entity key`,
identical to the legacy `.power` / `.events` suffixing, so test assertions on
IDs, Device layout, and support carry over untouched.
