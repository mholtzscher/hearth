import { createContext, useCallback, useContext, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from "react";
import { listConversations } from "../../api/agent";
import { useBaseUrlVersion } from "../../api/client";
import { useApi } from "../../api/hooks";
import { ConversationSession } from "./ConversationSession";

type ConversationSummary = Awaited<ReturnType<typeof listConversations>>[number];
type ConversationsValue = {
  conversations: ConversationSummary[];
  error: Error | null;
  loading: boolean;
  refresh: () => void;
  /** Increments when the household asks for a new conversation, including from the current empty one. */
  newEpoch: number;
  startNew: () => void;
  session: (key: string, id?: string) => ConversationSession;
};
const Context = createContext<ConversationsValue | null>(null);
export function useConversations() { const value = useContext(Context); if (!value) throw new Error("Conversations are missing"); return value; }

/** Owns browser conversations and their requests independently of the visible page. */
export function ConversationsProvider({ children }: { children: ReactNode }) {
  const version = useBaseUrlVersion();
  const { data, error, loading, refresh } = useApi("agent-conversations", () => listConversations());
  const [newEpoch, setNewEpoch] = useState(0);
  const [created, setCreated] = useState<{ version: number; items: ConversationSummary[] }>({ version, items: [] });
  const sessions = useMemo(() => new Map<string, ConversationSession>(), [version]);
  useEffect(() => () => { sessions.forEach(session => session.dispose()); }, [sessions]);
  const session = useCallback((key: string, id?: string) => {
    let value = sessions.get(key);
    if (!value) {
      value = new ConversationSession(key, id, (current, prompt) => {
        const id = current.getSnapshot().id!;
        // The creating page and the canonical URL must share the same live owner.
        sessions.delete(current.key);
        const owner = sessions.get(id) ?? current;
        sessions.set(id, owner);
        const now = new Date().toISOString();
        setCreated(previous => ({ version, items: [{ id, preview: prompt, created_at: now, last_message_at: now, message_count: 1 }, ...(previous.version === version ? previous.items : [])] }));
        return owner;
      }, () => { void refresh(); }, unused => {
        for (const [key, value] of sessions) if (value === unused) sessions.delete(key);
      });
      sessions.set(key, value);
    }
    return value;
  }, [sessions, refresh, version]);
  // A just-created conversation is navigable before its first server list refresh.
  const conversations = useMemo(() => {
    const known = new Set((data ?? []).map(item => item.id));
    return [...(created.version === version ? created.items.filter(item => !known.has(item.id)) : []), ...(data ?? [])];
  }, [created, data, version]);
  useEffect(() => {
    if (!data) return;
    const known = new Set(data.map(item => item.id));
    setCreated(previous => {
      if (previous.version !== version) return previous;
      const items = previous.items.filter(item => !known.has(item.id));
      return items.length === previous.items.length ? previous : { version, items };
    });
  }, [created, data, version]);
  const startNew = useCallback(() => setNewEpoch(epoch => epoch + 1), []);
  const value = useMemo(() => ({
    conversations, error, loading, refresh: () => void refresh(), newEpoch, startNew, session,
  }), [conversations, error, loading, refresh, newEpoch, startNew, session]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

/** Only the page subscription follows routes. Its session and requests live above them. */
export function useConversationSession(id?: string) {
  const { session, newEpoch } = useConversations();
  // Keep a pending/failed creation recoverable from Home. Successful creation removes its Home key.
  const key = id ?? `new:${newEpoch}`;
  const current = useMemo(() => session(key, id), [session, key, id]);
  const state = useSyncExternalStore(current.subscribe, current.getSnapshot);
  useEffect(() => { void current.load(); }, [current]);
  return { session: current, ...state };
}
