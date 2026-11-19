import { Link as RouterLink } from "react-router-dom";

import { automationJsonFieldText } from "../api/automation-json.ts";
import type {
  AutomationBranchCondition,
  AutomationStep,
} from "../api/types.ts";
import { linkClass } from "../components/link-styles.ts";
import {
  assertNever,
  automationDurationText,
  describeAutomationCondition,
  describeAutomationSteps,
} from "./automation-step-tree.ts";

const BindingView = ({
  owner,
  field,
  source,
  label,
}: {
  owner?: object;
  field?: string;
  source?: string;
  label: string;
}) => (
  <div>
    <span className="text-label">
      {label}:{" "}
      {source === undefined ? "Literal" : "Expression (resolved when reached)"}
    </span>
    <pre
      className="text-label max-h-64 overflow-auto font-mono break-all whitespace-pre-wrap"
      aria-label={label}
    >
      {source ??
        (owner && field
          ? automationJsonFieldText(owner, field)
          : "Exact value unavailable")}
    </pre>
  </div>
);

export const AutomationVariables = ({
  variables,
  expressions,
  label = "Initial variables",
}: {
  variables?: Record<string, unknown>;
  expressions?: Record<string, string>;
  label?: string;
}) => {
  if (!variables && !expressions) {
    return null;
  }
  return (
    <div>
      <h3 className="text-body font-semibold">{label}</h3>
      {Object.keys(variables ?? {}).map((name) => (
        <BindingView key={name} owner={variables} field={name} label={name} />
      ))}
      {Object.entries(expressions ?? {}).map(([name, source]) => (
        <BindingView key={name} source={source} label={name} />
      ))}
    </div>
  );
};

const conditionDescription = (
  node: AutomationBranchCondition,
  labels: ReadonlyMap<string, string>
) => {
  switch (node.kind) {
    case "all":
    case "any":
    case "not": {
      return null;
    }
    case "trigger": {
      return <> matches {node.trigger_ids.join(", ")}</>;
    }
    case "expression": {
      return <BindingView source={node.expr} label={`Value for ${node.id}`} />;
    }
    case "entity_state": {
      return (
        <>
          {" "}
          <RouterLink
            to={`/entities/${node.entity_id}`}
            className={linkClass}
            title={node.entity_id}
          >
            {labels.get(node.entity_id) ?? node.entity_id}
          </RouterLink>{" "}
          <span className="font-mono">
            {node.value_pointer || "(root)"} {node.operator}{" "}
            {JSON.stringify(node.operand)}
          </span>
          {node.max_age_seconds !== undefined && (
            <> · max age {node.max_age_seconds}s</>
          )}
        </>
      );
    }
    default: {
      return assertNever(node);
    }
  }
};

const stepDescription = (
  step: AutomationStep,
  labels: ReadonlyMap<string, string>
) => {
  switch (step.kind) {
    case "if":
    case "choose": {
      return null;
    }
    case "delay": {
      return <> · {automationDurationText(step.duration_ms)}</>;
    }
    case "set_variables": {
      return (
        <AutomationVariables
          variables={step.assignments}
          expressions={step.assignments_expr}
          label={`Assignments for ${step.id}`}
        />
      );
    }
    case "read_state": {
      return (
        <>
          {" "}
          ·{" "}
          <RouterLink
            to={`/entities/${step.entity_id}`}
            className={linkClass}
            title={step.entity_id}
          >
            {labels.get(step.entity_id) ?? step.entity_id}
          </RouterLink>
          <span className="font-mono"> · {step.value_pointer || "(root)"}</span>
          {step.max_age_seconds !== undefined && (
            <> · max age {step.max_age_seconds}s</>
          )}
          {" · on unusable: "}
          {step.on_unusable ?? "fail"}
        </>
      );
    }
    case "sub_automation": {
      return (
        <>
          {" "}
          ·{" "}
          <RouterLink
            to={`/automations/${step.automation_id}`}
            className={linkClass}
          >
            {step.automation_id}
          </RouterLink>
          <p className="text-label">
            Waits for the child Run to succeed before continuing.
          </p>
        </>
      );
    }
    case "function": {
      return (
        <>
          {" "}
          ·{" "}
          <span className="font-mono">
            {step.adapter_id} / {step.function_key}
          </span>
          <p className="text-label font-mono break-all">
            Contract fingerprint: {step.contract_fingerprint}
          </p>
          <BindingView
            owner={step}
            field="arguments"
            source={step.arguments_expr}
            label={`Arguments for ${step.id}`}
          />
        </>
      );
    }
    case "notification": {
      return (
        <>
          {" "}
          ·{" "}
          <RouterLink
            to={`/notification-destinations/${step.destination_id}`}
            className={linkClass}
          >
            {step.destination_id}
          </RouterLink>
          <BindingView
            owner={step}
            field="title"
            source={step.title_expr}
            label={`Title for ${step.id}`}
          />
          <BindingView
            owner={step}
            field="message"
            source={step.message_expr}
            label={`Message for ${step.id}`}
          />
        </>
      );
    }
    case "command": {
      return (
        <>
          {" · "}
          <RouterLink
            to={`/entities/${step.entity_id}`}
            className={linkClass}
            title={step.entity_id}
          >
            {labels.get(step.entity_id) ?? step.entity_id}
          </RouterLink>
          {" · "}
          {step.operation}{" "}
          <BindingView
            owner={step}
            field="parameters"
            source={step.parameters_expr}
            label={`Parameters for ${step.id}`}
          />
        </>
      );
    }
    default: {
      return assertNever(step);
    }
  }
};

export const AutomationConditionTree = ({
  condition,
  labels,
}: {
  condition: AutomationBranchCondition;
  labels: ReadonlyMap<string, string>;
}) => (
  <ul className="text-label mt-1">
    {describeAutomationCondition(condition).map(
      ({ condition: node, depth }, index) => (
        <li key={index} style={{ marginLeft: (depth - 1) * 16 }}>
          <span className="font-mono">{node.id}</span>
          {" · "}
          {node.kind}
          {conditionDescription(node, labels)}
        </li>
      )
    )}
  </ul>
);

/** Definition order, not an execution trace. */
const AutomationStepTree = ({
  steps,
  labels,
}: {
  steps: AutomationStep[];
  labels: ReadonlyMap<string, string>;
}) => {
  const tree = describeAutomationSteps(steps);
  return (
    <div>
      <p className="text-muted-foreground text-label">
        {tree.stepCount} defined Steps · {tree.commandCount} commands
        {tree.notificationCount > 0 && (
          <> · {tree.notificationCount} notifications</>
        )}
        {tree.functionCount > 0 && <> · {tree.functionCount} functions</>}
      </p>
      {tree.truncated && (
        <p role="alert">
          Definition exceeds display bounds; outline and counts are incomplete.
        </p>
      )}
      {steps.length === 0 && <p>No Steps.</p>}
      <ul className="mt-2 grid gap-2" aria-label="Step definition outline">
        {tree.rows.map((row, index) => (
          <li
            key={index}
            style={{ marginLeft: (row.depth - 1) * 20 }}
            className="text-label border-l pl-3"
          >
            {row.type === "arm" ? (
              <>
                <span className="font-medium">{row.label}</span>
                {row.condition && (
                  <AutomationConditionTree
                    condition={row.condition}
                    labels={labels}
                  />
                )}
              </>
            ) : (
              <>
                <span className="text-label font-mono">{row.step.id}</span>
                {" · "}
                {row.step.kind === "sub_automation"
                  ? "Sub-automation"
                  : row.step.kind}
                {stepDescription(row.step, labels)}
              </>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
};

export default AutomationStepTree;
