import { Link as RouterLink } from "react-router-dom";
import type { AutomationBranchCondition, AutomationStep } from "../api/types.ts";
import { linkClass } from "../components/common.tsx";
import { describeAutomationCondition, describeAutomationSteps } from "./automation-step-tree.ts";

export function AutomationConditionTree({
  condition,
  labels,
}: {
  condition: AutomationBranchCondition;
  labels: ReadonlyMap<string, string>;
}) {
  return (
    <ul className="mt-1 text-xs">
      {describeAutomationCondition(condition).map(({ condition: node, depth }, index) => (
        <li key={index} style={{ marginLeft: (depth - 1) * 16 }}>
          <span className="font-mono">{node.id}</span>{" · "}{node.kind}
          {node.kind === "trigger" && <> matches {node.trigger_ids.join(", ")}</>}
          {node.kind === "entity_state" && (
            <>
              {" "}
              <RouterLink to={`/entities/${node.entity_id}`} className={linkClass} title={node.entity_id}>
                {labels.get(node.entity_id) ?? node.entity_id}
              </RouterLink>{" "}
              <span className="font-mono">
                {node.value_pointer || "(root)"} {node.operator} {JSON.stringify(node.operand)}
              </span>
              {node.max_age_seconds !== undefined && <> · max age {node.max_age_seconds}s</>}
            </>
          )}
        </li>
      ))}
    </ul>
  );
}

/** Definition order, not an execution trace. Only command leaves get positions. */
export default function AutomationStepTree({
  steps,
  labels,
}: {
  steps: AutomationStep[];
  labels: ReadonlyMap<string, string>;
}) {
  const tree = describeAutomationSteps(steps);
  return (
    <div>
      <p className="text-xs text-muted-foreground">
        {tree.stepCount} defined Steps · {tree.commandCount} commands
      </p>
      {tree.truncated && (
        <p role="alert">Definition exceeds display bounds; outline and counts are incomplete.</p>
      )}
      {steps.length === 0 && <p>No Steps.</p>}
      <ul className="mt-2 grid gap-2" aria-label="Step definition outline">
        {tree.rows.map((row, index) => (
          <li key={index} style={{ marginLeft: (row.depth - 1) * 20 }} className="border-l pl-3 text-sm">
            {row.type === "arm" ? (
              <>
                <span className="font-medium">{row.label}</span>
                {row.condition && <AutomationConditionTree condition={row.condition} labels={labels} />}
              </>
            ) : (
              <>
                <span className="font-mono text-xs">{row.step.id}</span>{" · "}{row.step.kind ?? "command"}
                {!row.step.kind && (
                  <>
                    {" · position "}{row.position}{" · "}
                    <RouterLink to={`/entities/${row.step.entity_id}`} className={linkClass} title={row.step.entity_id}>
                      {labels.get(row.step.entity_id) ?? row.step.entity_id}
                    </RouterLink>{" · "}{row.step.operation}{" "}
                    <span className="font-mono text-xs">{JSON.stringify(row.step.parameters)}</span>
                  </>
                )}
              </>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
