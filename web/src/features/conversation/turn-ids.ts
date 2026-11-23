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

/**
 * assistant-ui keys each message by its id. A sent prompt and its reply keep
 * their in-flight ids after the stored history replaces them, so the same
 * components stay mounted: an inline view does not reload, and arrival motion
 * finishes instead of restarting or snapping. The next turn releases them.
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
  const [turn, setTurn] = useState({ key, ids: inFlight });
  if (
    turn.key !== key ||
    (inFlight.length > 0 && !sameIds(inFlight, turn.ids))
  ) {
    setTurn({ key, ids: inFlight });
  }
  const ids = inFlight.length > 0 || turn.key !== key ? inFlight : turn.ids;
  return useCallback(
    (item: ConversationItem, index: number) =>
      ids.find((entry) => entry.index === index && entry.role === item.role)
        ?.id ?? `${key}:${item.id}`,
    [ids, key]
  );
};
