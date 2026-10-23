# Adapter Functions

An API operator discovers an Adapter's current Function catalog, invokes one
revision-bound operation, and reads its retained result by exact invocation ID.
The native simulator echoes the submitted arguments without vendor hardware.

## Sub-features

- `function.catalog`: discover `inspect` and its current catalog revision.
- `function.invoke`: one valid revision-bound call returns `succeeded`.
- `function.audit`: exact-ID retained arguments and result match the request.

## How to get to it (user POV)

- HTTP `GET /v1/adapters/sim-healthy/functions` and `/functions/inspect`.
- HTTP `POST /v1/adapters/sim-healthy/functions/inspect/invocations`.
- HTTP `GET /v1/adapter-function-invocations/{id}` and Adapter invocation history.
  This map does not claim a browser Function editor or MCP route.

## Driving it with curl

Preconditions: baseline and [shared setup](README.md). The default simulator
registers `inspect`; no config edits or custom provider are needed.

- **Discover and validate the current descriptor:**

  ```sh
  curl -fsS --max-time 2 "$CORE/v1/adapters/sim-healthy/functions" > "$EVIDENCE/functions.json"
  curl -fsS --max-time 2 "$CORE/v1/adapters/sim-healthy/functions/inspect" > "$EVIDENCE/inspect-descriptor.json"
  REVISION=$(jq -er .catalog_revision "$EVIDENCE/inspect-descriptor.json")
  jq -e '.function_key == "inspect" and .callable == true' "$EVIDENCE/inspect-descriptor.json"
  ```

- **Invoke once, recording request and status:**

  ```sh
  jq -n --arg revision "$REVISION" --arg marker "$(basename "$EVIDENCE")" \
    '{catalog_revision:$revision,arguments:{marker:$marker,sequence:42}}' > "$EVIDENCE/function-input.json"
  STATUS=$(curl -sS --max-time 60 -w '%{http_code}' -o "$EVIDENCE/function-response.json" \
    -X POST "$CORE/v1/adapters/sim-healthy/functions/inspect/invocations" \
    -H 'content-type: application/json' --data-binary @"$EVIDENCE/function-input.json")
  printf '%s\n' "$STATUS" > "$EVIDENCE/function-status.txt"
  test "$STATUS" = 200
  INVOCATION=$(jq -er .id "$EVIDENCE/function-response.json")
  ```

- **Read authoritative retained evidence:**

  ```sh
  curl -fsS --max-time 2 "$CORE/v1/adapter-function-invocations/$INVOCATION" > "$EVIDENCE/function-core.json"
  jq -e --arg id "$INVOCATION" --slurpfile request "$EVIDENCE/function-input.json" '
    .id == $id and .adapter_id == "sim-healthy" and .function_key == "inspect" and
    .status == "succeeded" and .catalog_revision == $request[0].catalog_revision and
    .arguments == $request[0].arguments and .result == $request[0].arguments' \
    "$EVIDENCE/function-core.json"
  curl -fsS --max-time 2 "$CORE/v1/adapters/sim-healthy/function-invocations?function_key=inspect&limit=50" \
    > "$EVIDENCE/function-history.json"
  jq -e --arg id "$INVOCATION" 'any(.items[]; .id == $id and .status == "succeeded")' \
    "$EVIDENCE/function-history.json"
  ```

## Gotchas

- Simulator restart changes the catalog revision. A 409 `catalog_changed`
  requires rediscovery, not forced reuse of the old descriptor.
- A lost POST response can hide a completed invocation. Match the unique marker
  in retained history before considering any new call; never blindly resubmit.
- This semantic object comparison is not a byte-preservation or large-number
  proof. Exact-number, held invocation, stale-revision, and other modeled outcomes
  have separate procedures in the existing simulator scenario reference.
- `succeeded` here proves the scripted Adapter boundary, not that a vendor
  refresh produced fresh State or that an external side effect happened.
