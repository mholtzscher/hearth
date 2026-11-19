import { useEffect, useMemo, useRef, useState } from "react";
import type { Dispatch, ReactNode, SetStateAction } from "react";
import {
  Link as RouterLink,
  useParams,
  useSearchParams,
} from "react-router-dom";

import {
  automationFetch,
  automationJsonFieldText,
  automationJsonText,
  serializeAutomationEnablement,
} from "../api/automation-json.ts";
import { useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { fetchAllCollectionPages } from "../api/pagination.ts";
import { problemCode } from "../api/problem.ts";
import type {
  Automation,
  AutomationComparison,
  AutomationAdmissionCause,
  AutomationConditionDecision,
  AutomationConditionEvaluation,
  AutomationDefinition,
  AutomationStepExecution,
  AutomationHistoryEntry,
  AutomationHistorySummary,
  AutomationRun,
  AutomationSkip,
  AutomationSkipReason,
  AutomationTrigger,
  AutomationValueFailure,
  Collection,
  AutomationDeviceFact,
  Device,
  Entity,
} from "../api/types.ts";
import { Button } from "../components/button.tsx";
import { Card } from "../components/card.tsx";
import { EmptyRow } from "../components/empty-row.tsx";
import { ErrorBox } from "../components/error-box.tsx";
import { Facts } from "../components/facts.tsx";
import { Label } from "../components/label.tsx";
import { linkClass } from "../components/link-styles.ts";
import { MonoId } from "../components/mono-id.tsx";
import { PageHeader } from "../components/page-header.tsx";
import { RawJson } from "../components/raw-json.tsx";
import { Section } from "../components/section.tsx";
import { StatusChip } from "../components/status-chip.tsx";
import { Switch } from "../components/switch.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table.tsx";
import { AutomationCard } from "../features/automations/automation-card";
import { entityLabelText } from "../features/automations/automation-read";
import { fromSavedAutomation } from "../features/automations/document";
import { Canvas } from "../features/dashboards/canvas";
import { useOptionalWorkspace } from "../features/workspace/workspace";
import {
  assertNever,
  automationDurationText,
  automationEntityIds,
  describeAutomationSteps,
} from "./automation-step-tree.ts";
import AutomationStepTree, {
  AutomationConditionTree,
  AutomationVariables,
} from "./automation-step-tree.tsx";

const failureLabel = (failure: AutomationValueFailure): string => {
  if (failure.field) {
    return `${failure.failure_code} · ${failure.field}`;
  }
  if (failure.condition_id) {
    return `${failure.failure_code} · condition ID ${failure.condition_id}`;
  }
  return failure.failure_code;
};

const HISTORY_PAGE_LIMIT = 50;

/** One page path for the newest-first Automation history keyset. */
const automationHistoryPath = (
  automationId: string,
  cursor: string | undefined
): string =>
  `/v1/automations/${automationId}/history?limit=${HISTORY_PAGE_LIMIT}${
    cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""
  }`;

const EMPTY_LABELS: ReadonlyMap<string, string> = new Map();

/** Best-effort Entity labels, "Name (Device)", for the referenced IDs. The
    lookup reads the Entities and Devices collections once per server and keeps
    only the referenced IDs. A failed Entities read leaves references labeled by
    ID; a failed Devices read leaves the name alone. Neither blocks the page.
    `entityIds` is joined for effect identity, so a re-render with the same IDs
    does not refetch. */
const useEntityLabels = (entityIds: string[]): ReadonlyMap<string, string> => {
  const [labels, setLabels] = useState<ReadonlyMap<string, string>>(new Map());
  const wanted = entityIds.join(",");
  useEffect(() => {
    const ids = new Set(wanted ? wanted.split(",") : []);
    if (ids.size === 0) {
      return;
    }
    let cancelled = false;
    const devices = fetchAllCollectionPages<Device>("/v1/devices")
      .then(
        (collection) =>
          new Map(collection.items.map((device) => [device.id, device.name]))
      )
      .catch(() => new Map<string, string>());
    const load = async () => {
      try {
        const [collection, deviceNames] = await Promise.all([
          fetchAllCollectionPages<Entity>("/v1/entities"),
          devices,
        ]);
        if (cancelled) {
          return;
        }
        const found = new Map<string, string>();
        for (const entity of collection.items) {
          if (ids.has(entity.id)) {
            found.set(
              entity.id,
              entityLabelText(entity.name, deviceNames.get(entity.device_id))
            );
          }
        }
        setLabels(found);
      } catch {
        if (!cancelled) {
          setLabels(new Map());
        }
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, [wanted]);
  return wanted ? labels : EMPTY_LABELS;
};

/** Entity reference in a Trigger or Step: always a link to the Entity page,
    labeled with the resolved name when the lookup found one. */
const AutomationEntityLink = ({
  entityId,
  labels,
}: {
  entityId: string;
  labels: ReadonlyMap<string, string>;
}) => {
  const name = labels.get(entityId);
  return (
    <RouterLink
      to={`/entities/${entityId}`}
      className={linkClass}
      title={entityId}
    >
      {name ?? entityId}
    </RouterLink>
  );
};

/** One comparison as `pointer operator operand`, e.g. `value eq true`. */
const comparisonText = (comparison: AutomationComparison): string =>
  `${comparison.value_pointer} ${comparison.operator} ${JSON.stringify(comparison.operand)}`;

const triggerDescription = (trigger: AutomationTrigger): ReactNode => {
  switch (trigger.kind) {
    case "cron": {
      return trigger.expression;
    }
    case "entity_event": {
      return <>event name {trigger.event_name}</>;
    }
    case "held_state": {
      return (
        <>
          held for <span>{trigger.for_seconds} seconds</span> · comparisons{" "}
          <span>{trigger.comparisons.map(comparisonText).join(" and ")}</span>
        </>
      );
    }
    case "observation": {
      return (
        <>
          dispositions {(trigger.dispositions ?? []).join(", ") || "—"}
          {(trigger.comparisons?.length ?? 0) > 0 && (
            <>
              {" "}
              · comparisons{" "}
              {trigger.comparisons?.map(comparisonText).join(" and ")}
            </>
          )}
          {(trigger.previous_comparisons?.length ?? 0) > 0 && (
            <>
              {" "}
              · previous comparisons{" "}
              {trigger.previous_comparisons?.map(comparisonText).join(" and ")}
            </>
          )}
        </>
      );
    }
    default: {
      return assertNever(trigger);
    }
  }
};

/** Readable Triggers: each one is an alternative reason the Automation starts. */
const AutomationTriggerList = ({
  triggers,
  labels,
}: {
  triggers: AutomationTrigger[];
  labels: ReadonlyMap<string, string>;
}) => {
  if (triggers.length === 0) {
    return (
      <p className="text-muted-foreground text-label">No automatic triggers</p>
    );
  }
  return (
    <ul className="mt-1 grid gap-2">
      {triggers.map((trigger) => (
        <Card asChild size="compact" key={trigger.id} className="py-2">
          <li>
            <div className="flex flex-wrap items-center gap-2">
              <StatusChip status={trigger.kind} />
              <span className="text-label font-mono">{trigger.id}</span>
              {trigger.kind !== "cron" && (
                <>
                  <span className="text-muted-foreground text-label">
                    matches
                  </span>
                  <AutomationEntityLink
                    entityId={trigger.entity_id}
                    labels={labels}
                  />
                </>
              )}
            </div>
            <p className="text-label mt-1 font-mono">
              {triggerDescription(trigger)}
            </p>
          </li>
        </Card>
      ))}
    </ul>
  );
};

type ExecutionOf<Kind extends AutomationStepExecution["kind"]> = Extract<
  AutomationStepExecution,
  { kind: Kind }
>;

const ConditionEvaluationView = ({
  evaluation,
}: {
  evaluation: AutomationConditionEvaluation;
}) => (
  <ul className="text-label">
    {evaluation.nodes.map((node) => {
      switch (node.kind) {
        case "expression": {
          return (
            <li key={node.id}>
              {node.id}: {node.result} · expression value {String(node.value)}
            </li>
          );
        }
        case "trigger": {
          return (
            <li key={node.id}>
              {node.id}: {node.result} · matched triggers{" "}
              {node.matched_trigger_ids.join(", ") || "none"}
            </li>
          );
        }
        case "entity_state": {
          return (
            <li key={node.id}>
              {node.id}: {node.result}
              {node.result === "unknown" && <> · {node.unknown_reason}</>}
              {"selected_value" in node && (
                <>
                  {" "}
                  · selected value{" "}
                  {automationJsonFieldText(node, "selected_value")}
                </>
              )}
              {"observation_id" in node && (
                <>
                  {" "}
                  · observation {node.observation_id} at {node.observed_at}
                </>
              )}
            </li>
          );
        }
        default: {
          return assertNever(node);
        }
      }
    })}
  </ul>
);

const CommandExecutionEvidence = ({
  execution,
}: {
  execution: ExecutionOf<"command">;
}) => {
  const { evidence } = execution;
  return (
    <>
      <p>
        Command outcome:{" "}
        {evidence.command_outcome ?? "— (no confirmed Command outcome)"}
      </p>
      {evidence.verified_command_id && (
        <RouterLink
          className={linkClass}
          to={`/commands?command_id=${evidence.verified_command_id}`}
        >
          <MonoId value={evidence.verified_command_id} />
        </RouterLink>
      )}
      {evidence.command_id && (
        <p className="text-label font-mono break-all">
          Reserved Command: {evidence.command_id}
        </p>
      )}
      {evidence.correlation_id && (
        <p className="text-label font-mono break-all">
          Correlation: {evidence.correlation_id}
        </p>
      )}
      {!evidence.verified_command_id && (
        <p className="text-label">No ownership-verified Command link.</p>
      )}
      {evidence.reason_code && (
        <p className="text-label font-mono">{evidence.reason_code}</p>
      )}
    </>
  );
};

const DelayExecutionEvidence = ({
  execution,
  snapshot,
}: {
  execution: ExecutionOf<"delay">;
  snapshot: AutomationDefinition;
}) => {
  const step = describeAutomationSteps(snapshot.steps).rows.find(
    (row) => row.type === "step" && row.step.id === execution.step_id
  );
  const duration =
    step?.type === "step" && step.step.kind === "delay"
      ? step.step.duration_ms
      : undefined;
  return (
    <>
      {duration !== undefined && (
        <>
          <p>Duration: {automationDurationText(duration)}</p>
          <p className="text-label font-mono">
            Expected due (diagnostic):{" "}
            {new Date(
              Date.parse(execution.started_at) + duration
            ).toISOString()}
          </p>
        </>
      )}
      <p className="text-label">No wait resumes after interruption.</p>
      {execution.evidence.reason_code && (
        <p className="text-label font-mono">{execution.evidence.reason_code}</p>
      )}
    </>
  );
};

const SubAutomationExecutionEvidence = ({
  execution,
  snapshot,
}: {
  execution: ExecutionOf<"sub_automation">;
  snapshot: AutomationDefinition;
}) => {
  const row = describeAutomationSteps(snapshot.steps).rows.find(
    (candidate) =>
      candidate.type === "step" && candidate.step.id === execution.step_id
  );
  const target =
    row?.type === "step" && row.step.kind === "sub_automation"
      ? row.step.automation_id
      : undefined;
  const { evidence } = execution;
  return (
    <>
      {"child_run_id" in evidence && (
        <p>
          Child Run:{" "}
          {target ? (
            <RouterLink
              className={linkClass}
              to={`/automations/${target}?history_id=${evidence.child_run_id}`}
            >
              {evidence.child_run_id}
            </RouterLink>
          ) : (
            evidence.child_run_id
          )}
        </p>
      )}
      {"skip_id" in evidence && (
        <p>
          Condition Skip:{" "}
          {target ? (
            <RouterLink
              className={linkClass}
              to={`/automations/${target}?history_id=${evidence.skip_id}`}
            >
              {evidence.skip_id}
            </RouterLink>
          ) : (
            evidence.skip_id
          )}
        </p>
      )}
      {"reason_code" in evidence && (
        <p className="text-label font-mono">{evidence.reason_code}</p>
      )}
      {"child_status" in evidence && (
        <p>
          Child outcome: {evidence.child_status} · {evidence.child_failure_code}
        </p>
      )}
    </>
  );
};

const NotificationExecutionEvidence = ({
  execution,
}: {
  execution: ExecutionOf<"notification">;
}) => {
  const { evidence } = execution;
  return (
    <>
      {evidence.attempt_id && (
        <RouterLink
          className={linkClass}
          to={`/notifications/${evidence.attempt_id}`}
        >
          {evidence.attempt_id}
        </RouterLink>
      )}
      <p>
        Provider outcome:{" "}
        {evidence.outcome ?? "— (no confirmed attempt outcome)"}
      </p>
      {evidence.outcome === "unknown" && (
        <p className="text-label">
          Submission may have occurred; acceptance is unproven. Do not
          automatically resend.
        </p>
      )}
      {evidence.outcome === "accepted" && (
        <p className="text-label">
          Provider acceptance, not recipient delivery.
        </p>
      )}
      {evidence.reason_code && (
        <p className="text-label font-mono">{evidence.reason_code}</p>
      )}
    </>
  );
};

const FunctionExecutionEvidence = ({
  execution,
}: {
  execution: ExecutionOf<"function">;
}) => {
  const { evidence } = execution;
  return (
    <>
      {evidence.invocation_id && (
        <p className="text-label font-mono break-all">
          Reserved invocation: {evidence.invocation_id}
        </p>
      )}
      <p>
        Invocation outcome:{" "}
        {evidence.invocation_outcome ?? "— (no terminal invocation evidence)"}
      </p>
      {evidence.catalog_revision && (
        <p className="text-label font-mono">
          Catalog revision: {evidence.catalog_revision}
        </p>
      )}
      {evidence.invocation_outcome === "unknown" && (
        <p className="text-label">
          Effects may have occurred; success is unproven. Do not automatically
          retry.
        </p>
      )}
      {evidence.reason_code && (
        <p className="text-label font-mono">{evidence.reason_code}</p>
      )}
    </>
  );
};

const BranchExecutionEvidence = ({
  execution,
}: {
  execution: ExecutionOf<"if" | "choose">;
}) => {
  const { evidence } = execution;
  return (
    <>
      <p>
        Selection:{" "}
        {evidence.outcome === "branch"
          ? `Alternative ${evidence.selected_branch_id}`
          : evidence.outcome}
      </p>
      <p className="text-label">
        Selection only; does not establish child completion.
      </p>
      {"failure_code" in evidence && (
        <p className="text-label font-mono">{evidence.failure_code}</p>
      )}
      <details>
        <summary className="cursor-pointer">
          Evidence for {execution.step_id}
        </summary>
        <pre className="text-label mt-2 overflow-auto font-mono whitespace-pre-wrap">
          {automationJsonText(execution)}
        </pre>
        {evidence.evaluations.map((item, index) => (
          <ConditionEvaluationView key={index} evaluation={item.evaluation} />
        ))}
      </details>
    </>
  );
};

const StepExecutionEvidence = ({
  execution,
  snapshot,
}: {
  execution: AutomationStepExecution;
  snapshot: AutomationDefinition;
}) => {
  switch (execution.kind) {
    case "command": {
      return <CommandExecutionEvidence execution={execution} />;
    }
    case "delay": {
      return (
        <DelayExecutionEvidence execution={execution} snapshot={snapshot} />
      );
    }
    case "sub_automation": {
      return (
        <SubAutomationExecutionEvidence
          execution={execution}
          snapshot={snapshot}
        />
      );
    }
    case "notification": {
      return <NotificationExecutionEvidence execution={execution} />;
    }
    case "function": {
      return <FunctionExecutionEvidence execution={execution} />;
    }
    case "if":
    case "choose": {
      return <BranchExecutionEvidence execution={execution} />;
    }
    case "read_state":
    case "set_variables": {
      return execution.phase === "succeeded" ? (
        <p>Value evidence recorded in execution.</p>
      ) : null;
    }
    default: {
      return assertNever(execution);
    }
  }
};

const StepExecutionTimeline = ({ run }: { run: AutomationRun }) => (
  <Table className="mt-1" aria-label="Step execution timeline">
    <TableHeader>
      <TableRow>
        <TableHead>Sequence</TableHead>
        <TableHead>Step id</TableHead>
        <TableHead>Kind</TableHead>
        <TableHead>Phase</TableHead>
        <TableHead>Evidence</TableHead>
        <TableHead>Started</TableHead>
        <TableHead>Completed</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      {run.executions.length === 0 && (
        <EmptyRow
          colSpan={7}
          message="This Run has no durably reached Steps."
        />
      )}
      {[...run.executions]
        .toSorted((a, b) => a.sequence - b.sequence)
        .map((execution) => (
          <TableRow key={execution.step_id}>
            <TableCell>{execution.sequence}</TableCell>
            <TableCell className="text-label font-mono">
              {execution.step_id}
            </TableCell>
            <TableCell>
              {execution.kind === "sub_automation"
                ? "Sub-automation"
                : execution.kind}
            </TableCell>
            <TableCell>
              <StatusChip status={execution.phase} />
            </TableCell>
            <TableCell>
              <StepExecutionEvidence
                execution={execution}
                snapshot={run.snapshot}
              />
              {"value_failure" in execution.evidence &&
                execution.evidence.value_failure && (
                  <p>
                    Step value failure:{" "}
                    {failureLabel(execution.evidence.value_failure)}
                  </p>
                )}
              {execution.kind !== "if" && execution.kind !== "choose" && (
                <details>
                  <summary className="cursor-pointer">
                    Exact execution JSON for {execution.step_id}
                  </summary>
                  <pre className="text-label max-h-96 overflow-auto font-mono break-all whitespace-pre-wrap">
                    {automationJsonText(execution)}
                  </pre>
                </details>
              )}
            </TableCell>
            <TableCell className="text-label font-mono">
              {execution.started_at}
            </TableCell>
            <TableCell className="text-label font-mono">
              {execution.completed_at ?? "—"}
            </TableCell>
          </TableRow>
        ))}
    </TableBody>
  </Table>
);

/** Retained Fact evidence rows, shared by a Run and a Skip. An Entity Event
    Fact reports its event name and carries no Observation value. */
const deviceFactRows = (
  fact: AutomationDeviceFact,
  labels: ReadonlyMap<string, string>
): [string, ReactNode][] => {
  const common: [string, ReactNode][] = [
    ["Fact id", fact.fact_id],
    ["Fact family", fact.family],
    [
      "Fact entity",
      <AutomationEntityLink
        key="Fact entity"
        entityId={fact.entity_id}
        labels={labels}
      />,
    ],
    ["Fact emitted", fact.emitted_at],
  ];
  switch (fact.family) {
    case "observation": {
      return [
        ...common,
        ["Observation id", fact.observation_id],
        ["Disposition", fact.disposition],
        ["Reported value", automationJsonFieldText(fact, "value")],
        [
          "Previous value",
          "previous_value" in fact
            ? automationJsonFieldText(fact, "previous_value")
            : "— (not reported)",
        ],
      ];
    }
    case "entity_event": {
      return [
        ...common,
        ["Event id", fact.event_id],
        ["Event name", fact.name],
        ["Reported value", "— (an Entity Event Fact carries no value)"],
      ];
    }
    default: {
      return assertNever(fact);
    }
  }
};

const causeRows = (
  cause: AutomationAdmissionCause,
  labels: ReadonlyMap<string, string>
): [string, ReactNode][] => {
  switch (cause.kind) {
    case "manual": {
      return [["Admission cause", "manual"]];
    }
    case "invocation": {
      return [
        ["Admission cause", "invocation"],
        [
          "Parent Automation",
          <RouterLink
            key="Parent Automation"
            className={linkClass}
            to={`/automations/${cause.parent_automation_id}`}
          >
            {cause.parent_automation_id}
          </RouterLink>,
        ],
        [
          "Parent Run",
          <RouterLink
            key="Parent Run"
            className={linkClass}
            to={`/automations/${cause.parent_automation_id}?history_id=${cause.parent_run_id}`}
          >
            {cause.parent_run_id}
          </RouterLink>,
        ],
        ["Parent Step", cause.parent_step_id],
      ];
    }
    case "schedule": {
      return [["Admission cause", "schedule"]];
    }
    case "device_fact": {
      return deviceFactRows(cause.fact, labels);
    }
    case "held_state": {
      return [
        ["Held trigger", cause.evidence.trigger_id],
        ["Hold started", cause.evidence.started_at],
        ["Hold due", cause.evidence.due_at],
      ];
    }
    default: {
      return assertNever(cause);
    }
  }
};

const ConditionDecisionView = ({
  decision,
}: {
  decision: AutomationConditionDecision;
}) => {
  switch (decision.mode) {
    case "not_configured": {
      return <p>Admission Conditions not configured.</p>;
    }
    case "not_evaluated": {
      return <p>Admission Conditions not evaluated.</p>;
    }
    case "bypassed": {
      return <p>Admission Conditions bypassed.</p>;
    }
    case "evaluated": {
      return (
        <>
          <p>Admission Conditions: {decision.evaluation.result}</p>
          <ConditionEvaluationView evaluation={decision.evaluation} />
        </>
      );
    }
    default: {
      return assertNever(decision);
    }
  }
};

/** Plain-language meaning of a Skip reason (GLOSSARY.md: Automation Skip). */
const skipReasonNote = (reason: AutomationSkipReason): string => {
  switch (reason) {
    case "automation_busy": {
      return "Matched while this Automation already had a running Run, so no second Run started.";
    }
    case "stale_fact": {
      return "Matched a Fact that was already too old to run, so no Run started. A Skip never queues execution.";
    }
    case "conditions_false": {
      return "Conditions were false, so no Run started.";
    }
    case "conditions_unknown": {
      return "Conditions could not be confirmed, so no Run started.";
    }
    default: {
      return assertNever(reason);
    }
  }
};

const runOutcomeRows = (run: AutomationRun): [string, ReactNode][] => {
  switch (run.status) {
    case "running": {
      return [
        ["Completed at", "— (not complete yet)"],
        ["Failure", "—"],
      ];
    }
    case "succeeded": {
      return [
        ["Completed at", run.completed_at],
        ["Failure", "—"],
      ];
    }
    case "failed":
    case "interrupted": {
      return [
        ["Completed at", run.completed_at],
        ["Failure", run.failure_code],
      ];
    }
    default: {
      return assertNever(run);
    }
  }
};

const historySummaryStatus = (summary: AutomationHistorySummary): string => {
  switch (summary.kind) {
    case "run": {
      return summary.status;
    }
    case "skip": {
      return summary.reason;
    }
    default: {
      return assertNever(summary);
    }
  }
};

const RunInitialization = ({ run }: { run: AutomationRun }) => {
  if (run.data_evidence === "legacy_unavailable") {
    return (
      <p>Value evidence unavailable (created before expression support).</p>
    );
  }
  const initialization = run.value_initialization;
  if (!initialization) {
    return <p>No initialization recorded yet.</p>;
  }
  return (
    <>
      <p>
        {initialization.kind} at {initialization.recorded_at}
      </p>
      {initialization.kind === "failed" && (
        <p>Initialization failure: {failureLabel(initialization.failure)}</p>
      )}
      <details>
        <summary className="cursor-pointer">Exact initialization JSON</summary>
        <pre className="text-label max-h-96 overflow-auto font-mono break-all whitespace-pre-wrap">
          {automationJsonText(initialization)}
        </pre>
      </details>
    </>
  );
};

/** One Run: header facts, Fact evidence, ordered Step executions, and the
    immutable definition snapshot the Run executed. */
const AutomationRunView = ({
  run,
  labels,
}: {
  run: AutomationRun;
  labels: ReadonlyMap<string, string>;
}) => (
  <div>
    <div className="flex flex-wrap items-center gap-2">
      <StatusChip label="run" status={run.status} />
      <StatusChip label="cause" status={run.cause.kind} />
      <span className="text-muted-foreground text-label">
        revision {run.revision}
      </span>
    </div>
    <Facts
      rows={[
        ["Run id", run.id],
        ["Automation", run.automation_name],
        ["Started at", run.started_at],
        ...runOutcomeRows(run),
        [
          "Matched triggers",
          run.matched_trigger_ids.length > 0
            ? run.matched_trigger_ids.join(", ")
            : "none (a manual Run starts without one)",
        ],
      ]}
    />
    <div className="mt-2">
      <Facts rows={causeRows(run.cause, labels)} />
    </div>
    <ConditionDecisionView decision={run.condition_decision} />
    <h3 className="text-body mt-4 font-semibold">Run value initialization</h3>
    <RunInitialization run={run} />
    <h3 className="text-body mt-4 font-semibold">Step executions</h3>
    <p className="text-muted-foreground text-label">
      One zero-based sequence across all Step kinds. Only durably reached Steps
      appear; absence does not establish why a Step was not reached. Expected
      due times are diagnostic UTC timestamps, not countdowns or timer
      authority. Reserved Command, attempt and invocation IDs do not prove
      admission or dispatch. Missing external detail does not change retained
      outcomes.
    </p>
    <StepExecutionTimeline run={run} />
    <h3 className="text-body mt-4 font-semibold">Definition snapshot</h3>
    <AutomationTriggerList triggers={run.snapshot.triggers} labels={labels} />
    <AutomationVariables
      variables={run.snapshot.variables}
      expressions={run.snapshot.variables_expr}
    />
    {run.snapshot.conditions && (
      <AutomationConditionTree
        condition={run.snapshot.conditions}
        labels={labels}
      />
    )}
    <AutomationStepTree steps={run.snapshot.steps} labels={labels} />
    <RawJson value={run} code={automationJsonText(run)} title="Raw Run JSON" />
  </div>
);

/** One Skip: the matched Fact, its reason, and the Trigger snapshots that matched. */
const AutomationSkipView = ({
  skip,
  labels,
}: {
  skip: AutomationSkip;
  labels: ReadonlyMap<string, string>;
}) => (
  <div>
    <div className="flex flex-wrap items-center gap-2">
      <StatusChip label="skip" status={skip.reason} />
      <StatusChip label="cause" status={skip.cause.kind} />
      <span className="text-muted-foreground text-label">
        revision {skip.revision}
      </span>
    </div>
    <p className="text-muted-foreground text-label mt-1.5">
      {skipReasonNote(skip.reason)}
    </p>
    <Facts
      rows={[
        ["Skip id", skip.id],
        ["Automation", skip.automation_name],
        ["Skipped at", skip.skipped_at],
        ...causeRows(skip.cause, labels),
      ]}
    />
    <ConditionDecisionView decision={skip.condition_decision} />
    <h3 className="text-body mt-4 font-semibold">Matched Triggers</h3>
    <AutomationTriggerList triggers={skip.matched_triggers} labels={labels} />
    <RawJson
      value={skip}
      code={automationJsonText(skip)}
      title="Raw Skip JSON"
    />
  </div>
);

/** One retained history entry: exactly one of Run or Skip is present. */
const AutomationHistoryEntryView = ({
  entry,
  labels,
}: {
  entry: AutomationHistoryEntry;
  labels: ReadonlyMap<string, string>;
}) => {
  switch (entry.kind) {
    case "run": {
      return <AutomationRunView run={entry.run} labels={labels} />;
    }
    case "skip": {
      return <AutomationSkipView skip={entry.skip} labels={labels} />;
    }
    default: {
      return assertNever(entry);
    }
  }
};

/** Plain-language explanation for a failed manual Run POST. The POST is
    non-idempotent, so the caller needs to know whether a Run may exist. */
const manualRunFailure = (error: unknown): string | null => {
  switch (problemCode(error)) {
    case "automation_busy": {
      return "This Automation already has a running Run, so no Run was created (409 automation_busy).";
    }
    case "admission_unavailable": {
      return "Automation admission is closed, so no Run was created (503 admission_unavailable).";
    }
    case "not_found": {
      return "This Automation no longer exists (404 not_found), so no Run was created.";
    }
    default: {
      return null;
    }
  }
};

const automationReferenceIds = (
  definition: AutomationDefinition | undefined,
  entry: AutomationHistoryEntry | null,
  startedRun: AutomationRun | null
): string[] => {
  const ids = new Set<string>();
  for (const snapshot of [
    definition,
    entry?.kind === "run" ? entry.run.snapshot : undefined,
    startedRun?.snapshot,
  ]) {
    if (snapshot) {
      for (const id of automationEntityIds(snapshot)) {
        ids.add(id);
      }
    }
  }
  for (const trigger of entry?.kind === "skip"
    ? entry.skip.matched_triggers
    : []) {
    if (trigger.kind !== "cron") {
      ids.add(trigger.entity_id);
    }
  }
  for (const cause of [
    entry?.kind === "run" ? entry.run.cause : entry?.skip.cause,
    startedRun?.cause,
  ]) {
    if (cause?.kind === "device_fact") {
      ids.add(cause.fact.entity_id);
    }
  }
  return [...ids];
};

const savedAuthoringDocument = (automation: Automation | null) => {
  try {
    return automation ? fromSavedAutomation(automation).document : null;
  } catch {
    return null;
  }
};

const AutomationDetailHeader = ({
  automation,
  automationId,
  editing,
  error,
  loading,
  onRefresh,
  onCloseDraft,
}: {
  automation: Automation | null;
  automationId: string;
  editing: boolean;
  error: Error | null;
  loading: boolean;
  onRefresh: () => void;
  onCloseDraft?: () => void;
}) => (
  <>
    <PageHeader
      title={automation?.definition.name ?? "Automation"}
      description={
        <span className="text-label font-mono break-all">{automationId}</span>
      }
    >
      {automation && (
        <StatusChip
          status={automation.definition.enabled ? "enabled" : "disabled"}
        />
      )}
      {automation && (
        <StatusChip label="revision" status={String(automation.revision)} />
      )}
      <Button size="default" variant="outline" onClick={onRefresh}>
        Refresh
      </Button>
      {loading && (
        <span className="text-muted-foreground text-label">Loading…</span>
      )}
    </PageHeader>
    {error && <ErrorBox error={error} />}
    {editing && <Canvas automationEditing onClose={onCloseDraft} />}
  </>
);

const AutomationEnablementCard = ({
  automation,
  conflictNotice,
  editing,
  error,
  loading,
  onToggle,
  stepSummary,
  toggleError,
  toggling,
}: {
  automation: Automation;
  conflictNotice: string | null;
  editing: boolean;
  error: Error | null;
  loading: boolean;
  onToggle: (enabled: boolean) => void;
  stepSummary: ReturnType<typeof describeAutomationSteps>;
  toggleError: Error | null;
  toggling: boolean;
}) => {
  const { definition } = automation;
  return (
    <Card size="compact">
      <Facts
        rows={[
          ["Name", definition.name],
          ["Enabled", definition.enabled ? "yes" : "no"],
          ["Revision", String(automation.revision)],
          ["Created at", automation.created_at],
          ["Updated at", automation.updated_at],
          ["Triggers", String(definition.triggers.length)],
          ["Defined Steps", String(stepSummary.stepCount)],
          ["Commands", String(stepSummary.commandCount)],
          ["Notifications", String(stepSummary.notificationCount)],
          ["Functions", String(stepSummary.functionCount)],
        ]}
      />
      <div className="mt-3 flex items-center gap-2">
        <Switch
          id="automation-enabled"
          checked={definition.enabled}
          // Disabled through the PUT and its authoritative reload
          // (`toggling`), and whenever the definition is loading or
          // failed: a stale revision must not stay actionable.
          disabled={editing || toggling || loading || error !== null}
          onCheckedChange={onToggle}
        />
        <Label htmlFor="automation-enabled" className="text-label">
          Enabled
        </Label>
        <span className="text-muted-foreground text-label font-mono">
          PUT /v1/automations/{"{id}"}
        </span>
      </div>
      <p className="text-muted-foreground text-label mt-1.5">
        Enablement governs automatic Runs; a manual Run works either way.
      </p>
      {conflictNotice && (
        <p
          role="alert"
          className="border-warning/35 bg-warning/10 text-warning text-label mt-2 rounded-md border px-2 py-1.5"
        >
          {conflictNotice}
        </p>
      )}
      {toggleError && <ErrorBox error={toggleError} />}
    </Card>
  );
};

const AutomationManualRunCard = ({
  editing,
  labels,
  onRun,
  runError,
  runStarting,
  startedRun,
}: {
  editing: boolean;
  labels: ReadonlyMap<string, string>;
  onRun: () => void;
  runError: Error | null;
  runStarting: boolean;
  startedRun: AutomationRun | null;
}) => (
  <Card size="compact">
    <div className="flex items-center justify-between gap-2">
      <span className="text-label font-medium">Manual Run</span>
      <span className="text-muted-foreground text-label font-mono">
        POST /v1/automations/{"{id}"}/runs
      </span>
    </div>
    <p className="text-muted-foreground text-label mt-1.5">
      Starts one Run from the current definition snapshot. Each accepted request
      creates a distinct Run and there is no idempotency key, so an unconfirmed
      request is never retried: check the history before running again.
    </p>
    <div className="mt-3">
      <Button
        size="default"
        disabled={runStarting || !!editing}
        onClick={onRun}
      >
        {runStarting ? "Starting…" : "Run now"}
      </Button>
    </div>
    {runError && (
      <div className="mt-3">
        <ErrorBox error={runError} />
        <p className="text-muted-foreground text-label">
          {manualRunFailure(runError) ??
            "No Run outcome was confirmed for this request."}{" "}
          The POST was issued once and not retried.
        </p>
      </div>
    )}
    {startedRun && (
      <div className="mt-3">
        <AutomationRunView run={startedRun} labels={labels} />
        {startedRun.status === "running" && (
          <p className="text-muted-foreground text-label mt-2">
            Steps continue in the background: refresh the history below to
            follow them.
          </p>
        )}
      </div>
    )}
  </Card>
);

const AutomationDefinitionSections = ({
  definition,
  labels,
  stepSummary,
}: {
  definition: AutomationDefinition;
  labels: ReadonlyMap<string, string>;
  stepSummary: ReturnType<typeof describeAutomationSteps>;
}) => (
  <>
    <Section title="Triggers">
      <AutomationTriggerList triggers={definition.triggers} labels={labels} />
    </Section>

    <Section title="Steps">
      <AutomationVariables
        variables={definition.variables}
        expressions={definition.variables_expr}
      />
      {stepSummary.functionCount > 0 && (
        <p className="text-muted-foreground text-label">
          Function Steps use the exact-JSON inspector in the stack builder.
          Untouched arguments and contract fingerprints are preserved.
        </p>
      )}
      {stepSummary.notificationCount > 0 && (
        <p className="text-muted-foreground text-label">
          Notification Steps are editable in the stack builder. Enablement
          changes preserve the complete definition.
        </p>
      )}
      {definition.conditions && (
        <>
          <h3 className="text-body font-semibold">Admission Conditions</h3>
          <AutomationConditionTree
            condition={definition.conditions}
            labels={labels}
          />
        </>
      )}
      <AutomationStepTree steps={definition.steps} labels={labels} />
    </Section>
  </>
);

const AutomationHistorySection = ({
  entry,
  entryError,
  entryLoading,
  history,
  historyError,
  historyLoading,
  labels,
  onNextPage,
  onRefresh,
  onSelectEntry,
  selectedEntryId,
  showHistory,
}: {
  entry: AutomationHistoryEntry | null;
  entryError: Error | null;
  entryLoading: boolean;
  history: Collection<AutomationHistorySummary> | null;
  historyError: Error | null;
  historyLoading: boolean;
  labels: ReadonlyMap<string, string>;
  onNextPage: (cursor: string) => void;
  onRefresh: () => void;
  onSelectEntry: (entryId: string) => void;
  selectedEntryId: string | undefined;
  showHistory: boolean;
}) => {
  if (!showHistory) {
    return null;
  }
  return (
    <Section title="History">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="default" variant="outline" onClick={onRefresh}>
          Refresh history
        </Button>
        {historyLoading && (
          <span className="text-muted-foreground text-label">Loading…</span>
        )}
      </div>
      <p className="text-muted-foreground text-label mt-1.5">
        Newest first, Runs and Skips together. Select an entry id to load its
        recorded detail.
      </p>
      {historyError && <ErrorBox error={historyError} />}
      {history && (
        <>
          <Table className="mt-3" aria-label="Automation history">
            <TableHeader>
              <TableRow>
                <TableHead>Kind</TableHead>
                <TableHead>Recorded</TableHead>
                <TableHead>Status / reason</TableHead>
                <TableHead>Revision</TableHead>
                <TableHead>Entry id</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {history.items.length === 0 && (
                <EmptyRow
                  colSpan={5}
                  message="No Runs or Skips recorded yet."
                />
              )}
              {history.items.map((summary) => (
                <TableRow
                  key={summary.id}
                  className={
                    summary.id === selectedEntryId ? "bg-muted/50" : undefined
                  }
                >
                  <TableCell>
                    <StatusChip status={summary.kind} />
                  </TableCell>
                  <TableCell className="text-muted-foreground text-label font-mono">
                    {summary.recorded_at}
                  </TableCell>
                  <TableCell>
                    <StatusChip status={historySummaryStatus(summary)} />
                  </TableCell>
                  <TableCell className="text-label font-mono">
                    {summary.revision}
                  </TableCell>
                  <TableCell className="max-w-[18rem]">
                    <Button
                      variant="link"
                      size="default"
                      className="font-mono"
                      aria-current={
                        summary.id === selectedEntryId ? "true" : undefined
                      }
                      onClick={() => onSelectEntry(summary.id)}
                    >
                      {summary.id}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {history.next_cursor && (
            <Button
              size="default"
              variant="outline"
              className="mt-2"
              onClick={() => {
                const cursor = history.next_cursor;
                if (cursor) {
                  onNextPage(cursor);
                }
              }}
            >
              Next page
            </Button>
          )}
        </>
      )}
      {selectedEntryId && (
        <Card size="compact" className="mt-4">
          {entryLoading && (
            <p className="text-muted-foreground text-label">Loading entry…</p>
          )}
          {entryError && <ErrorBox error={entryError} />}
          {entry && (
            <AutomationHistoryEntryView entry={entry} labels={labels} />
          )}
        </Card>
      )}
    </Section>
  );
};

const visibleAutomation = (
  data: Automation | null,
  error: Error | null
): Automation | null =>
  error !== null && problemCode(error) === "not_found" ? null : data;

const historyIsVisible = (
  data: Automation | null,
  history: Collection<AutomationHistorySummary> | null,
  historyError: Error | null
): boolean => data !== null || history !== null || historyError !== null;

const useDraftRefresh = ({
  automation,
  editing,
  refresh,
  workspace,
}: {
  automation: Automation | null;
  editing: boolean;
  refresh: () => Promise<void>;
  workspace: ReturnType<typeof useOptionalWorkspace>;
}) => {
  const savedDraftRevision = editing
    ? workspace?.automationDraft?.base?.revision
    : undefined;
  useEffect(() => {
    if (
      savedDraftRevision !== undefined &&
      savedDraftRevision !== automation?.revision &&
      !workspace?.automationDraft?.dirty
    ) {
      void refresh();
    }
  }, [
    savedDraftRevision,
    automation?.revision,
    workspace?.automationDraft?.dirty,
    refresh,
  ]);
};

const AutomationDefinitionView = ({
  automation,
  authoringDocument,
  conflictNotice,
  definition,
  editing,
  error,
  labels,
  loading,
  onRun,
  onToggle,
  runError,
  runStarting,
  startedRun,
  stepSummary,
  toggleError,
  toggling,
  workspace,
}: {
  automation: Automation;
  authoringDocument: ReturnType<typeof savedAuthoringDocument>;
  conflictNotice: string | null;
  definition: AutomationDefinition;
  editing: boolean;
  error: Error | null;
  labels: ReadonlyMap<string, string>;
  loading: boolean;
  onRun: () => void;
  onToggle: (enabled: boolean) => void;
  runError: Error | null;
  runStarting: boolean;
  startedRun: AutomationRun | null;
  stepSummary: ReturnType<typeof describeAutomationSteps>;
  toggleError: Error | null;
  toggling: boolean;
  workspace: ReturnType<typeof useOptionalWorkspace>;
}) => (
  <>
    {!editing && authoringDocument && (
      <AutomationCard
        document={authoringDocument}
        automationId={automation.id}
        entityLabels={labels}
        headingLevel={2}
        onEdit={
          workspace ? () => workspace.selectAutomation(automation) : undefined
        }
      />
    )}
    <div className="mt-3 grid gap-3 lg:grid-cols-2">
      <AutomationEnablementCard
        automation={automation}
        conflictNotice={conflictNotice}
        editing={editing}
        error={error}
        loading={loading}
        onToggle={onToggle}
        stepSummary={stepSummary}
        toggleError={toggleError}
        toggling={toggling}
      />
      <AutomationManualRunCard
        editing={editing}
        labels={labels}
        onRun={onRun}
        runError={runError}
        runStarting={runStarting}
        startedRun={startedRun}
      />
    </div>
    <AutomationDefinitionSections
      definition={definition}
      labels={labels}
      stepSummary={stepSummary}
    />
  </>
);

const useAutomationHistory = ({
  automationId,
  linkedEntryId,
}: {
  automationId: string;
  linkedEntryId: string | undefined;
}) => {
  const [historyCursor, setHistoryCursor] = useState<string | undefined>(
    undefined
  );
  const [historyNonce, setHistoryNonce] = useState(0);
  const selectionScope = `${automationId}:${linkedEntryId ?? ""}`;
  const [selection, setSelection] = useState<{
    scope: string;
    entryId: string | undefined;
  }>({ scope: selectionScope, entryId: linkedEntryId });
  if (selection.scope !== selectionScope) {
    setSelection({ scope: selectionScope, entryId: linkedEntryId });
  }
  const selectedEntryId =
    selection.scope === selectionScope ? selection.entryId : linkedEntryId;
  const selectEntry = (entryId: string | undefined) => {
    setSelection({ scope: selectionScope, entryId });
  };

  const historyKey = `automation-history-${automationId}-${historyNonce}-${historyCursor ?? "first"}`;
  const {
    data: history,
    error: historyError,
    loading: historyLoading,
    refresh: refreshHistory,
  } = useApi(historyKey, () =>
    automationFetch<Collection<AutomationHistorySummary>>(
      automationHistoryPath(automationId, historyCursor)
    )
  );

  const {
    data: entry,
    error: entryError,
    loading: entryLoading,
    refresh: refreshEntry,
  } = useApi<AutomationHistoryEntry | null>(
    `automation-history-entry-${automationId}-${selectedEntryId ?? "none"}`,
    () =>
      selectedEntryId
        ? automationFetch<AutomationHistoryEntry>(
            `/v1/automations/${automationId}/history/${selectedEntryId}`
          )
        : Promise.resolve(null)
  );

  /** Refetch the history summaries and, when an entry is selected, that
      entry's recorded detail too: a Run still executing changes its Step
      statuses after the summary row was read, so refreshing only the list would
      leave the visible detail stale. Explicit user action only; never polled. */
  const refreshHistoryAndSelection = () => {
    void refreshHistory();
    if (selectedEntryId) {
      void refreshEntry();
    }
  };

  /** Move to the next history page and drop the previous page's selection: the
      selected entry belongs to the page being left, so keeping it selected
      would show a detail row that no longer appears in the list. */
  const goToNextHistoryPage = (cursor: string) => {
    setHistoryCursor(cursor);
    selectEntry(undefined);
  };

  return {
    entry,
    entryError,
    entryLoading,
    goToNextHistoryPage,
    history,
    historyError,
    historyLoading,
    refreshHistoryAndSelection,
    selectEntry,
    selectedEntryId,
    setHistoryCursor,
    setHistoryNonce,
  };
};

const useAutomationActions = ({
  automation,
  automationId,
  refresh,
  selectEntry,
  setHistoryCursor,
  setHistoryNonce,
}: {
  automation: Automation | null;
  automationId: string;
  refresh: () => Promise<void>;
  selectEntry: (entryId: string | undefined) => void;
  setHistoryCursor: (cursor: string | undefined) => void;
  setHistoryNonce: Dispatch<SetStateAction<number>>;
}) => {
  const [runStarting, setRunStarting] = useState(false);
  const [startedRun, setStartedRun] = useState<AutomationRun | null>(null);
  const [runError, setRunError] = useState<Error | null>(null);
  const [toggling, setToggling] = useState(false);
  const [toggleError, setToggleError] = useState<Error | null>(null);
  const [conflictNotice, setConflictNotice] = useState<string | null>(null);
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);

  /** Start exactly one manual Run. Each accepted POST creates a distinct Run
      and the endpoint has no idempotency key, so this issues at most one
      request and never retries an ambiguous failure. */
  const runNow = async () => {
    setRunStarting(true);
    setRunError(null);
    setStartedRun(null);
    let run: AutomationRun | null = null;
    let failure: Error | null = null;
    try {
      run = await automationFetch<AutomationRun>(
        `/v1/automations/${automationId}/runs`,
        {
          method: "POST",
        }
      );
    } catch (error_) {
      failure = error_ instanceof Error ? error_ : new Error(String(error_));
    }
    if (!active.current) {
      return;
    }
    if (run) {
      setStartedRun(run);
      // The new Run is the newest history row: restart the history at page one.
      setHistoryCursor(undefined);
      setHistoryNonce((nonce) => nonce + 1);
      selectEntry(undefined);
      void refresh();
    }
    setRunStarting(false);
    if (failure) {
      setRunError(failure);
    }
  };

  /** Enable or disable through a full replacement: the API takes the whole
      definition plus the revision this change was based on. A stale revision is
      reported and the current definition is refetched rather than overwritten. */
  const setEnabled = async (enabled: boolean) => {
    // A definition reported absent (404) is not a baseline to mutate from, and
    // a request already in flight owns the switch until it settles.
    if (!automation || toggling) {
      return;
    }
    const current = automation;
    setToggling(true);
    setToggleError(null);
    setConflictNotice(null);
    let failure: Error | null = null;
    try {
      await automationFetch<Automation>(`/v1/automations/${automationId}`, {
        method: "PUT",
        body: serializeAutomationEnablement(current, enabled),
      });
      if (!active.current) {
        return;
      }
      // Reload the authoritative definition and hold the switch disabled until
      // that read settles: the PUT response is not the truth shown, and a stale
      // revision must not stay actionable while the reload is pending or after
      // it fails.
      await refresh();
    } catch (error_) {
      failure = error_ instanceof Error ? error_ : new Error(String(error_));
    }
    if (!active.current) {
      return;
    }
    if (failure && problemCode(failure) === "revision_conflict") {
      setConflictNotice(
        "Revision conflict (409 revision_conflict): another writer replaced this Automation, so " +
          "enablement was not changed. The current definition was reloaded."
      );
      await refresh();
      if (!active.current) {
        return;
      }
    } else if (failure) {
      setToggleError(failure);
    }
    setToggling(false);
  };

  return {
    conflictNotice,
    runError,
    runNow,
    runStarting,
    setEnabled,
    startedRun,
    toggleError,
    toggling,
  };
};

const AutomationDetailContent = ({
  automationId,
}: {
  automationId: string;
}) => {
  const workspace = useOptionalWorkspace();
  const [searchParams] = useSearchParams();
  const linkedEntryId = searchParams.get("history_id") ?? undefined;

  const { data, error, loading, refresh } = useApi(
    `automation-${automationId}`,
    () => automationFetch<Automation>(`/v1/automations/${automationId}`)
  );

  const {
    entry,
    entryError,
    entryLoading,
    goToNextHistoryPage,
    history,
    historyError,
    historyLoading,
    refreshHistoryAndSelection,
    selectEntry,
    selectedEntryId,
    setHistoryCursor,
    setHistoryNonce,
  } = useAutomationHistory({ automationId, linkedEntryId });

  // A failed definition refresh keeps the previous read in `data`, but a
  // `not_found` means the current Automation is gone: treat it as absent for the
  // header, definition, actions, and raw JSON. Retained history is read from its
  // own endpoint and stays visible.
  const automation = visibleAutomation(data, error);
  const definition: AutomationDefinition | undefined = automation?.definition;

  const {
    conflictNotice,
    runError,
    runNow,
    runStarting,
    setEnabled,
    startedRun,
    toggleError,
    toggling,
  } = useAutomationActions({
    automation,
    automationId,
    refresh,
    selectEntry,
    setHistoryCursor,
    setHistoryNonce,
  });

  // A definition 404 must not hide retained history, so the History section
  // renders when either read produced something to show.
  const showHistory = historyIsVisible(data, history, historyError);
  const labels = useEntityLabels(
    useMemo(
      () => automationReferenceIds(definition, entry, startedRun),
      [definition, entry, startedRun]
    )
  );
  const stepSummary = describeAutomationSteps(definition?.steps ?? []);
  const authoringDocument = useMemo(
    () => savedAuthoringDocument(automation),
    [automation]
  );
  const editing =
    workspace?.automationDraft?.base?.automation_id === automationId;
  useDraftRefresh({ automation, editing, refresh, workspace });

  return (
    <div>
      <AutomationDetailHeader
        automation={automation}
        automationId={automationId}
        editing={editing}
        error={error}
        loading={loading}
        onRefresh={refresh}
        onCloseDraft={workspace?.closeDraft}
      />
      {automation && definition && (
        <AutomationDefinitionView
          automation={automation}
          authoringDocument={authoringDocument}
          conflictNotice={conflictNotice}
          definition={definition}
          editing={editing}
          error={error}
          labels={labels}
          loading={loading}
          onRun={runNow}
          onToggle={setEnabled}
          runError={runError}
          runStarting={runStarting}
          startedRun={startedRun}
          stepSummary={stepSummary}
          toggleError={toggleError}
          toggling={toggling}
          workspace={workspace}
        />
      )}
      <AutomationHistorySection
        entry={entry}
        entryError={entryError}
        entryLoading={entryLoading}
        history={history}
        historyError={historyError}
        historyLoading={historyLoading}
        labels={labels}
        onNextPage={goToNextHistoryPage}
        onRefresh={refreshHistoryAndSelection}
        onSelectEntry={selectEntry}
        selectedEntryId={selectedEntryId}
        showHistory={showHistory}
      />
      {automation && definition && (
        <RawJson
          value={automation}
          code={automationJsonText(automation)}
          title="Raw automation JSON"
        />
      )}
    </div>
  );
};

const AutomationDetailPage = () => {
  const { automationId = "" } = useParams();
  const baseVersion = useBaseUrlVersion();
  return (
    <AutomationDetailContent
      key={`${automationId}:${baseVersion}`}
      automationId={automationId}
    />
  );
};

export default AutomationDetailPage;
