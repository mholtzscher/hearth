import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { listConversations } from "../../api/agent";
import { useApi } from "../../api/hooks";

type ConversationSummary = Awaited<ReturnType<typeof listConversations>>[number];
type ConversationsValue = {
  conversations: ConversationSummary[];
  error: Error | null;
  loading: boolean;
  refresh: () => void;
  /** Increments when the household asks for a new conversation, including from the current empty one. */
  newEpoch: number;
  startNew: () => void;
  /** A turn is streaming; starting another conversation would abandon it. */
  turnActive: boolean;
  setTurnActive: (active: boolean) => void;
};
const Context = createContext<ConversationsValue | null>(null);
export function useConversations() { const value = useContext(Context); if (!value) throw new Error("Conversations are missing"); return value; }

/** Shares the conversation list between the sidebar and the Home workspace. */
export function ConversationsProvider({ children }: { children: ReactNode }) {
  const { data, error, loading, refresh } = useApi("agent-conversations", () => listConversations());
  const [newEpoch, setNewEpoch] = useState(0);
  const [turnActive, setTurnActive] = useState(false);
  // A turn that ends for any reason, including leaving Home mid-reply, may have created or extended a conversation.
  const wasActive = useRef(false);
  useEffect(() => {
    if (wasActive.current && !turnActive) void refresh();
    wasActive.current = turnActive;
  }, [turnActive, refresh]);
  const startNew = useCallback(() => setNewEpoch(epoch => epoch + 1), []);
  const value = useMemo(() => ({
    conversations: data ?? [], error, loading, refresh: () => void refresh(), newEpoch, startNew, turnActive, setTurnActive,
  }), [data, error, loading, refresh, newEpoch, startNew, turnActive]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
