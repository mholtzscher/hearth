import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Link as RouterLink, useParams } from "react-router-dom";
import { apiFetch, useBaseUrlVersion } from "../api/client.ts";
import { useApi } from "../api/hooks.ts";
import { fetchAllCollectionPages } from "../api/pagination.ts";
import type {
  Automation,
  AutomationComparison,
  AutomationDefinition,
  AutomationHistoryEntry,
  AutomationHistorySummary,
  AutomationRun,
  AutomationSkip,
  AutomationSkipReason,
  AutomationStep,
  AutomationStepAttempt,
  AutomationTrigger,
  Collection,
  DeviceFactSummary,
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

const HISTORY_PAGE_LIMIT = 50;

/** One page path for the newest-first Automation history keyset. */
function automationHistoryPath(automationId: string, cursor: string | undefined): string {
  return `/v1/automations/${automationId}/history?limit=${HISTORY_PAGE_LIMIT}${
    cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""
  }`;
}

/** Deduplicated Entity IDs referenced by Triggers and Steps, in first-seen order. */
function referencedEntityIds(triggers: AutomationTrigger[], steps: AutomationStep[]): string[] {
  const ids = new Set<string>();
  for (const trigger of triggers) ids.add(trigger.entity_id);
  for (const step of steps) ids.add(step.entity_id);
  return [...ids];
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

/** Static Step parameters as JSON text; absent or malformed values stay visible. */
function parameterText(parameters: unknown): string {
  try {
    const text = JSON.stringify(parameters);
    return text === undefined ? "—" : text;
  } catch {
    return String(parameters);
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
            <span className="text-sm text-muted-foreground">matches</span>
            <AutomationEntityLink entityId={trigger.entity_id} labels={labels} />
          </div>
          {trigger.kind === "entity_event" ? (
            <p className="mt-1 text-xs text-muted-foreground">
              event name <span className="font-mono">{trigger.event_name ?? "—"}</span>
            </p>
          ) : (
            <p className="mt-1 text-xs text-muted-foreground">
              dispositions{" "}
              <span className="font-mono">{(trigger.dispositions ?? []).join(", ") || "—"}</span>
              {(trigger.comparisons?.length ?? 0) > 0 && (
                <>
                  {" · comparisons "}
                  <span className="font-mono">
                    {(trigger.comparisons ?? []).map(comparisonText).join(" and ")}
                  </span>
                </>
              )}
            </p>
          )}
        </li>
      ))}
    </ul>
  );
}

/** Ordered Steps. A Step carries no position of its own, so the list position
    is shown: it matches the zero-based `position` of Run Step attempts. */
