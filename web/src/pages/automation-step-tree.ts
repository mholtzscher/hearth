import type {
  AutomationBranchCondition,
  AutomationCommandStep,
  AutomationDefinition,
  AutomationStep,
} from "../api/types.ts";

export type AutomationOutlineRow =
  | { type: "step"; depth: number; step: AutomationStep; position?: number }
  | { type: "arm"; depth: number; label: string; condition?: AutomationBranchCondition };

/** API definitions are validated by Core. Still bound display traversal so an
 * oversized response cannot cause an unbounded recursive render. */
export function describeAutomationSteps(steps: AutomationStep[]) {
  const rows: AutomationOutlineRow[] = [];
  const commands: AutomationCommandStep[] = [];
  let stepCount = 0;
  let truncated = false;
  function sequence(children: AutomationStep[], depth: number) {
    for (const step of children) {
      if (depth > 8 || stepCount >= 64) {
        truncated = true;
        return;
      }
      stepCount++;
      rows.push({ type: "step", depth, step, position: step.kind ? undefined : commands.length });
      if (step.kind === "if") {
        rows.push({ type: "arm", depth: depth + 1, label: "Then", condition: step.conditions });
        sequence(step.then, depth + 1);
        if (step.else) {
          rows.push({ type: "arm", depth: depth + 1, label: "Else" });
          sequence(step.else, depth + 1);
        }
      } else if (step.kind === "choose") {
        for (const branch of step.branches.slice(0, 32)) {
          rows.push({ type: "arm", depth: depth + 1, label: `Alternative ${branch.id}`, condition: branch.conditions });
          sequence(branch.steps, depth + 1);
        }
        if (step.branches.length > 32) truncated = true;
        if (step.default) {
          rows.push({ type: "arm", depth: depth + 1, label: "Default" });
          sequence(step.default, depth + 1);
        }
      } else {
        commands.push(step);
      }
    }
  }
  sequence(steps, 1);
  return { rows, commands, stepCount, commandCount: commands.length, truncated };
}

/** Pre-order Condition nodes with their boolean nesting, bounded per root. */
export function describeAutomationCondition(root: AutomationBranchCondition) {
  const nodes: { condition: AutomationBranchCondition; depth: number }[] = [];
  function visit(condition: AutomationBranchCondition, depth: number) {
    if (depth > 8 || nodes.length >= 64) return;
    nodes.push({ condition, depth });
    if (condition.kind === "not") visit(condition.child, depth + 1);
    if (condition.kind === "all" || condition.kind === "any") {
      for (const child of condition.children.slice(0, 64)) visit(child, depth + 1);
    }
  }
  visit(root, 1);
  return nodes;
}

/** Include every defined arm and predicate, not just recorded selections. */
export function automationEntityIds(definition: AutomationDefinition): string[] {
  const ids = new Set<string>();
  for (const trigger of definition.triggers) {
    if (trigger.kind !== "cron") ids.add(trigger.entity_id);
  }
  function conditionIds(condition: AutomationBranchCondition) {
    for (const node of describeAutomationCondition(condition)) {
      if (node.condition.kind === "entity_state") ids.add(node.condition.entity_id);
    }
  }
  if (definition.conditions) conditionIds(definition.conditions);
  for (const row of describeAutomationSteps(definition.steps).rows) {
    if (row.type === "arm" && row.condition) conditionIds(row.condition);
    if (row.type === "step" && !row.step.kind) ids.add(row.step.entity_id);
  }
  return [...ids];
}
