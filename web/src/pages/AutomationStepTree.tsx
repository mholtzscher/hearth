import { Link as RouterLink } from "react-router-dom";
import { automationJsonFieldText } from "../api/automation-json.ts";
import type { AutomationBranchCondition, AutomationStep } from "../api/types.ts";
import { linkClass } from "../components/common.tsx";
import { assertNever, automationDurationText, describeAutomationCondition, describeAutomationSteps } from "./automation-step-tree.ts";

function BindingView({ owner, field, source, label }: { owner?: object; field?: string; source?: string; label: string }) {
  return <div><span className="text-xs">{label}: {source !== undefined ? "Expression (resolved when reached)" : "Literal"}</span>
    <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono text-xs" aria-label={label}>{source ?? (owner && field ? automationJsonFieldText(owner, field) : "Exact value unavailable")}</pre>
  </div>;
}

export function AutomationVariables({ variables, expressions, label = "Initial variables" }: { variables?: Record<string, unknown>; expressions?: Record<string, string>; label?: string }) {
  if (!variables && !expressions) return null;
  return <div><h3 className="text-sm font-medium">{label}</h3>
    {Object.keys(variables ?? {}).map((name) => <BindingView key={name} owner={variables} field={name} label={name} />)}
    {Object.entries(expressions ?? {}).map(([name, source]) => <BindingView key={name} source={source} label={name} />)}
  </div>;
}

function conditionDescription(node: AutomationBranchCondition, labels: ReadonlyMap<string, string>) {
  switch (node.kind) {
    case "all":
    case "any":
    case "not": return null;
    case "trigger": return <> matches {node.trigger_ids.join(", ")}</>;
    case "expression": return <BindingView source={node.expr} label={`Value for ${node.id}`} />;
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

function stepDescription(step: AutomationStep, labels: ReadonlyMap<string, string>) {
  switch (step.kind) {
    case "if":
    case "choose": return null;
    case "delay": return <> · {automationDurationText(step.duration_ms)}</>;
    case "set_variables": return <AutomationVariables variables={step.assignments} expressions={step.assignments_expr} label={`Assignments for ${step.id}`} />;
    case "read_state": return <> · <RouterLink to={`/entities/${step.entity_id}`} className={linkClass} title={step.entity_id}>{labels.get(step.entity_id) ?? step.entity_id}</RouterLink>
      <span className="font-mono"> · {step.value_pointer || "(root)"}</span>
      {step.max_age_seconds !== undefined && <> · max age {step.max_age_seconds}s</>}
      {" · on unusable: "}{step.on_unusable ?? "fail"}
    </>;
    case "function": return <> · <span className="font-mono">{step.adapter_id} / {step.function_key}</span>
      <p className="break-all font-mono text-xs">Contract fingerprint: {step.contract_fingerprint}</p>
      <BindingView owner={step} field="arguments" source={step.arguments_expr} label={`Arguments for ${step.id}`} />
    </>;
    case "notification": return <> · <RouterLink to={`/notification-destinations/${step.destination_id}`} className={linkClass}>{step.destination_id}</RouterLink>
      <BindingView owner={step} field="title" source={step.title_expr} label={`Title for ${step.id}`} />
      <BindingView owner={step} field="message" source={step.message_expr} label={`Message for ${step.id}`} />
    </>;
    case "command": return <>
      {" · "}
      <RouterLink to={`/entities/${step.entity_id}`} className={linkClass} title={step.entity_id}>
        {labels.get(step.entity_id) ?? step.entity_id}
      </RouterLink>{" · "}{step.operation}{" "}
      <BindingView owner={step} field="parameters" source={step.parameters_expr} label={`Parameters for ${step.id}`} />
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

/** Definition order, not an execution trace. */
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
                {stepDescription(row.step, labels)}
              </>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
