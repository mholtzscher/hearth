# Commands

An operator requests a typed power change and can audit its durable Command
outcome independently of the sender. `satisfied` requires matching evidence;
mere acceptance or a changed value does not prove this Command completed.

## Sub-features

- `command.browser`: preset fills the dashboard sender; one click sends once.
- `command.http`: production HTTP submission returns a Command receipt.
- `command.audit`: exact-ID detail, Entity history, and State link agree.

## How to get to it (user POV)

- Device/Entity navigation or jump → Power detail → **power on** → **Send command**.
- HTTP `POST /v1/entities/{id}/commands`.
- **Commands** → `command_id` → **Look up**, or `#/commands?command_id=<id>`.
- Entity detail → **Command history** → **Refresh history**; HTTP
  `GET /v1/commands/{id}` and `/v1/entities/{id}/commands`.

## Driving it with agent-browser and curl

Preconditions: baseline and [shared setup](README.md). This bounded path uses
only the `sim-healthy` power Entity. Other fault Adapters are separate scenarios.

- **Establish a deterministic false baseline:**

  ```sh
  POWER=$(entity_id sim-healthy simulated-light power)
  curl -fsS --max-time 2 -X POST "$SIM/v1/sim/entities/$POWER/pause" > "$EVIDENCE/power-paused.json"
  printf '%s\n' '{"value":false}' > "$EVIDENCE/power-baseline-input.json"
  curl -fsS --max-time 2 -X POST "$SIM/v1/sim/entities/$POWER/publish" \
    -H 'content-type: application/json' --data-binary @"$EVIDENCE/power-baseline-input.json" \
    > "$EVIDENCE/power-baseline-published.json"
  BASELINE=$(jq -er .observation_id "$EVIDENCE/power-baseline-published.json")
  wait_core "/v1/entities/$POWER" "$EVIDENCE/power-before.json" \
    '.state.observation_id == $id and .state.value == false' --arg id "$BASELINE"
  ```

- **Dashboard submission:**

  ```sh
  agent-browser find label 'Jump to entity id' fill "$POWER"
  agent-browser press Enter
  agent-browser wait --url "**/entities/$POWER"
  agent-browser wait --text 'power on'
  agent-browser find role button click --name 'power on' --exact
  agent-browser snapshot > "$EVIDENCE/command-before.txt"
  agent-browser screenshot "$EVIDENCE/command-before.png"
  agent-browser find role button click --name 'Send command' --exact
  agent-browser wait --text satisfied
  agent-browser find role button click --name 'Command result JSON' --exact
  agent-browser snapshot > "$EVIDENCE/command-result.txt"
  agent-browser screenshot "$EVIDENCE/command-result.png"
  ```

  Read the full `command_id` from **Command result JSON**; set `COMMAND` to that
  observed ID in the shell. Keep the result snapshot linking it to this click.
  Do not select the newest household Command as a substitute for the receipt.
  The UI timeout is normally 25 seconds; this healthy path should complete
  within it. On timeout inspect history, not another click.

- **Durable outcome and linked State:**

  ```sh
  curl -fsS --max-time 2 "$CORE/v1/commands/$COMMAND" > "$EVIDENCE/command-core.json"
  jq -e --arg id "$COMMAND" --arg entity "$POWER" '
    .id == $id and .entity_id == $entity and .operation == "set" and
    .parameters.value == true and .status == "satisfied" and
    (.outcome_observation_id | startswith("obs_"))' "$EVIDENCE/command-core.json"
  OUTCOME=$(jq -er .outcome_observation_id "$EVIDENCE/command-core.json")
  wait_core "/v1/entities/$POWER" "$EVIDENCE/power-after.json" \
    '.state.value == true and .state.observation_id == $id' --arg id "$OUTCOME"
  wait_core "/v1/entities/$POWER/commands?limit=50" "$EVIDENCE/power-commands.json" \
    'any(.items[]; .id == $id and .status == "satisfied")' --arg id "$COMMAND"
  wait_core "/v1/entities/$POWER/state/history?limit=50" "$EVIDENCE/power-observations.json" \
    'any(.items[]; .observation_id == $id and .value == true and .disposition == "applied")' \
    --arg id "$OUTCOME"
  agent-browser find role link click --name Commands --exact
  agent-browser wait 'input[aria-label="command_id"]'
  agent-browser find label command_id fill "$COMMAND"
  agent-browser find role button click --name 'Look up' --exact
  agent-browser wait --text 'Satisfied by observation'
  agent-browser snapshot > "$EVIDENCE/command-audit.txt"
  agent-browser screenshot "$EVIDENCE/command-audit.png"
  ```

  Check exact Command/Entity/Observation IDs on the audit screen, then reload
  the shareable URL and verify the same detail. The separate Entity-history
  button route needs its own interaction if claimed as browser coverage.

- **Alternate HTTP sender:** this is a separate mutation, not part of browser
  proof. Re-establish the false baseline above first. Save
  `{"operation":"set","parameters":{"value":true}}` as
  `$EVIDENCE/command-input.json`, then:

  ```sh
  STATUS=$(curl -sS --max-time 60 -w '%{http_code}' -o "$EVIDENCE/command-http.json" \
    -X POST "$CORE/v1/entities/$POWER/commands" -H 'content-type: application/json' \
    --data-binary @"$EVIDENCE/command-input.json")
  printf '%s\n' "$STATUS" > "$EVIDENCE/command-http-status.txt"
  test "$STATUS" = 200
  COMMAND=$(jq -er .command_id "$EVIDENCE/command-http.json")
  ```

  Repeat durable checks with this ID, keeping separate evidence filenames for
  the two entry points.

## Gotchas

- A preset only fills parameters; it does not send. **Send command** is the
  mutation. Avoid duplicate clicks when the response is slow or lost.
- Pause scripted output: unrelated Observations can change State mid-proof.
  Establish false before requesting true so changed-State evidence is meaningful.
- Stateless effects finish `dispatched`, not `satisfied`, and State remains null.
  The preset's generic parameter application does not implement XY/HS changes.
- Never use discovered physical Entities for this recipe. Missing healthy power
  support or an unhealthy route is a setup/product failure to report.
