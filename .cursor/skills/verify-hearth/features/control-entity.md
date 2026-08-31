# Control an Entity

Entity control sends a supported `set` Command, waits for a linked matching Observation, returns the satisfied outcome, projects State, and records the attempt for audit.

## Sub-features

- `control-set` sends the power `set` operation.
- `control-linked-outcome` returns a satisfied Command only after a linked Observation.
- `control-state-projection` exposes the observed value through the Entity route.
- `control-durable-record` exposes the same outcome through Command history.
- `control-no-short-circuit` dispatches even when State already matches the requested value.

## How to get to it (user POV)

- Send `POST /v1/entities/{entity_id}/commands` with `{"operation":"set","parameters":{"value":true}}` or `false`.
- Send `GET /v1/entities/{entity_id}` to inspect the resulting State.
- Send `GET /v1/commands/{command_id}` or `GET /v1/entities/{entity_id}/commands` to inspect the durable attempt.

## Driving it with control-hearth

Preconditions:

- A fresh `happy` run passes doctor.
- The Entity is enabled and its initial State value is `false`.
- The simulator Command subscription is active.

- **Capture initial State.** Run `"$CONTROL" request "$RUN_ID" control-before GET "/v1/entities/$ENTITY_ID" 200`. Save its `state.observation_id` for comparison.
- **Set power true.** Run `"$CONTROL" request "$RUN_ID" control-set-true POST "/v1/entities/$ENTITY_ID/commands" 200 '{"operation":"set","parameters":{"value":true}}'`. The body has `status: "satisfied"`, `value: true`, a `cmd_` Command ID, and an `obs_` Observation ID.
- **Load outcome IDs.** Run `read -r COMMAND_ID OUTCOME_OBSERVATION_ID < <(python3 -c 'import json,sys; x=json.load(open(sys.argv[1])); print(x["command_id"], x["observation_id"])' "$EVIDENCE_DIR/requests/control-set-true/response.json")`.
- **Confirm projected State.** Run `"$CONTROL" request "$RUN_ID" control-after GET "/v1/entities/$ENTITY_ID" 200`. The State value is `true`; its Observation ID equals `$OUTCOME_OBSERVATION_ID` and differs from the initial Observation ID.
- **Confirm durable outcome.** Run `"$CONTROL" request "$RUN_ID" control-record GET "/v1/commands/$COMMAND_ID" 200`. The record has the same Entity ID, operation `set`, parameters `{"value":true}`, status `satisfied`, and outcome Observation ID.
- **Prove no short-circuit.** Send the same target again with `"$CONTROL" request "$RUN_ID" control-set-true-again POST "/v1/entities/$ENTITY_ID/commands" 200 '{"operation":"set","parameters":{"value":true}}'`. It returns new Command and Observation IDs even though the prior State was already `true`.

## Gotchas

- HTTP 200 means the linked outcome was observed. Adapter acceptance alone is not success.
- A Command always dispatches, even when current State already matches the target.
- The first power `set` operation has a ten-second end-to-end deadline. Do not replace route readiness checks with a fixed sleep.
- Missing adapter, upstream rejection, outcome timeout, and disabled Entity are distinct HTTP outcomes. Use their matching simulator or enablement recipe instead of weakening the expected status.
