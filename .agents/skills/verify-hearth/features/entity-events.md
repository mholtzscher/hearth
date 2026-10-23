# Entity Events and Core-offline recovery

An operator can inspect named occurrences without confusing them with State.
Broker-acknowledged simulator reports survive a Core-only outage and are
recorded once by exact event ID after Core returns.

## Sub-features

- `events.history`: accepted event evidence has the reported name and exact ID.
- `events.stateless`: event-source State remains null and has no Command sender.
- `events.recovery`: a report acknowledged while Core is stopped is recovered.

## How to get to it (user POV)

- **Devices** → **Simulated button** → **Events**, or Entity jump/direct detail.
- Entity detail → **Entity events**; HTTP `GET /v1/entities/{id}/events`.
- Outage stimulus uses the simulator's loopback publish API; Core is managed
  through the same worktree's Mise daemon names.

## Driving it with agent-browser and curl

Preconditions: baseline and [shared setup](README.md); this run owns Core,
NATS, and simulator. Recovery intentionally interrupts Core, not the broker or
the simulator. No physical button or Automation is required.

- **Pause and optionally stop Core:**

  ```sh
  EVENTS=$(entity_id sim-healthy simulated-button events)
  curl -fsS --max-time 2 -X POST "$SIM/v1/sim/entities/$EVENTS/pause" > "$EVIDENCE/events-paused.json"
  # Run this line only for events.recovery, not the online-history variant.
  mise daemons stop sim-core > "$EVIDENCE/core-outage.log" 2>&1
  mise daemons ls --json > "$EVIDENCE/daemons-outage.json"
  ```

  For recovery, confirm Core's PID is gone and NATS/simulator PIDs are still
  alive before publishing. Readiness alone does not establish an outage.
- **Publish once while Core is offline:**

  ```sh
  printf '%s\n' '{"name":"double_press"}' > "$EVIDENCE/event-input.json"
  curl -fsS --max-time 2 -X POST "$SIM/v1/sim/entities/$EVENTS/publish" \
    -H 'content-type: application/json' --data-binary @"$EVIDENCE/event-input.json" \
    > "$EVIDENCE/event-published.json"
  EVENT=$(jq -er .event_id "$EVIDENCE/event-published.json")
  mise daemons start sim-core > "$EVIDENCE/core-return.log" 2>&1
  ```

  Skip the restart line for the online-history variant. Rerun Doctor after
  restart against the same SQLite/NATS paths, preserving the outage artifacts.
- **Assert recovered evidence separately from readiness:**

  ```sh
  wait_core "/v1/entities/$EVENTS/events?limit=50" "$EVIDENCE/event-history.json" \
    '[.items[] | select(.event_id == $id and .name == "double_press" and
      .disposition == "accepted")] | length == 1' --arg id "$EVENT"
  curl -fsS --max-time 2 "$CORE/v1/entities/$EVENTS" > "$EVIDENCE/event-entity.json"
  jq -e 'has("state") and .state == null' "$EVIDENCE/event-entity.json"
  agent-browser open "$WEB/#/entities/$EVENTS"
  agent-browser wait --text "$EVENT"
  agent-browser snapshot > "$EVIDENCE/event-detail.txt"
  agent-browser screenshot "$EVIDENCE/event-detail.png"
  ```

  Require **Event source**, supported names, the exact event in history, and no
  sender. For the Device navigation route, select **Simulated button**, then
  the **Events** link and save a separate snapshot.

## Gotchas

- Publish success proves broker acknowledgment, not Core acceptance. Reject
  dispositions are evidence of a failed expectation, not a recovered success.
- Core readiness does not wait for backlog. Poll the exact event ID, following
  history cursors when needed; matching only `double_press` can find an old event.
- This proves a managed Core-only restart and bounded backlog, not NATS loss,
  simulator restart during outage, stream-retention exhaustion, or physical input.
- A failed outage attempt still needs final stack cleanup. Restart only this
  run's Core for diagnosis; preserve the same database rather than reseeding.
