---
name: hearth-automations
description: Create or edit Hearth Automations, including Device event triggers, branching, and Command Steps.
---
# Hearth Automations

## Resolve targets before authoring

Discover Devices with `list_devices` and inspect relevant candidates with `get_device` (follow pagination). Match each requested button and target to the user's description.

**If two or more Devices fit any requested target, stop this turn with a clarification question listing their names. Wait for the user's selection before calling `create_automation` or `replace_automation`.** A shared room, compatible capability, or more familiar Device name does not resolve which one the user intended. This phase is complete only when every target is uniquely identified by the request or the user's clarification.

## Author with resolved targets

1. Use the selected Devices' discovered Entities for the following steps.
2. Inspect each referenced Entity's `state.value` with `get_entity`. Use its actual type, support, availability, and State to choose supported event names and Command operations/parameters. For a `hearth.enumevent/v1` button, use `support.events.names`; do not assume the names for single and double presses. For on/off targets, select their `hearth.power/v1` Entities and use their supported `set` operation. Set only power so existing brightness and color settings remain unchanged.
3. Create or replace the Automation using the published tool schema. The example below illustrates routing by trigger ID. Each Command Step has `kind`, `id`, `entity_id`, `operation`, and `parameters`; the example assumes discovery confirms a boolean `set` with a `value` parameter for each power Entity. Substitute discovered IDs, event names, and supported parameters. A `choose` branch has `conditions` and `steps`; an `if` Step instead has `conditions` and `then` (and optionally `else`). A trigger condition matches `trigger_ids`, not event names.
4. If the tool explicitly rejects the definition for schema or reference validation, inspect its diagnostics, correct the definition, and submit the corrected definition. A timeout or uncertain write is not a validation rejection: inspect the current Automation state or report the uncertainty rather than blindly retrying a potentially completed write.
5. After a successful save, call `get_automation` with the returned ID. Check the saved enabled state, triggers, selected Entities, branch routing, and parameters against the request, and report the result. Authoring an Automation does not authorize running it or dispatching Commands to test it.

## Definition and execution checks

- Every Automation Step requires `kind`, including `kind: "command"` for a Command.
- Observation comparison `value_pointer` fields address `state.value` directly: use `value_pointer: ""` for a scalar number, boolean, or string, and an RFC 6901 path for a nested object or array. Never use `/state/value`.
- When a matching observation occurs and follow-up is practical, inspect the Automation's history to verify its execution.

## Example: button routes on and off

This example assumes the user has resolved all targets. The placeholders below are not household IDs. Replace all placeholders with discovered canonical Entity IDs; `single` and `double` are illustrative event names only and must match the button's discovered support.

`create_automation` input, assuming those event names and boolean power Commands are supported:

```json
{
  "definition": {
    "name": "Button fan and lamps",
    "enabled": true,
    "triggers": [
      {"id":"single_press","kind":"entity_event","entity_id":"<BUTTON_ENTITY_ID>","event_name":"single"},
      {"id":"double_press","kind":"entity_event","entity_id":"<BUTTON_ENTITY_ID>","event_name":"double"}
    ],
    "steps": [{
      "id":"route","kind":"choose","branches":[
        {"id":"on","conditions":{"id":"single_match","kind":"trigger","trigger_ids":["single_press"]},"steps":[
          {"id":"fan_on","kind":"command","entity_id":"<FAN_POWER_ENTITY_ID>","operation":"set","parameters":{"value":true}},
          {"id":"lamp_1_on","kind":"command","entity_id":"<LAMP_1_POWER_ENTITY_ID>","operation":"set","parameters":{"value":true}},
          {"id":"lamp_2_on","kind":"command","entity_id":"<LAMP_2_POWER_ENTITY_ID>","operation":"set","parameters":{"value":true}}
        ]},
        {"id":"off","conditions":{"id":"double_match","kind":"trigger","trigger_ids":["double_press"]},"steps":[
          {"id":"fan_off","kind":"command","entity_id":"<FAN_POWER_ENTITY_ID>","operation":"set","parameters":{"value":false}},
          {"id":"lamp_1_off","kind":"command","entity_id":"<LAMP_1_POWER_ENTITY_ID>","operation":"set","parameters":{"value":false}},
          {"id":"lamp_2_off","kind":"command","entity_id":"<LAMP_2_POWER_ENTITY_ID>","operation":"set","parameters":{"value":false}}
        ]}
      ]
    }]
  }
}
```
