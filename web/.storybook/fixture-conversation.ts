import type { AgentMessage, TurnEvent } from "../src/api/agent";
import {
  emptyTurn,
  projectHistory,
  reduceTurn,
} from "../src/features/conversation/runtime";
import type {
  ConversationItem,
  LiveTurn,
} from "../src/features/conversation/runtime";

/** The parts of a conversation session's snapshot that the thread renders. */
export interface FixtureState {
  key: string;
  items: ConversationItem[];
  optimistic: ConversationItem | null;
  live: LiveTurn | null;
  queued: ConversationItem | null;
  busy: boolean;
  loading: boolean;
  error: string;
  failedPrompt: string;
  needsRecovery: boolean;
}

export type FixtureSeed = Partial<Omit<FixtureState, "items">> & {
  /** Stored rows, projected with projectHistory as a loaded history is. */
  history?: AgentMessage[];
};

/**
 * A story-local stand-in for ConversationSession. It holds the same snapshot,
 * changes it synchronously, and refuses calls the real session cannot reach.
 */
export interface FixtureConversation {
  getSnapshot: () => FixtureState;
  subscribe: (listener: () => void) => () => void;
  /** Like a send: shows the prompt optimistically and starts an empty live turn. */
  send: (text: string) => void;
  /** Reduces each event into the live turn, as the stream does. */
  apply: (events: TurnEvent[]) => void;
  /** Like the post-turn history read: stores the rows, then ends the turn. */
  commit: (history: AgentMessage[]) => void;
}

export const createFixtureConversation = ({
  history = [],
  ...seed
}: FixtureSeed = {}): FixtureConversation => {
  let state: FixtureState = {
    key: "story-conversation",
    items: projectHistory(history),
    optimistic: null,
    live: null,
    queued: null,
    busy: false,
    loading: false,
    error: "",
    failedPrompt: "",
    needsRecovery: false,
    ...seed,
  };
  let sent = 0;
  const listeners = new Set<() => void>();
  const update = (next: Partial<FixtureState>) => {
    state = { ...state, ...next };
    for (const listener of listeners) {
      listener();
    }
  };
  return {
    getSnapshot: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    send: (text) => {
      if (state.busy || state.loading || state.needsRecovery) {
        throw new Error("The session does not send while busy or unloaded");
      }
      sent += 1;
      update({
        busy: true,
        error: "",
        failedPrompt: "",
        live: emptyTurn(),
        optimistic: {
          id: `pending-user-${sent}`,
          role: "user",
          text: text.trim(),
          activities: [],
          artifacts: [],
        },
      });
    },
    apply: (events) => {
      if (!state.live) {
        throw new Error("Apply turn events after send");
      }
      let { live } = state;
      for (const event of events) {
        live = reduceTurn(live, event, `${state.key}:${sent}`, null);
      }
      update({ live });
    },
    commit: (rows) => {
      if (!state.busy) {
        throw new Error("Commit history after send");
      }
      // ConversationSession stores the history before it clears busy.
      update({ items: projectHistory(rows), live: null, optimistic: null });
      update({ busy: false });
    },
  };
};
