import type { AgentMessage, Artifact, TurnEvent } from "../../api/agent";

export type Base = { dashboard_id: string; revision: number } | null;
export type Activity = { key: string; id: string; name: string; arguments: string; result?: string };
export type PresentedArtifact = { artifact: Artifact; base: Base };
export type ConversationItem = { id: string; role: "user" | "assistant"; text: string; activities: Activity[]; artifacts: PresentedArtifact[]; context?: unknown };

/** Project committed rows without assuming call IDs are unique outside a batch. */
export function projectHistory(rows: AgentMessage[]): ConversationItem[] {
  const output: ConversationItem[] = [];
  let base: Base = null;
  let batch: ConversationItem | undefined;
  const seen = new Set<string>();
  for (const row of rows) {
    if (row.role === "user") {
      const context = row.view_context;
      base = context?.source_dashboard_id && context.source_revision ? { dashboard_id: context.source_dashboard_id, revision: context.source_revision } : null;
      batch = undefined;
      output.push({ id: `row-${row.message_id}`, role: "user", text: row.content, activities: [], artifacts: [], context });
    } else if (row.role === "assistant") {
      const calls = row.calls ?? [];
      batch = { id: `row-${row.message_id}`, role: "assistant", text: row.content, activities: calls.map(call => ({ key: `row-${row.message_id}:${call.id}`, ...call })), artifacts: [] };
      if (!calls.length && row.tool_calls?.length) batch.activities = row.tool_calls.map((name, index) => ({ key: `row-${row.message_id}:legacy-${index}`, id: `legacy-${index}`, name, arguments: "" }));
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

/** Present one reply per user turn by merging the consecutive assistant messages a turn commits. */
export function groupReplies(items: ConversationItem[]): ConversationItem[] {
  const output: ConversationItem[] = [];
  for (const item of items) {
    const previous = output[output.length - 1];
    if (item.role === "assistant" && previous?.role === "assistant") {
      output[output.length - 1] = {
        ...previous,
        text: [previous.text, item.text].filter(Boolean).join("\n\n"),
        activities: [...previous.activities, ...item.activities],
        artifacts: [...previous.artifacts, ...item.artifacts],
      };
    } else output.push(item);
  }
  return output;
}

export type LiveTurn = { activities: Activity[]; artifacts: PresentedArtifact[]; failed: string | null; terminal: boolean };
export const emptyTurn = (): LiveTurn => ({ activities: [], artifacts: [], failed: null, terminal: false });
export function reduceTurn(turn: LiveTurn, event: TurnEvent, scope: string, base: Base): LiveTurn {
  if (event.type === "tool.started" && event.call_id && event.name) {
    const occurrence = turn.activities.filter(call => call.id === event.call_id).length;
    return { ...turn, activities: [...turn.activities, { key: `${scope}:${event.call_id}:${occurrence}`, id: event.call_id, name: event.name, arguments: event.arguments ?? "" }] };
  }
  if (event.type === "tool.finished" && event.call_id) {
    const index = turn.activities.map(call => call.id === event.call_id && call.result === undefined).lastIndexOf(true);
    return { ...turn, activities: turn.activities.map((call, position) => position === index ? { ...call, result: event.result ?? "" } : call) };
  }
  if (event.type === "view.presented" && event.artifact && !turn.artifacts.some(item => item.artifact.artifact_id === event.artifact?.artifact_id)) return { ...turn, artifacts: [...turn.artifacts, { artifact: event.artifact, base }] };
  if (event.type === "turn.failed") return { ...turn, terminal: true, failed: event.error || "The request failed." };
  if (event.type === "turn.finished") return { ...turn, terminal: true };
  return turn;
}
