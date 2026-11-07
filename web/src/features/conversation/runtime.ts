import type { AgentMessage, Artifact, TurnEvent } from "../../api/agent";

export type Base = { dashboard_id: string; revision: number } | null;
export type Activity = { key: string; id: string; name: string; arguments: string; result?: string };
export type PresentedArtifact = { artifact: Artifact; base: Base };
/** Pending presentations retain their kind until the committed artifact arrives. */
export type ConversationItem = { id: string; role: "user" | "assistant"; text: string; activities: Activity[]; artifacts: PresentedArtifact[]; context?: unknown; pendingArtifacts?: { id: string; kind: "view" | "automation" }[] };

/** Project committed rows without assuming call IDs are unique outside a batch. */
export function projectHistory(rows: AgentMessage[]): ConversationItem[] {
  const output: ConversationItem[] = [];
  let base: Base = null;
  let batch: ConversationItem | undefined;
  const seen = new Set<string>();
  for (const row of rows) {
    if (row.role === "user") {
      const context = row.automation_context ?? row.view_context;
      base = row.view_context?.source_dashboard_id && row.view_context.source_revision ? { dashboard_id: row.view_context.source_dashboard_id, revision: row.view_context.source_revision } : null;
      batch = undefined;
      output.push({ id: `row-${row.message_id}`, role: "user", text: row.content, activities: [], artifacts: [], context });
    } else if (row.role === "assistant") {
      const calls = row.calls ?? [];
      batch = { id: `row-${row.message_id}`, role: "assistant", text: row.content, activities: calls.map(call => ({ key: `row-${row.message_id}:${call.id}`, ...call })), artifacts: [] };
      if (!calls.length && row.tool_calls?.length) batch.activities = row.tool_calls.map((name, index) => ({ key: `row-${row.message_id}:legacy-${index}`, id: `legacy-${index}`, name, arguments: "" }));
      // Tool-call prose is a preamble, not the terminal answer. Keep the raw
      // history and activities, but do not resurrect narration on reload.
      if (batch.activities.length) batch.text = "";
      output.push(batch);
    } else if (row.role === "tool") {
      if (!batch) continue;
      const activity = (row.tool_call_id ? batch.activities.find(call => call.id === row.tool_call_id && call.result === undefined) : undefined)
        ?? batch.activities.find(call => call.id.startsWith("legacy-") && call.result === undefined && (!row.tool_name || row.tool_name === call.name));
      if (activity) activity.result = row.content;
      for (const artifact of row.artifacts ?? []) {
        if (!seen.has(artifact.artifact_id)) { seen.add(artifact.artifact_id); batch.artifacts.push({ artifact, base }); }
      }
    }
  }
  return output;
}

/** Combine a turn's activity and artifacts, showing only its terminal reply text. */
export function groupReplies(items: ConversationItem[]): ConversationItem[] {
  const output: ConversationItem[] = [];
  for (const item of items) {
    const previous = output[output.length - 1];
    if (item.role === "assistant" && previous?.role === "assistant") {
      output[output.length - 1] = {
        ...previous,
        text: item.text,
        activities: [...previous.activities, ...item.activities],
        artifacts: [...previous.artifacts, ...item.artifacts],
        ...(item.pendingArtifacts ? { pendingArtifacts: item.pendingArtifacts } : {}),
      };
    } else output.push(item);
  }
  return output;
}

/** preparing holds calls the model has begun writing, until each call finishes. */
export type LiveTurn = { activities: Activity[]; artifacts: PresentedArtifact[]; preparing: { id: string; name: string }[]; failed: string | null; terminal: boolean };
export const emptyTurn = (): LiveTurn => ({ activities: [], artifacts: [], preparing: [], failed: null, terminal: false });

/** Announced or running presentation calls without a result. */
export function pendingArtifactCalls(turn: LiveTurn): NonNullable<ConversationItem["pendingArtifacts"]> {
  const calls = [...turn.preparing, ...turn.activities.filter(call => call.result === undefined)];
  const pending = new Map<string, "view" | "automation">();
  for (const call of calls) {
    if (call.name === "present_view") pending.set(call.id, "view");
    if (call.name === "present_automation") pending.set(call.id, "automation");
  }
  return [...pending].map(([id, kind]) => ({ id, kind }));
}
export function reduceTurn(turn: LiveTurn, event: TurnEvent, scope: string, base: Base): LiveTurn {
  if (event.type === "tool.preparing" && event.call_id && event.name && !turn.preparing.some(call => call.id === event.call_id)) {
    return { ...turn, preparing: [...turn.preparing, { id: event.call_id, name: event.name }] };
  }
  if (event.type === "tool.started" && event.call_id && event.name) {
    const occurrence = turn.activities.filter(call => call.id === event.call_id).length;
    return { ...turn, activities: [...turn.activities, { key: `${scope}:${event.call_id}:${occurrence}`, id: event.call_id, name: event.name, arguments: event.arguments ?? "" }] };
  }
  if (event.type === "tool.finished" && event.call_id) {
    const index = turn.activities.map(call => call.id === event.call_id && call.result === undefined).lastIndexOf(true);
    return { ...turn, preparing: turn.preparing.filter(call => call.id !== event.call_id), activities: turn.activities.map((call, position) => position === index ? { ...call, result: event.result ?? "" } : call) };
  }
  if ((event.type === "view.presented" || event.type === "automation.presented") && event.artifact && !turn.artifacts.some(item => item.artifact.artifact_id === event.artifact?.artifact_id)) return { ...turn, artifacts: [...turn.artifacts, { artifact: event.artifact, base }] };
  if (event.type === "turn.failed") return { ...turn, terminal: true, failed: event.error || "The request failed." };
  if (event.type === "turn.finished") return { ...turn, terminal: true };
  return turn;
}
