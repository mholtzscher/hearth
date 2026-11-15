import { http, HttpResponse } from "msw";
import type { Automation, AutomationDefinition } from "../src/api/types";
import { entityId } from "./fixtures";

/** Synthetic saved definitions; these do not execute or establish device effects. */
export const automationId = "aut_01920000-0000-7000-8000-000000000001";
export const childAutomationId = "aut_01920000-0000-7000-8000-000000000002";
const destinationId = "ndst_01920000-0000-7000-8000-000000000003";
export const definition: AutomationDefinition = {
  name: "Hallway light at dusk",
  enabled: true,
  triggers: [{ id: "evening", kind: "cron", expression: "0 18 * * *" }],
  conditions: {
    id: "admission", kind: "all", children: [
      { id: "power", kind: "entity_state", entity_id: entityId(4), value_pointer: "", operator: "eq", operand: false },
    ],
  },
  variables: { quiet: true, previous: null },
  steps: [
    { id: "switch", kind: "command", entity_id: entityId(4), operation: "set", parameters: { value: true } },
    { id: "route", kind: "if", conditions: { id: "ready", kind: "expression", expr: "vars.quiet" },
      then: [{ id: "wait", kind: "delay", duration_ms: 1000 }] },
    { id: "notify", kind: "notification", destination_id: destinationId, title: "Hallway light", message: "The evening routine started." },
  ],
};
export const automation: Automation = {
  id: automationId, revision: 3, definition,
  created_at: "2026-11-09T09:00:00Z", updated_at: "2026-11-09T09:30:00Z",
};
const child: Automation = {
  ...automation, id: childAutomationId, revision: 1,
  definition: { name: "Settle the hallway", enabled: false, triggers: [], steps: [{ id: "wait", kind: "delay", duration_ms: 1000 }] },
};

export function automationHandlers() {
  return [
    http.get("/v1/automations", () => HttpResponse.json({ items: [automation], next_cursor: null })),
    http.get(`/v1/automations/${childAutomationId}`, () => HttpResponse.json(child)),
    http.get("/v1/notification-destinations", () => HttpResponse.json({ items: [{ id: destinationId, name: "Household" }], next_cursor: null })),
    http.get("/v1/adapters", () => HttpResponse.json({ items: [], next_cursor: null })),
  ];
}
