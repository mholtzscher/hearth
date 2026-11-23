import type { AgentMessage, Artifact, TurnEvent } from "../../api/agent";

export type Base = { dashboard_id: string; revision: number } | null;
interface ToolCall {
  id: string;
  name: string;
  presentationKey?: string;
}

export interface Activity extends ToolCall {
  key: string;
  arguments: string;
  result?: string;
}
export interface PresentedArtifact {
  artifact: Artifact;
  base: Base;
  /** Same occurrence key in the pending, committed, and stored reply. */
  presentationKey?: string;
}
/** Pending presentations retain their kind until the committed artifact arrives. */
interface PendingArtifact {
  id: string;
  kind: "view" | "automation";
}

export interface ConversationItem {
  id: string;
  role: "user" | "assistant";
  text: string;
  activities: Activity[];
  artifacts: PresentedArtifact[];
  context?: unknown;
  pendingArtifacts?: PendingArtifact[];
}

const viewIdentity = (
  { id, name }: Pick<ToolCall, "id" | "name">,
  occurrence: number
) =>
  name === "present_view"
    ? { presentationKey: `view:${id}:${occurrence}` }
    : {};

/** Project committed rows without assuming call IDs are unique outside a batch. */
const historyBase = (row: AgentMessage): Base => {
  const source = row.view_context;
  if (source?.source_dashboard_id && source.source_revision) {
    return {
      dashboard_id: source.source_dashboard_id,
      revision: source.source_revision,
    };
  }
  return null;
};

export const projectHistory = (rows: AgentMessage[]): ConversationItem[] => {
  const output: ConversationItem[] = [];
  let base: Base = null;
  let batch: ConversationItem | undefined;
  const seen = new Set<string>();
  const occurrences = new Map<string, number>();
  for (const row of rows) {
    if (row.role === "user") {
      const context = row.automation_context ?? row.view_context;
      base = historyBase(row);
      batch = undefined;
      occurrences.clear();
      output.push({
        id: `row-${row.message_id}`,
        role: "user",
        text: row.content,
        activities: [],
        artifacts: [],
        context,
      });
    } else if (row.role === "assistant") {
      const calls = row.calls ?? [];
      batch = {
        id: `row-${row.message_id}`,
        role: "assistant",
        text: row.content,
        activities: calls.map((call) => {
          const occurrence = occurrences.get(call.id) ?? 0;
          occurrences.set(call.id, occurrence + 1);
          return {
            key: `row-${row.message_id}:${call.id}`,
            ...call,
            ...viewIdentity(call, occurrence),
          };
        }),
        artifacts: [],
      };
      if (!calls.length && row.tool_calls?.length) {
        batch.activities = row.tool_calls.map((name, index) => ({
          key: `row-${row.message_id}:legacy-${index}`,
          id: `legacy-${index}`,
          name,
          arguments: "",
        }));
      }
      // Tool-call prose is a preamble, not the terminal answer. Keep the raw
      // history and activities, but do not resurrect narration on reload.
      if (batch.activities.length) {
        batch.text = "";
      }
      output.push(batch);
    } else if (row.role === "tool") {
      if (!batch) {
        continue;
      }
      const activity =
        (row.tool_call_id
          ? batch.activities.find(
              (call) =>
                call.id === row.tool_call_id && call.result === undefined
            )
          : undefined) ??
        batch.activities.find(
          (call) =>
            call.id.startsWith("legacy-") &&
            call.result === undefined &&
            (!row.tool_name || row.tool_name === call.name)
        );
      if (activity) {
        activity.result = row.content;
      }
      for (const artifact of row.artifacts ?? []) {
        if (!seen.has(artifact.artifact_id)) {
          seen.add(artifact.artifact_id);
          batch.artifacts.push({
            artifact,
            base,
            ...(activity?.presentationKey && {
              presentationKey: activity.presentationKey,
            }),
          });
        }
      }
    }
  }
  return output;
};

/** Combine a turn's activity and artifacts, showing only its terminal reply text. */
export const groupReplies = (items: ConversationItem[]): ConversationItem[] => {
  const output: ConversationItem[] = [];
  for (const item of items) {
    const previous = output.at(-1);
    if (item.role === "assistant" && previous?.role === "assistant") {
      output[output.length - 1] = {
        ...previous,
        text: item.text,
        activities: [...previous.activities, ...item.activities],
        artifacts: [...previous.artifacts, ...item.artifacts],
        ...(item.pendingArtifacts
          ? { pendingArtifacts: item.pendingArtifacts }
          : {}),
      };
    } else {
      output.push(item);
    }
  }
  return output;
};

