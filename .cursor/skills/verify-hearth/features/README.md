# Hearth verification map

This directory is the maintained source for verifying Hearth's user-facing HTTP behavior. Read this index first, then drive every entry point named by the selected feature file.

## Baseline preconditions

- Start a fresh `happy` run with `control-hearth launch <run-id> happy`.
- Run `control-hearth doctor <run-id>` and require `doctor: ok`.
- Source `.data/verify-hearth/runs/$RUN_ID/runtime.env`.
- Require one `Simulated light` Device with one enabled `Power` Entity.
- Require the initial State value `false` unless the feature says it mutates State first.
- Never drive an instance that this verification run did not start.

## Driving conventions

- Start each feature from the baseline state unless its preconditions say otherwise.
- Use the canonical `DEVICE_ID` and `ENTITY_ID` from `runtime.env`.
- Send all user actions through `control-hearth request`.
- Give every request a fresh evidence label. The helper does not overwrite proof.
- Treat HTTP method, route, body, status, and JSON field names as literal.
- Do not write the SQLite database or publish NATS messages to create a convenient state.

## Proof and skip reporting

- Capture the action and response, then read the changed resource through another HTTP route.
- A satisfied Command needs an Entity read and a Command-history read.
- A rejected disabled Command needs its Problem Details body and durable Command record.
- Record the feature ID and route used when summarizing evidence.
- Report an unreachable route with the attempted command and unmet precondition.
- Do not claim an untried route through proof from another route.
- Keep `.data/verify-hearth/evidence/$RUN_ID` after cleanup.

## Feature entry contract

Each feature file has one user-visible behavior and exactly four H2 sections in this order:

1. `Sub-features`
2. `How to get to it (user POV)`
3. `Driving it with control-hearth`
4. `Gotchas`

The files name user routes, exact requests, required state, and observable results. Implementation-only checks do not belong in the map.

## Features

- [Discover resources](./discover-resources.md) covers Device and Entity collections, Device detail, and Device-filtered Entity discovery.
- [Inspect Entity State](./inspect-entity-state.md) covers the current value and Observation metadata in detail and collection reads.
- [Control an Entity](./control-entity.md) covers synchronous `set` Commands, State projection, and durable outcome proof.
- [Enable and disable an Entity](./entity-enablement.md) covers management changes, disabled rejection, durable failure history, and recovery after re-enabling.
- [Inspect Command history](./command-history.md) covers direct audit reads, newest-first Entity history, and cursor continuation.
