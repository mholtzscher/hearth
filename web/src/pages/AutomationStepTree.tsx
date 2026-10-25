import { Link as RouterLink } from "react-router-dom";
import { automationJsonText } from "../api/automation-json.ts";
import type { AutomationBranchCondition, AutomationStep, AutomationValueBinding } from "../api/types.ts";
import { linkClass } from "../components/common.tsx";
import { assertNever, automationDurationText, describeAutomationCondition, describeAutomationSteps } from "./automation-step-tree.ts";

function BindingView({ value, label }: { value: AutomationValueBinding; label: string }) {
  return <div><span className="text-xs">{label}: {"expr" in value ? "Expression (resolved when reached)" : "Literal"}</span>
    <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono text-xs" aria-label={label}>{automationJsonText(value)}</pre>
  </div>;
}

export function AutomationVariables({ variables, label = "Initial variables" }: { variables?: Record<string, AutomationValueBinding>; label?: string }) {
  if (!variables) return null;
  return <div><h3 className="text-sm font-medium">{label}</h3>
    {Object.entries(variables).map(([name, value]) => <BindingView key={name} value={value} label={name} />)}
  </div>;
}

function conditionDescription(node: AutomationBranchCondition, labels: ReadonlyMap<string, string>) {
  switch (node.kind) {
    case "all":
    case "any":
    case "not": return null;
    case "trigger": return <> matches {node.trigger_ids.join(", ")}</>;
    case "expression": return <BindingView value={node.value} label={`Value for ${node.id}`} />;
    case "entity_state": return <>
      {" "}<RouterLink to={`/entities/${node.entity_id}`} className={linkClass} title={node.entity_id}>
        {labels.get(node.entity_id) ?? node.entity_id}
      </RouterLink>{" "}
      <span className="font-mono">{node.value_pointer || "(root)"} {node.operator} {JSON.stringify(node.operand)}</span>
      {node.max_age_seconds !== undefined && <> · max age {node.max_age_seconds}s</>}
    </>;
    default: return assertNever(node);
  }
}

function stepDescription(step: AutomationStep, position: number | undefined, labels: ReadonlyMap<string, string>) {
  switch (step.kind) {
    case "if":
    case "choose": return null;
    case "delay": return <> · Literal · {automationDurationText(step.duration_ms.literal)}</>;
    case "set_variables": return <AutomationVariables variables={step.assignments} label={`Assignments for ${step.id}`} />;
    case "read_state": return <> · <RouterLink to={`/entities/${step.entity_id}`} className={linkClass} title={step.entity_id}>{labels.get(step.entity_id) ?? step.entity_id}</RouterLink>
      <span className="font-mono"> · {step.value_pointer || "(root)"}</span>
      {step.max_age_seconds !== undefined && <> · max age {step.max_age_seconds}s</>}
      {" · on unusable: "}{step.on_unusable ?? "fail"}
    </>;
    case "function": return <> · <span className="font-mono">{step.adapter_id} / {step.function_key}</span>
      <p className="break-all font-mono text-xs">Contract fingerprint: {step.contract_fingerprint}</p>
      <BindingView value={step.arguments} label={`Arguments for ${step.id}`} />
    </>;
    case "notification": return <> · <RouterLink to={`/notification-destinations/${step.destination_id}`} className={linkClass}>{step.destination_id}</RouterLink>
      <BindingView value={step.title} label={`Title for ${step.id}`} />
      <BindingView value={step.message} label={`Message for ${step.id}`} />
    </>;
    case "command": return <>
      {" · position "}{position}{" · "}
      <RouterLink to={`/entities/${step.entity_id}`} className={linkClass} title={step.entity_id}>
        {labels.get(step.entity_id) ?? step.entity_id}
      </RouterLink>{" · "}{step.operation}{" "}
      <BindingView value={step.parameters} label={`Parameters for ${step.id}`} />
    </>;
    default: return assertNever(step);
  }
}

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
          {conditionDescription(node, labels)}
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
        {tree.stepCount} defined Steps · {tree.commandCount} commands{tree.notificationCount > 0 && <> · {tree.notificationCount} notifications</>}
        {tree.functionCount > 0 && <> · {tree.functionCount} functions</>}
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
                <span className="font-mono text-xs">{row.step.id}</span>{" · "}{row.step.kind}
                {stepDescription(row.step, row.position, labels)}
              </>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