/** preparing holds calls the model has begun writing, until each call finishes. */
export interface LiveTurn {
  reply: string;
  activities: Activity[];
  artifacts: PresentedArtifact[];
  preparing: ToolCall[];
  failed: string | null;
  terminal: boolean;
}
export const emptyTurn = (): LiveTurn => ({
  reply: "",
  activities: [],
  artifacts: [],
  preparing: [],
  failed: null,
  terminal: false,
});

/** Announced or running presentation calls without a result. */
export const pendingArtifactCalls = (
  turn: LiveTurn
): NonNullable<ConversationItem["pendingArtifacts"]> => {
  if (turn.terminal) {
    return [];
  }
  const presented = new Set(turn.artifacts.map((item) => item.presentationKey));
  const calls = [
    ...turn.preparing,
    ...turn.activities.filter(
      (call) =>
        call.result === undefined ||
        // A successful tool result precedes the committed view event. Keep the
        // placeholder across that gap, including when the result was truncated.
        (call.name === "present_view" &&
          /"kind"\s*:\s*"hearth\.view"/u.test(call.result))
    ),
  ];
  const pending = new Map<string, PendingArtifact>();
  for (const call of calls) {
    if (call.presentationKey && !presented.has(call.presentationKey)) {
      pending.set(call.presentationKey, {
        id: call.presentationKey,
        kind: "view",
      });
    }
    if (call.name === "present_automation") {
      pending.set(`automation:${call.id}`, { id: call.id, kind: "automation" });
    }
  }
  return [...pending.values()];
};

const streamedPresentation = (
  turn: LiveTurn,
  event: TurnEvent,
  artifact: Artifact,
  base: Base
): PresentedArtifact => {
  // Core emits each committed view before the next model batch can reuse its ID.
  const presentationKey =
    event.type === "view.presented"
      ? turn.activities.findLast((call) => call.id === event.call_id)
          ?.presentationKey
      : undefined;
  return { artifact, base, ...(presentationKey && { presentationKey }) };
};

const activityCount = (turn: LiveTurn, id: string) =>
  turn.activities.filter((call) => call.id === id).length;

export const reduceTurn = (
  turn: LiveTurn,
  event: TurnEvent,
  scope: string,
  base: Base
): LiveTurn => {
  if (
    event.type === "tool.preparing" &&
    event.call_id &&
    event.name &&
    !turn.preparing.some((call) => call.id === event.call_id)
  ) {
    const { call_id: id, name } = event;
    return {
      ...turn,
      preparing: [
        ...turn.preparing,
        {
          id,
          name,
          ...viewIdentity({ id, name }, activityCount(turn, id)),
        },
      ],
    };
  }
  if (event.type === "tool.started" && event.call_id && event.name) {
    const { call_id: id, name } = event;
    const occurrence = activityCount(turn, id);
    return {
      ...turn,
      activities: [
        ...turn.activities,
        {
          key: `${scope}:${id}:${occurrence}`,
          id,
          name,
          arguments: event.arguments ?? "",
          ...viewIdentity({ id, name }, occurrence),
        },
      ],
    };
  }
  if (event.type === "tool.finished" && event.call_id) {
    const index = turn.activities
      .map((call) => call.id === event.call_id && call.result === undefined)
      .lastIndexOf(true);
    return {
      ...turn,
      preparing: turn.preparing.filter((call) => call.id !== event.call_id),
      activities: turn.activities.map((call, position) =>
        position === index ? { ...call, result: event.result ?? "" } : call
      ),
    };
  }
  if (
    (event.type === "view.presented" ||
      event.type === "automation.presented") &&
    event.artifact &&
    !turn.artifacts.some(
      (item) => item.artifact.artifact_id === event.artifact?.artifact_id
    )
  ) {
    return {
      ...turn,
      artifacts: [
        ...turn.artifacts,
        streamedPresentation(turn, event, event.artifact, base),
      ],
    };
  }
  if (event.type === "turn.failed") {
    return {
      ...turn,
      terminal: true,
      failed: event.error || "The request failed.",
    };
  }
  if (event.type === "turn.finished") {
    return { ...turn, terminal: true, reply: event.reply ?? "" };
  }
  return turn;
};
