# Enable and disable an Entity

Entity enablement lets an API user remove an Entity from normal control while retaining its identity, metadata, last accepted State, and durable Command history, then enable it again.

## Sub-features

- `enablement-disable` sets `enabled` to `false`.
- `enablement-retain-state` keeps the last State visible while disabled.
- `enablement-reject-command` returns a durable `entity_disabled` Command failure.
- `enablement-enable` restores normal control.
- `enablement-idempotent` accepts a repeated requested enablement value.

## How to get to it (user POV)

- Send `PATCH /v1/entities/{entity_id}` with `{"enabled":false}` or `{"enabled":true}`.
- Send `GET /v1/entities/{entity_id}` to inspect enablement and retained State.
- Send `POST /v1/entities/{entity_id}/commands` to observe disabled rejection or restored control.
- Send `GET /v1/commands/{command_id}` to inspect the durable rejection.

## Driving it with control-hearth

Preconditions:

- A fresh `happy` run passes doctor.
- The Entity is enabled with initial State value `false`.
- No prior Command has run.

- **Disable the Entity.** Run `"$CONTROL" request "$RUN_ID" enablement-disable PATCH "/v1/entities/$ENTITY_ID" 200 '{"enabled":false}'`. The response has `enabled: false` and retains the existing State.
- **Confirm the read view.** Run `"$CONTROL" request "$RUN_ID" enablement-disabled-read GET "/v1/entities/$ENTITY_ID" 200`. The Entity remains discoverable, disabled, and has the same State Observation ID as the PATCH response.
- **Attempt control.** Run `"$CONTROL" request "$RUN_ID" enablement-rejected POST "/v1/entities/$ENTITY_ID/commands" 409 '{"operation":"set","parameters":{"value":true}}'`. The Problem Details body has `status: 409`, `detail: "entity is disabled"`, `code: "entity_disabled"`, and a `cmd_` Command ID.
- **Read the durable rejection.** Run `DISABLED_COMMAND_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["command_id"])' "$EVIDENCE_DIR/requests/enablement-rejected/response.json")`, then `"$CONTROL" request "$RUN_ID" enablement-rejected-record GET "/v1/commands/$DISABLED_COMMAND_ID" 200`. The record has status `failed` and failure code `entity_disabled`.
- **Enable the Entity.** Run `"$CONTROL" request "$RUN_ID" enablement-enable PATCH "/v1/entities/$ENTITY_ID" 200 '{"enabled":true}'`. The response is enabled and still retains State.
- **Confirm restored control.** Run `"$CONTROL" request "$RUN_ID" enablement-restored-command POST "/v1/entities/$ENTITY_ID/commands" 200 '{"operation":"set","parameters":{"value":true}}'`, then `"$CONTROL" request "$RUN_ID" enablement-restored-read GET "/v1/entities/$ENTITY_ID" 200`. The Command is satisfied and the visible State is `true`.
- **Check idempotence.** Run `"$CONTROL" request "$RUN_ID" enablement-enable-again PATCH "/v1/entities/$ENTITY_ID" 200 '{"enabled":true}'`. It remains enabled without changing identity or State.

## Gotchas

- Disablement is not deletion or temporary adapter unavailability. The Entity and last State remain readable.
- The rejected Command is still durable. A 409 response without the matching Command record is incomplete proof.
- Re-enabling does not synthesize a State change. State changes only after a valid Observation.
- A PATCH body with a missing, null, string, or extra `enabled` field returns HTTP 422 through Huma validation.
