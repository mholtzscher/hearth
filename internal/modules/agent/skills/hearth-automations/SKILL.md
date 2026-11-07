---
name: hearth-automations
description: Show saved Automation cards, or create and edit Hearth Automations with Device event triggers, branching, and Command Steps.
---
# Hearth Automations

## Show a saved Automation

1. Discover the requested Automation with `list_automations`. If its identity is ambiguous, ask which one the user means.
2. Call `present_automation` with `mode: "reference"` and the discovered `automation_id`. Hearth renders its current saved definition as an Automation card in the conversation, with an option to open it in the canvas. The card is separate from the Entity and Device dashboard catalog.

Reference presentation requires only the saved ID. It does not save, enable, or run the Automation.

## Resolve targets before authoring

Discover Devices with `list_devices` and inspect relevant candidates with `get_device` (follow pagination). Match each requested button and target to the user's description.

**If two or more Devices fit any requested target, stop this turn with a clarification question listing their names. Wait for the user's selection before proposing an Automation.** A shared room, compatible capability, or more familiar Device name does not resolve which one the user intended. This phase is complete only when every target is uniquely identified by the request or the user's clarification.

## Author with resolved targets

1. Use the selected Devices' discovered Entities for the following steps.
2. Inspect each referenced Entity's `state.value` with `get_entity`. Use its actual type, support, availability, and State to choose supported event names and Command operations/parameters. For a `hearth.enumevent/v1` button, use `support.events.names`; do not assume the names for single and double presses. For on/off targets, select their `hearth.power/v1` Entities and use their supported `set` operation. Set only power so existing brightness and color settings remain unchanged.
3. Use `present_automation` in create or replace mode to present the proposal. Presentation does not save, enable, or run anything; only the user's explicit browser Save creates or replaces a definition. The example below illustrates routing by trigger ID. Each Command Step has `kind`, `id`, `entity_id`, `operation`, and `parameters`; substitute discovered IDs, event names, and supported parameters.
4. If presentation rejects the definition for schema or reference validation, inspect its diagnostics, correct the definition, and present it again.
5. Tell the user to review and save the proposal in the browser. Authoring does not authorize running it or dispatching Commands to test it.

## Definition and execution checks

- Every Automation Step requires `kind`, including `kind: "command"` for a Command.
- Observation comparison `value_pointer` fields address `state.value` directly: use `value_pointer: ""` for a scalar number, boolean, or string, and an RFC 6901 path for a nested object or array. Never use `/state/value`.
- When a matching observation occurs and follow-up is practical, inspect the Automation's history to verify its execution.

## Example: button routes on and off

This example assumes the user has resolved all targets. The placeholders below are not household IDs. Replace all placeholders with discovered canonical Entity IDs; `single` and `double` are illustrative event names only and must match the button's discovered support.

`present_automation` create-mode input, assuming those event names and boolean power Commands are supported:

```json
{
  "mode": "create",
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
