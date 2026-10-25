import { describe, expect, it } from "vitest";
import type { AutomationBranchCondition, AutomationDefinition, AutomationStep, AutomationValueBinding } from "../api/types.ts";
import { automationEntityIds, describeAutomationCondition, describeAutomationSteps } from "./automation-step-tree.ts";

describe("Automation v3 outline and references", () => {
  it("visits every Step and Condition variant without mistaking dynamic payloads for references", () => {
    const literal: AutomationValueBinding = { literal: { entity_id: "not-a-target", steps: [{ kind: "command" }] } };
    const condition: AutomationBranchCondition = { id: "all", kind: "all", children: [
      { id: "any", kind: "any", children: [{ id: "expression", kind: "expression", value: { expr: "vars.ready" } }] },
      { id: "not", kind: "not", child: { id: "state", kind: "entity_state", entity_id: "condition-entity", value_pointer: "", operator: "eq", operand: true } },
      { id: "trigger", kind: "trigger", trigger_ids: ["cron"] },
    ] };
    const steps: AutomationStep[] = [
      { id: "command", kind: "command", entity_id: "command-entity", operation: "set", parameters: literal },
      { id: "delay", kind: "delay", duration_ms: { literal: 1000 } },
      { id: "notification", kind: "notification", destination_id: "destination", title: { literal: "Title" }, message: { expr: "string(vars.ready)" } },
      { id: "function", kind: "function", adapter_id: "adapter", function_key: "key", contract_fingerprint: "fingerprint", arguments: literal },
      { id: "assignment", kind: "set_variables", assignments: { ready: { literal: true } } },
      { id: "if", kind: "if", conditions: condition, then: [{ id: "read", kind: "read_state", entity_id: "capture-entity", value_pointer: "" }], else: [
        { id: "choose", kind: "choose", branches: [{ id: "alternative", conditions: condition, steps: [{ id: "unselected", kind: "read_state", entity_id: "unselected-capture", value_pointer: "/value" }] }], default: [] },
      ] },
    ];
    const definition: AutomationDefinition = { name: "Variants", enabled: true, triggers: [{ id: "cron", kind: "cron", expression: "0 7 * * *" }], steps };
    const tree = describeAutomationSteps(steps);
    expect(tree.rows.filter((row) => row.type === "step").map((row) => row.step.id)).toEqual(["command", "delay", "notification", "function", "assignment", "if", "read", "choose", "unselected"]);
    expect(tree).toMatchObject({ stepCount: 9, commandCount: 1, notificationCount: 1, functionCount: 1, truncated: false });
    expect(tree.rows.filter((row) => row.type === "step").map((row) => row.position)).toEqual([0, undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined]);
    expect(describeAutomationCondition(condition).map(({ condition: node }) => node.kind)).toEqual(["all", "any", "expression", "not", "entity_state", "trigger"]);
    expect(automationEntityIds(definition)).toEqual(["command-entity", "condition-entity", "capture-entity", "unselected-capture"]);
  });
});
