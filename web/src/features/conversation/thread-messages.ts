import type { AppendMessage, ThreadMessageLike } from "@assistant-ui/react";

import { pendingArtifactCalls } from "./runtime";
import type { ConversationItem, LiveTurn } from "./runtime";

type MessageStatus = ThreadMessageLike["status"];

const messageStatus = (
  item: ConversationItem,
  running: boolean
): MessageStatus => {
  if (item.id === "live" && running) {
    return { type: "running" };
  }
  if (item.activities.some((call) => call.result === undefined)) {
    return { type: "incomplete", reason: "other" };
  }
  return { type: "complete", reason: "stop" };
};

export const convertMessage = (
  item: ConversationItem,
  messageId: string,
  running: boolean
): ThreadMessageLike => {
  const text = item.text ? [{ type: "text" as const, text: item.text }] : [];
  return {
    id: messageId,
    role: item.role,
    ...(item.role === "assistant"
      ? { status: messageStatus(item, running) }
      : {}),
    // Summarize tool activity before the presented views and terminal reply text.
    content:
      item.role === "user"
        ? [
            ...text,
            ...(item.context
              ? [{ type: "data-hearth-context" as const, data: item.context }]
              : []),
          ]
        : [
            ...item.activities.map((call) => ({
              type: "tool-call" as const,
              toolCallId: call.key,
              toolName: call.name,
              argsText: call.arguments,
              ...(call.result === undefined ? {} : { result: call.result }),
            })),
            ...item.artifacts.map((presentation) => ({
              type: "data-hearth-view" as const,
              id: presentation.presentationKey
                ? `presentation:${presentation.presentationKey}`
                : presentation.artifact.artifact_id,
              data: presentation,
            })),
            // Hold each presentation's place until Core has checked and committed it.
            ...(running
              ? (item.pendingArtifacts ?? []).map(({ id, kind }) => ({
                  type: "data-hearth-view" as const,
                  id:
                    kind === "view"
                      ? `presentation:${id}`
                      : `pending-automation:${id}`,
                  data: { pending: kind },
                }))
              : []),
            ...text,
          ],
  };
};

export const conversationItems = ({
  items,
  optimistic,
  live,
  queued,
}: {
  items: ConversationItem[];
  optimistic: ConversationItem | null;
  live: LiveTurn | null;
  queued: ConversationItem | null;
}): ConversationItem[] => [
  ...items,
  ...(optimistic ? [optimistic] : []),
  ...(live
    ? [
        {
          id: "live",
          role: "assistant" as const,
          text: live.reply,
          activities: live.activities,
          artifacts: live.artifacts,
          pendingArtifacts: pendingArtifactCalls(live),
        },
      ]
    : []),
  ...(queued ? [queued] : []),
];

export const sendIsBlocked = ({
  busy,
  loading,
  needsRecovery,
  saving,
  editorValid,
}: {
  busy: boolean;
  loading: boolean;
  needsRecovery: boolean;
  saving: boolean;
  editorValid: boolean;
}) => busy || loading || needsRecovery || saving || !editorValid;

/** The text a composer submitted, joined across its text parts. */
export const promptText = (message: AppendMessage): string =>
  message.content
    .filter((part) => part.type === "text")
    .map((part) => part.text)
    .join("\n");
