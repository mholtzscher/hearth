# Notifications

An operator sends one test message to a discovered destination and inspects its
durable attempt. A simulated provider receipt can independently confirm the
payload; `accepted` does not establish Discord or physical delivery.

## Sub-features

- `notification.browser`: destination detail sends one message through the UI.
- `notification.http`: production manual-send API records one attempt.
- `notification.audit`: history and simulator receipt match the exact attempt.

## How to get to it (user POV)

- **Destinations** → **Simulated accepted notification** → **Send test message**.
- **Inspect attempt <id>**, **History for this destination**, or **Notifications**.
- HTTP `POST /v1/notification-destinations/{id}/notifications` and
  `GET /v1/notifications/{id}`.

## Driving it with agent-browser and curl

Preconditions: baseline and [shared setup](README.md). Use only the native
`sim-healthy` accepted destination, without behavior overrides. No webhook,
secret file, physical provider, or Automation is required.

- **Discover:**

  ```sh
  curl -fsS --max-time 2 "$SIM/v1/sim/notifications/destinations" > "$EVIDENCE/destinations.json"
  DESTINATION=$(jq -er '[.[] | select(.adapter_id == "sim-healthy" and .key == "accepted")]
    | if length == 1 then .[0].destination_id else error("expected one destination") end' \
    "$EVIDENCE/destinations.json")
  curl -fsS --max-time 2 "$CORE/v1/notification-destinations/$DESTINATION" > "$EVIDENCE/destination-core.json"
  jq -e '.available == true' "$EVIDENCE/destination-core.json"
  MESSAGE="verify-hearth $(basename "$EVIDENCE")"
  ```

- **Dashboard action:**

  ```sh
  agent-browser find role link click --name Destinations --exact
  agent-browser wait --text 'Simulated accepted notification'
  agent-browser find role link click --name 'Simulated accepted notification' --exact
  agent-browser wait --url "**/notification-destinations/$DESTINATION"
  agent-browser wait '#notification-message'
  agent-browser find label 'Title (optional, up to 256 characters)' fill 'Hearth verification'
  agent-browser find label 'Message (required, up to 2000 characters)' fill "$MESSAGE"
  agent-browser snapshot > "$EVIDENCE/notification-before.txt"
  agent-browser screenshot "$EVIDENCE/notification-before.png"
  agent-browser find role button click --name 'Send test message' --exact
  agent-browser wait --text 'Inspect attempt'
  agent-browser snapshot > "$EVIDENCE/notification-result.txt"
  ```

  Read `ATTEMPT` from the **Inspect attempt** link, then follow that link and
  save the resulting history detail snapshot/screenshot. Require `accepted`,
  exact destination, title, and message. Other history entry points require
  their own interactions if claimed.
- **Durable result and independent receive evidence:**

  ```sh
  curl -fsS --max-time 2 "$CORE/v1/notifications/$ATTEMPT" > "$EVIDENCE/notification-core.json"
  jq -e --arg id "$ATTEMPT" --arg destination "$DESTINATION" --arg message "$MESSAGE" '
    .id == $id and .destination_id == $destination and .status == "accepted" and
    .title == "Hearth verification" and .message == $message' "$EVIDENCE/notification-core.json"
  curl -fsS --max-time 2 "$SIM/v1/sim/notifications/requests?adapter_id=sim-healthy&after_sequence=0&limit=1000" \
    > "$EVIDENCE/notification-captures.json"
  jq -e --arg id "$ATTEMPT" --arg message "$MESSAGE" 'any(.items[];
    .attempt_id == $id and .message.Title == "Hearth verification" and .message.Text == $message)' \
    "$EVIDENCE/notification-captures.json"
  ```

- **Alternate HTTP sender:** use a different message marker, save a body with
  `title` and `message` using `jq -n`, and issue one request:

  ```sh
  jq -n --arg message "$MESSAGE" '{title:"Hearth verification",message:$message}' > "$EVIDENCE/notification-input.json"
  STATUS=$(curl -sS --max-time 60 -w '%{http_code}' -o "$EVIDENCE/notification-http.json" \
    -X POST "$CORE/v1/notification-destinations/$DESTINATION/notifications" \
    -H 'content-type: application/json' --data-binary @"$EVIDENCE/notification-input.json")
  printf '%s\n' "$STATUS" > "$EVIDENCE/notification-http-status.txt"
  test "$STATUS" = 201
  ATTEMPT=$(jq -er .id "$EVIDENCE/notification-http.json")
  ```

  Repeat the exact-ID checks with separate artifact names. This is an additional
  send, not a way to fetch the browser action's receipt.

## Gotchas

- Every click/send creates a new attempt. Unknown or transport failure is
  ambiguous; inspect history rather than retrying to obtain a nicer result.
- Core uses lowercase `title`/`message`; SDK capture uses `message.Title` and
  `message.Text`. Simulator receipt is not durable provider acceptance.
- If captures are paginated, follow `has_more` using the per-Adapter sequence.
  Missing/evicted captures cannot prove no send. See the existing simulator
  control reference for retention metadata and absence/count experiments.
- Held/rejected/not-sent/unknown destinations are separate recipes, not expected
  failures of this accepted path. Native Automation notification scenarios are
  available in the existing simulator skill and are outside this initial proof.
