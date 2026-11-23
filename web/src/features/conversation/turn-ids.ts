import { useCallback, useMemo, useState } from "react";

import type { ConversationItem } from "./runtime";

export interface TurnMessageId {
  index: number;
  role: ConversationItem["role"];
  id: string;
}

/** The in-flight prompt and reply, with message ids unique to their turn. */
export const inFlightIds = (
  key: string,
  replies: readonly ConversationItem[],
  promptId: string | undefined
): TurnMessageId[] =>
  replies.flatMap((item, index) => {
    if (promptId && item.id === promptId) {
      return [{ index, role: item.role, id: `${key}:${promptId}` }];
    }
    if (item.id === "live") {
      return [
        {
          index,
          role: item.role,
          id: promptId ? `${key}:reply-to-${promptId}` : `${key}:live`,
        },
      ];
    }
    return [];
  });

const sameIds = (a: TurnMessageId[], b: TurnMessageId[]) =>
  a.length === b.length &&
  a.every(
    (entry, position) =>
      entry.index === b[position]?.index &&
      entry.role === b[position]?.role &&
      entry.id === b[position]?.id
  );

const retainStoredIds = (
  stored: ReadonlyMap<string, string>,
  active: TurnMessageId[],
  replies: readonly ConversationItem[]
) => {
  const retained = new Map(stored);
  for (const { index, role, id } of active) {
    const reply = replies[index];
    if (
      reply?.role === role &&
      reply.id.startsWith("row-") &&
      !retained.has(reply.id)
    ) {
      retained.set(reply.id, id);
    }
  }
  return retained;
};

/**
 * assistant-ui keys each message by its id. A sent prompt and its reply keep
 * their in-flight ids after the stored history replaces them, so the same
 * components stay mounted: an inline view does not reload, and arrival motion
 * finishes instead of restarting or snapping. Once stored, the row id keeps
 * that alias until the conversation changes.
 */
export const useTurnMessageIds = (
  key: string,
  replies: readonly ConversationItem[],
  promptId: string | undefined
) => {
  const inFlight = useMemo(
    () => inFlightIds(key, replies, promptId),
    [key, replies, promptId]
  );
  const [turn, setTurn] = useState(() => ({
    key,
    active: inFlight,
    stored: new Map<string, string>(),
  }));
  if (turn.key !== key) {
    setTurn({ key, active: inFlight, stored: new Map() });
  } else if (!sameIds(inFlight, turn.active)) {
    setTurn({
      key,
      active: inFlight,
      stored:
        inFlight.length === 0
          ? retainStoredIds(turn.stored, turn.active, replies)
          : turn.stored,
    });
  }
  return useCallback(
    (item: ConversationItem, index: number) =>
      inFlight.find(
        (entry) => entry.index === index && entry.role === item.role
      )?.id ??
      turn.stored.get(item.id) ??
      `${key}:${item.id}`,
    [inFlight, key, turn.stored]
  );
};