function AutomationStepTable({
  steps,
  labels,
}: {
  steps: AutomationStep[];
  labels: ReadonlyMap<string, string>;
}) {
  return (
    <Table className="mt-1">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead>Position</TableHead>
          <TableHead>Step id</TableHead>
          <TableHead>Entity</TableHead>
          <TableHead>Operation</TableHead>
          <TableHead>Parameters</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {steps.length === 0 && <EmptyRow colSpan={5} message="No Steps." />}
        {steps.map((step, position) => (
          <TableRow key={step.id}>
            <TableCell className="font-mono text-xs text-muted-foreground">{position}</TableCell>
            <TableCell className="font-mono text-xs">{step.id}</TableCell>
            <TableCell>
              <AutomationEntityLink entityId={step.entity_id} labels={labels} />
            </TableCell>
            <TableCell>{step.operation}</TableCell>
            <TableCell className="font-mono text-xs">{parameterText(step.parameters)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

/** Ordered Step attempts of one Run. Only ownership-verified Command IDs are
    exposed, so an attempt with no verified Command shows a dash. A verified
    Command links to its durable audit record on the Commands tab. */
function AutomationStepAttemptTable({ attempts }: { attempts: AutomationStepAttempt[] }) {
  return (
    <Table className="mt-1">
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
        {attempts.map((attempt) => (
          <TableRow key={`${attempt.position}-${attempt.step_id}`}>
            <TableCell className="font-mono text-xs text-muted-foreground">
              {attempt.position}
            </TableCell>
            <TableCell className="font-mono text-xs">{attempt.step_id}</TableCell>
            <TableCell>
              <StatusChip status={attempt.status} />
            </TableCell>
            <TableCell className="max-w-[16rem]">
              {attempt.verified_command_id ? (
                <RouterLink
                  to={`/commands?command_id=${attempt.verified_command_id}`}
                  className={linkClass}
                  title={attempt.verified_command_id}
                >
                  <MonoId value={attempt.verified_command_id} className="text-muted-foreground" />
                </RouterLink>
              ) : (
                <MonoId value="—" className="text-muted-foreground" />
              )}
            </TableCell>
            <TableCell className="font-mono text-xs">{attempt.failure_code ?? "—"}</TableCell>
            <TableCell className="font-mono text-xs text-muted-foreground">
              {attempt.started_at ?? "—"}
            </TableCell>
            <TableCell className="font-mono text-xs text-muted-foreground">
              {attempt.completed_at ?? "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

/** Retained Fact evidence rows, shared by a Run and a Skip. An Entity Event
    Fact reports a variant name and carries no Observation value. */
function deviceFactRows(
  fact: DeviceFactSummary | undefined,
  labels: ReadonlyMap<string, string>,
): [string, ReactNode][] {
  if (!fact) return [];
  return [
    ["Fact id", fact.fact_id],
    ["Fact family", `${fact.family} / ${fact.variant}`],
    ["Fact entity", <AutomationEntityLink entityId={fact.entity_id} labels={labels} />],
    ["Fact emitted", fact.emitted_at],
    ["Fact causation", fact.causation_id],
    [
      "Reported value",
      fact.observation_value === undefined
        ? "— (an Entity Event Fact carries no value)"
        : JSON.stringify(fact.observation_value),
    ],
  ];
}

/** Plain-language meaning of a Skip reason (CONTEXT.md: Automation Skip). */
function skipReasonNote(reason: AutomationSkipReason): string {
  return reason === "automation_busy"
    ? "Matched while this Automation already had a running Run, so no second Run started."
    : "Matched a Fact that was already too old to run, so no Run started. A Skip never queues execution.";
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
        <StatusChip label="source" status={run.source} />
        <span className="text-xs text-muted-foreground">revision {run.revision}</span>
      </div>
      <Facts
        rows={[
          ["Run id", run.id],
          ["Automation", run.automation_name],
          ["Started at", run.started_at],
          ["Completed at", run.completed_at ?? "— (not complete yet)"],
          ["Failure", run.failure_code ?? "—"],
          [
            "Matched triggers",
            run.matched_trigger_ids.length > 0
              ? run.matched_trigger_ids.join(", ")
              : "none (a manual Run starts without one)",
          ],
        ]}
      />
      {run.fact && (
        <div className="mt-2">
          <Facts rows={deviceFactRows(run.fact, labels)} />
        </div>
      )}
      <h3 className="mt-4 text-sm font-medium">Step attempts</h3>
      <AutomationStepAttemptTable attempts={run.steps} />
      <h3 className="mt-4 text-sm font-medium">Definition snapshot</h3>
      <AutomationTriggerList triggers={run.snapshot.triggers} labels={labels} />
      <AutomationStepTable steps={run.snapshot.steps} labels={labels} />
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
        <span className="text-xs text-muted-foreground">revision {skip.revision}</span>
      </div>
      <p className="mt-1.5 text-xs text-muted-foreground">{skipReasonNote(skip.reason)}</p>
      <Facts
        rows={[
          ["Skip id", skip.id],
          ["Automation", skip.automation_name],
          ["Skipped at", skip.skipped_at],
          ...deviceFactRows(skip.fact, labels),
        ]}
      />
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
  if (entry.run) return <AutomationRunView run={entry.run} labels={labels} />;
  if (entry.skip) return <AutomationSkipView skip={entry.skip} labels={labels} />;
  return (
    <p className="text-sm text-muted-foreground">
      {`A ${entry.kind} history entry carried no Run or Skip payload.`}
    </p>
  );
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
      const triggers = [
        ...(definition?.triggers ?? []),
        ...(entry?.run?.snapshot.triggers ?? []),
        ...(entry?.skip?.matched_triggers ?? []),
      ];
      const steps = [
        ...(definition?.steps ?? []),
        ...(entry?.run?.snapshot.steps ?? []),
      ];
      return referencedEntityIds(triggers, steps);
    }, [definition, entry]),
  );

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
                    ["Steps", String(definition.steps.length)],
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
            <AutomationStepTable steps={definition.steps} labels={labels} />
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
                        {summary.status ? (
                          <StatusChip status={summary.status} />
                        ) : summary.reason ? (
                          <StatusChip status={summary.reason} />
                        ) : (
                          "—"
                        )}
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
