# Inspect Entity State

Entity State reads show Hearth's latest accepted Observation value and its adapter and core timestamps without presenting requested or desired state.

## Sub-features

- `state-detail` reads the current State from the canonical Entity route.
- `state-collection` returns the same State in Entity discovery.
- `state-observation-metadata` exposes Observation identity and receive timestamps.
- `state-stable-read` leaves State unchanged when no new Observation arrives.

## How to get to it (user POV)

- Send `GET /v1/entities/{entity_id}`.
- Send `GET /v1/entities` and inspect the matching item.
- Send `GET /v1/devices/{device_id}` and inspect the embedded Entity.

## Driving it with control-hearth

Preconditions:

- A fresh `happy` run passes doctor.
- The simulator's initial Observation has been accepted.
- No Command or enablement change has run.

- **Read Entity detail.** Run `"$CONTROL" request "$RUN_ID" state-detail GET "/v1/entities/$ENTITY_ID" 200`. The Entity is enabled and `state.value` is `false`.
- **Check Observation metadata.** In `state-detail/response.json`, require `state.observation_id` to start with `obs_`; require UTC `adapter_received_at` and `observed_at`; allow `source_updated_at` to be absent in the `happy` scenario.
- **Read the collection view.** Run `"$CONTROL" request "$RUN_ID" state-collection GET "/v1/entities" 200`. The matching item's complete State equals the detail State.
- **Read the embedded view.** Run `"$CONTROL" request "$RUN_ID" state-device GET "/v1/devices/$DEVICE_ID" 200`. The embedded Entity's State equals the detail State.
- **Prove no read mutation.** Run `"$CONTROL" request "$RUN_ID" state-detail-again GET "/v1/entities/$ENTITY_ID" 200`. The second detail read has the same value and Observation ID as the first.

## Gotchas

- `state` may be `null` for a registered Entity with no accepted Observation. Launch waits for initial State in `happy`; the `malformed` scenario intentionally does not.
- `observed_at` is core receive time. `adapter_received_at` comes from the adapter. Do not require them to be equal.
- State is accepted Observation evidence, not a requested target and not proof of physical causation.
- The `future-clock-skew` and `delayed-source-time` scenarios deliberately change timestamp relationships.
