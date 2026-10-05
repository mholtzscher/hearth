import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Link as RouterLink, useParams } from "react-router-dom";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { fetchAllCollectionPages } from "../api/pagination.ts";
import type {
  Automation,
  AutomationBranchDecision,
  AutomationComparison,
  AutomationAdmissionCause,
  AutomationConditionDecision,
  AutomationConditionEvaluation,
  AutomationDefinition,
  AutomationDelayExecution,
  AutomationHistoryEntry,
  AutomationHistorySummary,
  AutomationRun,
  AutomationSkip,
  AutomationSkipReason,
  AutomationStepAttempt,
  AutomationTrigger,
  Collection,
  AutomationDeviceFact,
  Entity,
} from "../api/types.ts";
import {
  EmptyRow,
  ErrorBox,
  Facts,
  MonoId,
  RawJson,
  Section,
  StatusChip,
  linkClass,
  problemCode,
} from "../components/common.tsx";
import { Button } from "../components/ui/button.tsx";
import { Card, CardContent } from "../components/ui/card.tsx";
import { Label } from "../components/ui/label.tsx";
import { Switch } from "../components/ui/switch.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/ui/table.tsx";
import AutomationStepTree, { AutomationConditionTree } from "./AutomationStepTree.tsx";
import { assertNever, automationDurationText, automationEntityIds, describeAutomationSteps } from "./automation-step-tree.ts";

const HISTORY_PAGE_LIMIT = 50;

/** One page path for the newest-first Automation history keyset. */
function automationHistoryPath(automationId: string, cursor: string | undefined): string {
  return `/v1/automations/${automationId}/history?limit=${HISTORY_PAGE_LIMIT}${
    cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""
  }`;
}

/** Best-effort Entity names for the referenced IDs. The lookup reads the
    Entities collection once per server and keeps only the referenced IDs; a
    failed read leaves references labeled by ID instead of blocking the page.
    `entityIds` is joined for effect identity, so a re-render with the same IDs
    does not refetch. */
function useEntityLabels(entityIds: string[]): ReadonlyMap<string, string> {
  const [labels, setLabels] = useState<ReadonlyMap<string, string>>(new Map());
  const baseVersion = useBaseUrlVersion();
  const wanted = entityIds.join(",");
  useEffect(() => {
    const ids = new Set(wanted ? wanted.split(",") : []);
    if (ids.size === 0) {
      setLabels(new Map());
      return;
    }
    let cancelled = false;
    void fetchAllCollectionPages<Entity>("/v1/entities")
      .then((collection) => {
        if (cancelled) return;
        const found = new Map<string, string>();
        for (const entity of collection.items) {
          if (ids.has(entity.id)) found.set(entity.id, entity.name);
        }
        setLabels(found);
      })
      .catch(() => {
        if (!cancelled) setLabels(new Map());
      });
    return () => {
      cancelled = true;
    };
  }, [wanted, baseVersion]);
  return labels;
}

/** Entity reference in a Trigger or Step: always a link to the Entity page,
    labeled with the resolved name when the lookup found one. */
function AutomationEntityLink({
  entityId,
  labels,
}: {
  entityId: string;
  labels: ReadonlyMap<string, string>;
}) {
  const name = labels.get(entityId);
  return (
    <RouterLink to={`/entities/${entityId}`} className={linkClass} title={entityId}>
      {name ?? entityId}
    </RouterLink>
  );
}

/** One comparison as `pointer operator operand`, e.g. `value eq true`. */
function comparisonText(comparison: AutomationComparison): string {
  return `${comparison.value_pointer} ${comparison.operator} ${JSON.stringify(comparison.operand)}`;
}

function triggerDescription(trigger: AutomationTrigger): ReactNode {
  switch (trigger.kind) {
    case "cron": return trigger.expression;
    case "entity_event": return <>event name {trigger.event_name}</>;
    case "held_state": return <>held for <span>{trigger.for_seconds} seconds</span> · comparisons <span>{trigger.comparisons.map(comparisonText).join(" and ")}</span></>;
    case "observation": return <>
      dispositions {(trigger.dispositions ?? []).join(", ") || "—"}
      {(trigger.comparisons?.length ?? 0) > 0 && <> · comparisons {trigger.comparisons?.map(comparisonText).join(" and ")}</>}
      {(trigger.previous_comparisons?.length ?? 0) > 0 && <> · previous comparisons {trigger.previous_comparisons?.map(comparisonText).join(" and ")}</>}
    </>;
    default: return assertNever(trigger);
  }
}

