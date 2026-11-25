import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { ReactNode } from "react";

import {
  listConversations,
  listStarterQuestions,
  refreshStarterQuestions as postStarterQuestionRefresh,
} from "../../api/agent";
import type { StarterQuestion } from "../../api/agent";
import { useBaseUrlVersion } from "../../api/client";
import { useApi } from "../../api/hooks";
import { ConversationSession } from "./conversation-session";

type ConversationSummary = Awaited<
  ReturnType<typeof listConversations>
>[number];
interface ConversationsValue {
  conversations: ConversationSummary[];
  error: Error | null;
  loading: boolean;
  refresh: () => void;
  /** Increments when the household asks for a new conversation, including from the current empty one. */
  newEpoch: number;
  startNew: () => void;
  session: (key: string, id?: string) => ConversationSession;
  /** Core's generated starter questions, or null before they load or when the read fails. */
  starterQuestions: readonly StarterQuestion[] | null;
  /** Core generates starter questions, so they can be regenerated on request. */
  starterQuestionsEnabled: boolean;
  /** The first read of starter questions has not answered yet. */
  starterQuestionsPending: boolean;
  /** Reads Core's current starter questions again. */
  refreshStarterQuestions: () => void;
  /** Asks Core to generate new starter questions now and returns them. */
  regenerateStarterQuestions: () => Promise<readonly StarterQuestion[]>;
}
class SessionRegistry {
  readonly version: number;
  private readonly sessions = new Map<string, ConversationSession>();

  constructor(version: number) {
    this.version = version;
  }

  get(key: string) {
    return this.sessions.get(key);
  }

  set(key: string, session: ConversationSession) {
    this.sessions.set(key, session);
  }

  delete(key: string) {
    this.sessions.delete(key);
  }

  remove(session: ConversationSession) {
    for (const [key, value] of this.sessions) {
      if (value === session) {
        this.sessions.delete(key);
      }
    }
  }

  disposeAll() {
    for (const session of this.sessions.values()) {
      session.dispose();
    }
  }
}

const Context = createContext<ConversationsValue | null>(null);
export const useConversations = () => {
  const value = useContext(Context);
  if (!value) {
    throw new Error("Conversations are missing");
  }
  return value;
};

/** Owns browser conversations and their requests independently of the visible page. */
export const ConversationsProvider = ({
  children,
}: {
  children: ReactNode;
}) => {
  const version = useBaseUrlVersion();
  const { data, error, loading, refresh } = useApi("agent-conversations", () =>
    listConversations()
  );
  // Read at mount so generated questions are usually ready before history loads.
  const {
    data: starterData,
    loading: starterQuestionsLoading,
    error: starterQuestionsError,
    refresh: reloadStarterQuestions,
    setData: setStarterData,
  } = useApi("agent-starter-questions", () => listStarterQuestions());
  const starterQuestions = starterData?.questions ?? null;
  const starterQuestionsEnabled = starterData?.enabled ?? false;
  const starterQuestionsPending =
    starterData === null &&
    starterQuestionsLoading &&
    starterQuestionsError === null;
  // A refresh answered after the backend changed belongs to the previous
  // household; dropping it keeps its questions out of the new backend's cache.
  const backendVersion = useRef(version);
  useLayoutEffect(() => {
    backendVersion.current = version;
  }, [version]);
  const regenerateStarterQuestions = useCallback(async () => {
    const requestedFrom = backendVersion.current;
    const next = await postStarterQuestionRefresh();
    if (backendVersion.current !== requestedFrom) {
      return [];
    }
    setStarterData(next);
    return next.questions;
  }, [setStarterData]);
  const refreshStarterQuestions = useCallback(() => {
    void reloadStarterQuestions();
  }, [reloadStarterQuestions]);
  const [newEpoch, setNewEpoch] = useState(0);
  const [created, setCreated] = useState<{
    version: number;
    items: ConversationSummary[];
  }>({ version, items: [] });
  const sessions = useMemo(() => new SessionRegistry(version), [version]);
  useEffect(
    () => () => {
      sessions.disposeAll();
    },
    [sessions]
  );
  const session = useCallback(
    (key: string, id?: string) => {
      let value = sessions.get(key);
      if (!value) {
        value = new ConversationSession(
          key,
          id,
          (current, prompt) => {
            const createdId = current.getSnapshot().id;
            if (!createdId) {
              throw new Error("Created conversation is missing its id");
            }
            // The creating page and the canonical URL must share the same live owner.
            sessions.delete(current.key);
            const owner = sessions.get(createdId) ?? current;
            sessions.set(createdId, owner);
            const now = new Date().toISOString();
            setCreated((previous) => ({
              version,
              items: [
                {
                  id: createdId,
                  preview: prompt,
                  created_at: now,
                  last_message_at: now,
                  message_count: 1,
                },
                ...(previous.version === version ? previous.items : []),
              ],
            }));
            return owner;
          },
          () => {
            void refresh();
          },
          (unused) => {
            sessions.remove(unused);
          }
        );
        sessions.set(key, value);
      }
      return value;
    },
    [sessions, refresh, version]
  );
  // A just-created conversation is navigable before its first server list refresh.
  const conversations = useMemo(() => {
    const known = new Set((data ?? []).map((item) => item.id));
    return [
      ...(created.version === version
        ? created.items.filter((item) => !known.has(item.id))
        : []),
      ...(data ?? []),
    ];
  }, [created, data, version]);
  const serverIds = new Set((data ?? []).map((item) => item.id));
  if (data && created.version === version) {
    const items = created.items.filter((item) => !serverIds.has(item.id));
    if (items.length !== created.items.length) {
      setCreated({ version, items });
    }
  }
  const startNew = useCallback(() => setNewEpoch((epoch) => epoch + 1), []);
  const value = useMemo(
    () => ({
      conversations,
      error,
      loading,
      refresh: () => {
        void refresh();
      },
      newEpoch,
      startNew,
      session,
      starterQuestions,
      starterQuestionsEnabled,
      starterQuestionsPending,
      refreshStarterQuestions,
      regenerateStarterQuestions,
    }),
    [
      conversations,
      error,
      loading,
      refresh,
      newEpoch,
      startNew,
      session,
      starterQuestions,
      starterQuestionsEnabled,
      starterQuestionsPending,
      refreshStarterQuestions,
      regenerateStarterQuestions,
    ]
  );
  return <Context.Provider value={value}>{children}</Context.Provider>;
};

/** Only the page subscription follows routes. Its session and requests live above them. */
export const useConversationSession = (id?: string) => {
  const { session, newEpoch } = useConversations();
  // Keep a pending/failed creation recoverable from Home. Successful creation removes its Home key.
  const key = id ?? `new:${newEpoch}`;
  const current = useMemo(() => session(key, id), [session, key, id]);
  const state = useSyncExternalStore(current.subscribe, current.getSnapshot);
  useEffect(() => {
    void current.load();
  }, [current]);
  return { session: current, ...state };
};
