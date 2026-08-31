# Discover resources

Resource discovery lets an API user list Hearth Devices and Entities, filter Entities by Device, and inspect a Device with its Entity bodies and current State.

## Sub-features

- `discovery-list-devices` lists canonical Devices.
- `discovery-list-entities` lists canonical Entities with State.
- `discovery-filter-entities` limits Entity discovery to one Device.
- `discovery-device-detail` embeds the Device's Entity page.

## How to get to it (user POV)

- Send `GET /v1/devices`.
- Send `GET /v1/entities`.
- Send `GET /v1/entities?device_id={device_id}`.
- Send `GET /v1/devices/{device_id}`.

## Driving it with control-hearth

Preconditions:

- A fresh `happy` run passes doctor.
- `DEVICE_ID` and `ENTITY_ID` are loaded from `runtime.env`.
- No mutation request has run.

- **List Devices.** Run `"$CONTROL" request "$RUN_ID" discovery-devices GET "/v1/devices" 200`. The response has one item named `Simulated light`, kind `light`, and ID `$DEVICE_ID`.
- **List Entities.** Run `"$CONTROL" request "$RUN_ID" discovery-entities GET "/v1/entities" 200`. The response has one enabled item named `Power`, type `hearth.power/v1`, ID `$ENTITY_ID`, and `device_id` `$DEVICE_ID`.
- **Filter by Device.** Run `"$CONTROL" request "$RUN_ID" discovery-filtered GET "/v1/entities?device_id=$DEVICE_ID" 200`. The response has the same Entity and no unrelated item.
- **Read Device detail.** Run `"$CONTROL" request "$RUN_ID" discovery-device-detail GET "/v1/devices/$DEVICE_ID" 200`. The top-level Device fields match the collection item and `entities[0]` matches `$ENTITY_ID` with its current State.
- **Proof.** Compare all four response files under `$EVIDENCE_DIR/requests/`. The Device ID, Entity ID, name, type, support, enabled value, and State agree across routes.

## Gotchas

- Huma adds a `$schema` field to responses. Assert the documented fields rather than exact top-level equality.
- Collection responses use `items` and omit `next_cursor` on the last page. They do not return totals.
- Device detail uses `entities` and `next_entity_cursor`, not the collection's field names.
- A valid but unknown `device_id` filter returns an empty Entity page. It does not prove that the known Device contains the expected Entity.
