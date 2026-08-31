# Inspect Command history

Command history lets an API user inspect one durable Command record or page through an Entity's attempts in newest-first order without exposing adapter or correlation identifiers.

## Sub-features

- `history-direct` reads one Command by canonical ID.
- `history-entity` lists Commands for one Entity.
- `history-order` returns newest requests first.
- `history-pagination` continues with an endpoint-scoped cursor.
- `history-public-fields` omits internal adapter and correlation identifiers.

## How to get to it (user POV)

- Send `GET /v1/commands/{command_id}`.
- Send `GET /v1/entities/{entity_id}/commands`.
- Add `limit` and the returned `cursor` to continue Entity history.

## Driving it with control-hearth

Preconditions:

- A fresh `happy` run passes doctor.
- The Entity is enabled.
- No prior Command has run.

- **Create the older record.** Run `"$CONTROL" request "$RUN_ID" history-set-true POST "/v1/entities/$ENTITY_ID/commands" 200 '{"operation":"set","parameters":{"value":true}}'`.
- **Create the newer record.** Run `"$CONTROL" request "$RUN_ID" history-set-false POST "/v1/entities/$ENTITY_ID/commands" 200 '{"operation":"set","parameters":{"value":false}}'`.
- **Load IDs.** Run `OLDER_COMMAND_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["command_id"])' "$EVIDENCE_DIR/requests/history-set-true/response.json")` and `NEWER_COMMAND_ID=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["command_id"])' "$EVIDENCE_DIR/requests/history-set-false/response.json")`.
- **Read directly.** Run `"$CONTROL" request "$RUN_ID" history-direct GET "/v1/commands/$NEWER_COMMAND_ID" 200`. The record has parameters `{"value":false}`, status `satisfied`, completion timestamps, and its outcome Observation ID.
- **Read the first page.** Run `"$CONTROL" request "$RUN_ID" history-page-one GET "/v1/entities/$ENTITY_ID/commands?limit=1" 200`. Its only item is `$NEWER_COMMAND_ID`, and `next_cursor` is present.
- **Continue from the cursor.** Run `HISTORY_CURSOR=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["next_cursor"])' "$EVIDENCE_DIR/requests/history-page-one/response.json")`, then `"$CONTROL" request "$RUN_ID" history-page-two GET "/v1/entities/$ENTITY_ID/commands?limit=1&cursor=$HISTORY_CURSOR" 200`. Its only item is `$OLDER_COMMAND_ID`, and no continuation cursor remains.
- **Check the public record.** Confirm the direct and collection bodies contain no `adapter_id` or `correlation_id` field. They do contain operation parameters and terminal outcome fields.

## Gotchas

- Entity history is newest-first by persisted request time and Command ID, not completion time.
- Cursors are opaque and scoped to this route and Entity. Do not decode, edit, or reuse them for another Entity.
- An unknown Entity returns 404. A known Entity with no Commands returns an empty `items` page.
- Command records are audit history, not executable work. Hearth does not replay unfinished records after restart.