/** Readable Triggers: each one is an alternative reason the Automation starts. */
function AutomationTriggerList({
  triggers,
  labels,
}: {
  triggers: AutomationTrigger[];
  labels: ReadonlyMap<string, string>;
}) {
  if (triggers.length === 0) {
    return <p className="text-sm text-muted-foreground">No Triggers.</p>;
  }
  return (
    <ul className="mt-1 grid gap-2">
      {triggers.map((trigger) => (
        <li key={trigger.id} className="rounded-lg border px-3 py-2">
          <div className="flex flex-wrap items-center gap-2">
            <StatusChip status={trigger.kind} />
            <span className="font-mono text-xs">{trigger.id}</span>
            {trigger.kind !== "cron" && (
              <>
                <span className="text-sm text-muted-foreground">matches</span>
                <AutomationEntityLink entityId={trigger.entity_id} labels={labels} />
              </>
            )}
          </div>
          <p className="mt-1 font-mono text-xs">{triggerDescription(trigger)}</p>
        </li>
      ))}
    </ul>
  );
}

function AutomationDelayExecutionTable({ delays }: { delays: AutomationDelayExecution[] }) {
  return (
    <Table className="mt-1" aria-label="Delay executions">
      <TableHeader>
        <TableRow>
          <TableHead>Reached position</TableHead>
          <TableHead>Step id</TableHead>
          <TableHead>Duration</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Started</TableHead>
          <TableHead>Expected due (diagnostic)</TableHead>
          <TableHead>Completed</TableHead>
          <TableHead>Interruption reason</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {delays.length === 0 && <EmptyRow colSpan={8} message="This Run reached no delay." />}
        {[...delays].sort((a, b) => a.position - b.position).map((delay) => (
          <TableRow key={delay.position}>
            <TableCell>{delay.position}</TableCell>
            <TableCell className="font-mono text-xs">{delay.step_id}</TableCell>
            <TableCell>{automationDurationText(delay.duration_ms)}</TableCell>
            <TableCell><StatusChip status={delay.status} /></TableCell>
            <TableCell className="font-mono text-xs">{delay.started_at}</TableCell>
            <TableCell className="font-mono text-xs">{delay.due_at}</TableCell>
            <TableCell className="font-mono text-xs">{delay.completed_at ?? "—"}</TableCell>
            <TableCell className="font-mono text-xs">{delay.failure_code ?? "—"}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function AutomationBranchDecisionTable({ decisions }: { decisions: AutomationBranchDecision[] }) {
  return (
    <Table className="mt-1" aria-label="Branch selection decisions">
      <TableHeader>
        <TableRow>
          <TableHead>Decision position</TableHead>
          <TableHead>Step id</TableHead>
          <TableHead>Selected arm / outcome</TableHead>
          <TableHead>Evaluated at</TableHead>
          <TableHead>Recorded evidence</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {decisions.length === 0 && <EmptyRow colSpan={5} message="No branch decisions recorded." />}
        {[...decisions].sort((a, b) => a.position - b.position).map((decision) => (
          <TableRow key={decision.position}>
            <TableCell>{decision.position}</TableCell>
            <TableCell className="font-mono text-xs">{decision.step_id}</TableCell>
            <TableCell>
              {branchOutcome(decision)}
              {"failure_code" in decision && <p className="font-mono text-xs">{decision.failure_code}</p>}
            </TableCell>
            <TableCell className="font-mono text-xs">{decision.evaluated_at}</TableCell>
            <TableCell>
              <details>
                <summary className="cursor-pointer">Evidence for {decision.step_id}</summary>
                <pre className="mt-2 overflow-auto whitespace-pre-wrap font-mono text-xs">
                  {JSON.stringify(decision.evaluations, null, 2)}
                </pre>
                {decision.evaluations.map((item, index) => <ConditionEvaluationView key={index} evaluation={item.evaluation} />)}
              </details>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function branchOutcome(decision: AutomationBranchDecision): string {
  switch (decision.kind) {
    case "if":
      switch (decision.outcome) {
        case "then":
        case "else":
        case "no_match":
        case "unknown":
        case "error": return decision.outcome;
        default: return assertNever(decision);
      }
    case "choose":
      switch (decision.outcome) {
        case "branch": return `Alternative ${decision.selected_branch_id}`;
        case "default":
        case "no_match":
        case "unknown":
        case "error": return decision.outcome;
        default: return assertNever(decision);
      }
    default: return assertNever(decision);
  }
}

function attemptEvidence(attempt: AutomationStepAttempt) {
  switch (attempt.status) {
    case "not_attempted": return { started: "—", completed: "—", failure: "—", verifiedCommandId: undefined };
    case "running": return { started: attempt.started_at, completed: "—", failure: "—", verifiedCommandId: undefined };
    case "satisfied":
    case "dispatched": return { started: attempt.started_at, completed: attempt.completed_at, failure: "—", verifiedCommandId: attempt.verified_command_id };
    case "failed":
    case "interrupted": return { started: attempt.started_at, completed: attempt.completed_at, failure: attempt.failure_code, verifiedCommandId: attempt.verified_command_id };
    default: return assertNever(attempt);
  }
}

/** Ordered Step attempts of one Run. Only ownership-verified Command IDs are
    exposed, so an attempt with no verified Command shows a dash. A verified
    Command links to its durable audit record on the Commands tab. */
function AutomationStepAttemptTable({ attempts }: { attempts: AutomationStepAttempt[] }) {
  return (
    <Table className="mt-1" aria-label="Command Step attempts">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead>Position</TableHead>
          <TableHead>Step id</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Verified command</TableHead>
          <TableHead>Failure</TableHead>
          <TableHead>Started</TableHead>
          <TableHead>Completed</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {attempts.length === 0 && <EmptyRow colSpan={7} message="This Run recorded no Step attempts." />}
        {attempts.map((attempt) => {
          const evidence = attemptEvidence(attempt);
          return (
          <TableRow key={`${attempt.position}-${attempt.step_id}`}>
            <TableCell className="font-mono text-xs text-muted-foreground">
              {attempt.position}
            </TableCell>
            <TableCell className="font-mono text-xs">{attempt.step_id}</TableCell>
            <TableCell>
              <StatusChip status={attempt.status} />
            </TableCell>
            <TableCell className="max-w-[16rem]">
              {evidence.verifiedCommandId ? (
                <RouterLink
                  to={`/commands?command_id=${evidence.verifiedCommandId}`}
                  className={linkClass}
                  title={evidence.verifiedCommandId}
                >
                  <MonoId value={evidence.verifiedCommandId} className="text-muted-foreground" />
                </RouterLink>
              ) : (
                <MonoId value="—" className="text-muted-foreground" />
              )}
            </TableCell>
            <TableCell className="font-mono text-xs">{evidence.failure}</TableCell>
            <TableCell className="font-mono text-xs text-muted-foreground">
              {evidence.started}
            </TableCell>
            <TableCell className="font-mono text-xs text-muted-foreground">
              {evidence.completed}
            </TableCell>
          </TableRow>
          );
        })}
      </TableBody>
    </Table>
  );
}

/** Retained Fact evidence rows, shared by a Run and a Skip. An Entity Event
    Fact reports its event name and carries no Observation value. */
function deviceFactRows(
  fact: AutomationDeviceFact,
  labels: ReadonlyMap<string, string>,
): [string, ReactNode][] {
  const common: [string, ReactNode][] = [
    ["Fact id", fact.fact_id],
    ["Fact family", fact.family],
    ["Fact entity", <AutomationEntityLink entityId={fact.entity_id} labels={labels} />],
    ["Fact emitted", fact.emitted_at],
  ];
  switch (fact.family) {
    case "observation": return [...common,
      ["Observation id", fact.observation_id], ["Disposition", fact.disposition],
      ["Reported value", JSON.stringify(fact.value)],
      ["Previous value", "previous_value" in fact ? JSON.stringify(fact.previous_value) : "— (not reported)"],
    ];
    case "entity_event": return [...common, ["Event id", fact.event_id], ["Event name", fact.name],
      ["Reported value", "— (an Entity Event Fact carries no value)"]];
    default: return assertNever(fact);
  }
}

function causeRows(cause: AutomationAdmissionCause, labels: ReadonlyMap<string, string>): [string, ReactNode][] {
  switch (cause.kind) {
    case "manual": return [["Admission cause", "manual"]];
    case "schedule": return [["Admission cause", "schedule"]];
    case "device_fact": return deviceFactRows(cause.fact, labels);
    case "held_state": return [["Held trigger", cause.evidence.trigger_id], ["Hold started", cause.evidence.started_at], ["Hold due", cause.evidence.due_at]];
    default: return assertNever(cause);
  }
}

function ConditionEvaluationView({ evaluation }: { evaluation: AutomationConditionEvaluation }) {
  return <ul className="text-xs">{evaluation.nodes.map((node) => {
    switch (node.kind) {
      case "trigger": return <li key={node.id}>{node.id}: {node.result} · matched triggers {node.matched_trigger_ids.join(", ") || "none"}</li>;
      case "entity_state": return <li key={node.id}>{node.id}: {node.result}
        {node.result === "unknown" && <> · {node.unknown_reason}</>}
        {"selected_value" in node && <> · selected value {JSON.stringify(node.selected_value)}</>}
        {"observation_id" in node && <> · observation {node.observation_id} at {node.observed_at}</>}
      </li>;
      default: return assertNever(node);
    }
  })}</ul>;
}

function ConditionDecisionView({ decision }: { decision: AutomationConditionDecision }) {
  switch (decision.mode) {
    case "not_configured": return <p>Admission Conditions not configured.</p>;
    case "not_evaluated": return <p>Admission Conditions not evaluated.</p>;
    case "bypassed": return <p>Admission Conditions bypassed.</p>;
    case "evaluated": return <><p>Admission Conditions: {decision.evaluation.result}</p><ConditionEvaluationView evaluation={decision.evaluation} /></>;
    default: return assertNever(decision);
  }
}

/** Plain-language meaning of a Skip reason (GLOSSARY.md: Automation Skip). */
function skipReasonNote(reason: AutomationSkipReason): string {
  switch (reason) {
    case "automation_busy":
      return "Matched while this Automation already had a running Run, so no second Run started.";
    case "stale_fact":
      return "Matched a Fact that was already too old to run, so no Run started. A Skip never queues execution.";
    case "conditions_false":
      return "Conditions were false, so no Run started.";
    case "conditions_unknown":
      return "Conditions could not be confirmed, so no Run started.";
    default: return assertNever(reason);
  }
}

function runOutcomeRows(run: AutomationRun): [string, ReactNode][] {
  switch (run.status) {
    case "running": return [["Completed at", "— (not complete yet)"], ["Failure", "—"]];
    case "succeeded": return [["Completed at", run.completed_at], ["Failure", "—"]];
    case "failed":
    case "interrupted": return [["Completed at", run.completed_at], ["Failure", run.failure_code]];
    default: return assertNever(run);
  }
}

function historySummaryStatus(summary: AutomationHistorySummary): string {
  switch (summary.kind) {
    case "run": return summary.status;
    case "skip": return summary.reason;
    default: return assertNever(summary);
  }
}

/** One Run: header facts, Fact evidence, ordered Step attempts, and the
    immutable definition snapshot the Run executed. */
function AutomationRunView({
  run,
  labels,
}: {
  run: AutomationRun;
  labels: ReadonlyMap<string, string>;
}) {
  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <StatusChip label="run" status={run.status} />
        <StatusChip label="cause" status={run.cause.kind} />
        <span className="text-xs text-muted-foreground">revision {run.revision}</span>
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
      <div className="mt-2"><Facts rows={causeRows(run.cause, labels)} /></div>
      <ConditionDecisionView decision={run.condition_decision} />
      <h3 className="mt-4 text-sm font-medium">Step attempts</h3>
      <p className="text-xs text-muted-foreground">Command Step attempts record execution outcomes. Positions are zero-based.</p>
      <AutomationStepAttemptTable attempts={run.steps} />
      <h3 className="mt-4 text-sm font-medium">Branch decisions</h3>
      <p className="text-xs text-muted-foreground">Decisions record arm selection, not command completion. Commands may remain not_attempted after a selection.</p>
      <AutomationBranchDecisionTable decisions={run.branch_decisions} />
      <h3 className="mt-4 text-sm font-medium">Delay executions</h3>
      <p className="text-xs text-muted-foreground">
        Positions are zero-based reached-delay order, independent of Commands and branches.
        Expected due times are diagnostic UTC timestamps, not countdowns or timer authority.
        Shutdown or restart interrupts waiting. No wait resumes and no remaining Step executes after interruption.
      </p>
      <AutomationDelayExecutionTable delays={run.delays} />
      <h3 className="mt-4 text-sm font-medium">Definition snapshot</h3>
      <AutomationTriggerList triggers={run.snapshot.triggers} labels={labels} />
      {run.snapshot.conditions && <AutomationConditionTree condition={run.snapshot.conditions} labels={labels} />}
      <AutomationStepTree steps={run.snapshot.steps} labels={labels} />
      <RawJson value={run} title="Raw Run JSON" />
    </div>
  );
}

/** One Skip: the matched Fact, its reason, and the Trigger snapshots that matched. */
function AutomationSkipView({
  skip,
  labels,
}: {
  skip: AutomationSkip;
  labels: ReadonlyMap<string, string>;
}) {
  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <StatusChip label="skip" status={skip.reason} />
        <StatusChip label="cause" status={skip.cause.kind} />
        <span className="text-xs text-muted-foreground">revision {skip.revision}</span>
      </div>
      <p className="mt-1.5 text-xs text-muted-foreground">{skipReasonNote(skip.reason)}</p>
      <Facts
        rows={[
          ["Skip id", skip.id],
          ["Automation", skip.automation_name],
          ["Skipped at", skip.skipped_at],
          ...causeRows(skip.cause, labels),
        ]}
      />
      <ConditionDecisionView decision={skip.condition_decision} />
      <h3 className="mt-4 text-sm font-medium">Matched Triggers</h3>
      <AutomationTriggerList triggers={skip.matched_triggers} labels={labels} />
      <RawJson value={skip} title="Raw Skip JSON" />
    </div>
  );
}

/** One retained history entry: exactly one of Run or Skip is present. */
function AutomationHistoryEntryView({
  entry,
  labels,
}: {
  entry: AutomationHistoryEntry;
  labels: ReadonlyMap<string, string>;
}) {
  switch (entry.kind) {
    case "run": return <AutomationRunView run={entry.run} labels={labels} />;
    case "skip": return <AutomationSkipView skip={entry.skip} labels={labels} />;
    default: return assertNever(entry);
  }
}

/** Plain-language explanation for a failed manual Run POST. The POST is
    non-idempotent, so the caller needs to know whether a Run may exist. */
function manualRunFailure(error: unknown): string | null {
  switch (problemCode(error)) {
    case "automation_busy":
      return "This Automation already has a running Run, so no Run was created (409 automation_busy).";
    case "admission_unavailable":
      return "Automation admission is closed, so no Run was created (503 admission_unavailable).";
    case "not_found":
      return "This Automation no longer exists (404 not_found), so no Run was created.";
    default:
      return null;
  }
}

export default function AutomationDetailPage() {
  const { automationId = "" } = useParams();
  // The route reuses this page across Automations and the toolbar can switch
  // servers under the same Automation ID, so in-flight requests compare against
  // these refs before writing state.
  const automationIdRef = useRef(automationId);
  automationIdRef.current = automationId;
  const baseVersion = useBaseUrlVersion();
  const baseVersionRef = useRef(baseVersion);
  baseVersionRef.current = baseVersion;

  const { data, error, loading, refresh } = useApi(`automation-${automationId}`, () =>
    apiFetch<Automation>(`/v1/automations/${automationId}`),
  );

  const [runStarting, setRunStarting] = useState(false);
  const [startedRun, setStartedRun] = useState<AutomationRun | null>(null);
  const [runError, setRunError] = useState<Error | null>(null);
  const [toggling, setToggling] = useState(false);
  const [toggleError, setToggleError] = useState<Error | null>(null);
  const [conflictNotice, setConflictNotice] = useState<string | null>(null);
  const [historyCursor, setHistoryCursor] = useState<string | undefined>(undefined);
  const [historyNonce, setHistoryNonce] = useState(0);
  const [selectedEntryId, setSelectedEntryId] = useState<string | undefined>(undefined);

  useEffect(() => {
    // A new Automation or server is a new scope: drop the previous manual-Run
    // outcome, toggle state, conflict notice, and history selection instead of
    // showing them under the new scope.
    setRunStarting(false);
    setStartedRun(null);
    setRunError(null);
    setToggling(false);
    setToggleError(null);
    setConflictNotice(null);
    setHistoryCursor(undefined);
    setHistoryNonce(0);
    setSelectedEntryId(undefined);
  }, [automationId, baseVersion]);

  const historyKey = `automation-history-${automationId}-${historyNonce}-${historyCursor ?? "first"}`;
  const {
    data: history,
    error: historyError,
    loading: historyLoading,
    refresh: refreshHistory,
  } = useApi(historyKey, () =>
    apiFetch<Collection<AutomationHistorySummary>>(
      automationHistoryPath(automationId, historyCursor),
    ),
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
        ? apiFetch<AutomationHistoryEntry>(
            `/v1/automations/${automationId}/history/${selectedEntryId}`,
          )
        : Promise.resolve(null),
  );

  // A failed definition refresh keeps the previous read in `data`, but a
  // `not_found` means the current Automation is gone: treat it as absent for the
  // header, definition, actions, and raw JSON. Retained history is read from its
  // own endpoint and stays visible.
  const definitionGone = error !== null && problemCode(error) === "not_found";
  const automation = definitionGone ? null : data;
  const definition: AutomationDefinition | undefined = automation?.definition;
  // A definition 404 must not hide retained history, so the History section
  // renders when either read produced something to show.
  const showHistory = data !== null || history !== null || historyError !== null;
  const labels = useEntityLabels(
    useMemo(() => {
      const ids = new Set<string>();
      for (const snapshot of [definition, entry?.kind === "run" ? entry.run.snapshot : undefined, startedRun?.snapshot]) {
        if (snapshot) for (const id of automationEntityIds(snapshot)) ids.add(id);
      }
      for (const trigger of entry?.kind === "skip" ? entry.skip.matched_triggers : []) {
        if (trigger.kind !== "cron") ids.add(trigger.entity_id);
      }
      for (const cause of [entry?.kind === "run" ? entry.run.cause : entry?.skip.cause, startedRun?.cause]) {
        if (cause?.kind === "device_fact") ids.add(cause.fact.entity_id);
      }
      return [...ids];
    }, [definition, entry, startedRun]),
  );
  const stepSummary = describeAutomationSteps(definition?.steps ?? []);

  /** Refetch the history summaries and, when an entry is selected, that
      entry's recorded detail too: a Run still executing changes its Step
      statuses after the summary row was read, so refreshing only the list would
      leave the visible detail stale. Explicit user action only; never polled. */
  function refreshHistoryAndSelection() {
    void refreshHistory();
    if (selectedEntryId) void refreshEntry();
  }

  /** Move to the next history page and drop the previous page's selection: the
      selected entry belongs to the page being left, so keeping it selected
      would show a detail row that no longer appears in the list. */
  function goToNextHistoryPage(cursor: string) {
    setHistoryCursor(cursor);
    setSelectedEntryId(undefined);
  }

  /** Start exactly one manual Run. Each accepted POST creates a distinct Run
      and the endpoint has no idempotency key, so this issues at most one
      request and never retries an ambiguous failure. */
  async function runNow() {
    const target = automationId;
    const targetBase = baseVersionRef.current;
    setRunStarting(true);
    setRunError(null);
    setStartedRun(null);
    try {
      const run = await apiFetch<AutomationRun>(`/v1/automations/${target}/runs`, {
        method: "POST",
      });
      if (automationIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      setStartedRun(run);
      // The new Run is the newest history row: restart the history at page one.
      setHistoryCursor(undefined);
      setHistoryNonce((nonce) => nonce + 1);
      setSelectedEntryId(undefined);
      void refresh();
    } catch (e) {
      if (automationIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      setRunError(e instanceof Error ? e : new Error(String(e)));
    } finally {
      if (automationIdRef.current === target && baseVersionRef.current === targetBase) {
        setRunStarting(false);
      }
    }
  }

  /** Enable or disable through a full replacement: the API takes the whole
      definition plus the revision this change was based on. A stale revision is
      reported and the current definition is refetched rather than overwritten. */
  async function setEnabled(enabled: boolean) {
    const target = automationId;
    const targetBase = baseVersionRef.current;
    // A definition reported absent (404) is not a baseline to mutate from, and
    // a request already in flight owns the switch until it settles.
    if (!automation || toggling) return;
    const current = automation;
    setToggling(true);
    setToggleError(null);
    setConflictNotice(null);
    try {
      await apiFetch<Automation>(`/v1/automations/${target}`, {
        method: "PUT",
        body: JSON.stringify({
          expected_revision: current.revision,
          definition: { ...current.definition, enabled },
        }),
      });
      if (automationIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      // Reload the authoritative definition and hold the switch disabled until
      // that read settles: the PUT response is not the truth shown, and a stale
      // revision must not stay actionable while the reload is pending or after
      // it fails.
      await refresh();
    } catch (e) {
      if (automationIdRef.current !== target || baseVersionRef.current !== targetBase) return;
      const failure = e instanceof Error ? e : new Error(String(e));
      if (problemCode(failure) === "revision_conflict") {
        setConflictNotice(
          "Revision conflict (409 revision_conflict): another writer replaced this Automation, so " +
            "enablement was not changed. The current definition was reloaded.",
        );
        await refresh();
      } else {
        setToggleError(failure);
      }
    } finally {
      if (automationIdRef.current === target && baseVersionRef.current === targetBase) {
        setToggling(false);
      }
    }
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-1 text-lg font-semibold">{automation?.definition.name ?? "Automation"}</h1>
        {automation && (
          <StatusChip
            label={automation.definition.enabled ? "enabled" : "disabled"}
            status={automation.definition.enabled ? "enabled" : "disabled"}
          />
        )}
        {automation && <StatusChip label="revision" status={String(automation.revision)} />}
        <Button size="sm" variant="outline" onClick={() => void refresh()}>
          Refresh
        </Button>
        {loading && <span className="text-sm text-muted-foreground">Loading…</span>}
      </div>
      <p className="mt-1 font-mono text-xs break-all text-muted-foreground">{automationId}</p>
      {error && <ErrorBox error={error} />}
      {automation && definition && (
        <>
          <div className="mt-3 grid gap-3 lg:grid-cols-2">
            <Card size="sm">
              <CardContent>
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
                  ]}
                />
                <div className="mt-3 flex items-center gap-2">
                  <Switch
                    id="automation-enabled"
                    checked={definition.enabled}
                    // Disabled through the PUT and its authoritative reload
                    // (`toggling`), and whenever the definition is loading or
                    // failed: a stale revision must not stay actionable.
                    disabled={toggling || loading || error !== null}
                    onCheckedChange={(checked) => void setEnabled(checked)}
                  />
                  <Label htmlFor="automation-enabled" className="text-sm">
                    Enabled
                  </Label>
                  <span className="font-mono text-xs text-muted-foreground">
                    PUT /v1/automations/{"{id}"}
                  </span>
                </div>
                <p className="mt-1.5 text-xs text-muted-foreground">
                  Enablement governs automatic Runs; a manual Run works either way.
                </p>
                {conflictNotice && (
                  <p
                    role="alert"
                    className="mt-2 rounded-lg border border-warning/35 bg-warning/10 px-2 py-1.5 text-xs text-warning"
                  >
                    {conflictNotice}
                  </p>
                )}
                {toggleError && <ErrorBox error={toggleError} />}
              </CardContent>
            </Card>

            <Card size="sm">
              <CardContent>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm font-medium">Manual Run</span>
                  <span className="font-mono text-xs text-muted-foreground">
                    POST /v1/automations/{"{id}"}/runs
                  </span>
                </div>
                <p className="mt-1.5 text-xs text-muted-foreground">
                  Starts one Run from the current definition snapshot. Each accepted request creates a
                  distinct Run and there is no idempotency key, so an unconfirmed request is never
                  retried: check the history before running again.
                </p>
                <div className="mt-3">
                  <Button size="sm" disabled={runStarting} onClick={() => void runNow()}>
                    {runStarting ? "Starting…" : "Run now"}
                  </Button>
                </div>
                {runError && (
                  <div className="mt-3">
                    <ErrorBox error={runError} />
                    <p className="text-xs text-muted-foreground">
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
                      <p className="mt-2 text-xs text-muted-foreground">
                        Steps continue in the background: refresh the history below to follow them.
                      </p>
                    )}
                  </div>
                )}
              </CardContent>
            </Card>
          </div>

          <Section title="Triggers">
            <AutomationTriggerList triggers={definition.triggers} labels={labels} />
          </Section>

          <Section title="Steps">
            {definition.conditions && <>
              <h3 className="text-sm font-medium">Admission Conditions</h3>
              <AutomationConditionTree condition={definition.conditions} labels={labels} />
            </>}
            <AutomationStepTree steps={definition.steps} labels={labels} />
          </Section>
        </>
      )}

      {/* History is read from its own endpoint and the backend retains it after
          an Automation is deleted, so it renders on its own gate: a definition
          404 must not hide retained Runs and Skips. */}
      {showHistory && (
        <Section title="History">
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" onClick={refreshHistoryAndSelection}>
              Refresh history
            </Button>
            {historyLoading && <span className="text-sm text-muted-foreground">Loading…</span>}
          </div>
          <p className="mt-1.5 text-xs text-muted-foreground">
            Newest first, Runs and Skips together. Select an entry id to load its recorded detail.
          </p>
          {historyError && <ErrorBox error={historyError} />}
          {history && (
            <>
              <Table className="mt-3">
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead>Kind</TableHead>
                    <TableHead>Recorded</TableHead>
                    <TableHead>Status / reason</TableHead>
                    <TableHead>Revision</TableHead>
                    <TableHead>Entry id</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.items.length === 0 && (
                    <EmptyRow colSpan={5} message="No Runs or Skips recorded yet." />
                  )}
                  {history.items.map((summary) => (
                    <TableRow
                      key={summary.id}
                      className={summary.id === selectedEntryId ? "bg-muted/50" : undefined}
                    >
                      <TableCell>
                        <StatusChip status={summary.kind} />
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {summary.recorded_at}
                      </TableCell>
                      <TableCell>
                        <StatusChip status={historySummaryStatus(summary)} />
                      </TableCell>
                      <TableCell className="font-mono text-xs">{summary.revision}</TableCell>
                      <TableCell className="max-w-[18rem]">
                        <Button
                          variant="link"
                          size="xs"
                          className="font-mono"
                          aria-current={summary.id === selectedEntryId ? "true" : undefined}
                          onClick={() => setSelectedEntryId(summary.id)}
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
                  size="sm"
                  variant="outline"
                  className="mt-2"
                  onClick={() => {
                    const cursor = history.next_cursor;
                    if (cursor) goToNextHistoryPage(cursor);
                  }}
                >
                  Next page
                </Button>
              )}
            </>
          )}
          {selectedEntryId && (
            <div className="mt-4 rounded-lg border p-3">
              {entryLoading && <p className="text-sm text-muted-foreground">Loading entry…</p>}
              {entryError && <ErrorBox error={entryError} />}
              {entry && <AutomationHistoryEntryView entry={entry} labels={labels} />}
            </div>
          )}
        </Section>
      )}

      {automation && definition && <RawJson value={automation} title="Raw automation JSON" />}
    </div>
  );
}
