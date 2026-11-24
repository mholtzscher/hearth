import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
} from "@assistant-ui/react";
import type { ComponentProps, ReactNode } from "react";
import { useMemo, useRef, useSyncExternalStore } from "react";

import { ConversationThread } from "../src/features/conversation/conversation-thread";
import { groupReplies } from "../src/features/conversation/runtime";
import {
  conversationItems,
  convertMessage,
  promptText,
  sendIsBlocked,
  STARTERS,
} from "../src/features/conversation/thread-messages";
import { useTurnMessageIds } from "../src/features/conversation/turn-ids";
import type { FixtureConversation } from "./fixture-conversation";

interface DraftState {
  saving: boolean;
  editorValid: boolean;
}

/**
 * The external store runtime AgentPage builds, over a fixture conversation
 * instead of a ConversationSession. The canvas draft's saving and validity
 * come from the story, as AgentPage reads them from the workspace.
 */
const useFixtureRuntime = (
  conversation: FixtureConversation,
  { saving, editorValid }: DraftState
) => {
  const state = useSyncExternalStore(
    conversation.subscribe,
    conversation.getSnapshot
  );
  const { key, items, optimistic, live, queued, busy, loading, needsRecovery } =
    state;
  const allItems = useMemo(
    () => conversationItems({ items, optimistic, live, queued }),
    [items, optimistic, live, queued]
  );
  const replies = useMemo(() => groupReplies(allItems), [allItems]);
  const messageId = useTurnMessageIds(key, replies, optimistic?.id);
  const runtime = useExternalStoreRuntime({
    messages: replies,
    isRunning: busy,
    isLoading: loading,
    isSendDisabled: sendIsBlocked({
      busy,
      loading,
      needsRecovery,
      saving,
      editorValid,
    }),
    suggestions: STARTERS,
    convertMessage: (item, index) =>
      convertMessage(item, messageId(item, index), busy),
    onNew: (message) => {
      conversation.send(promptText(message));
      return Promise.resolve();
    },
  });
  return {
    state,
    runtime,
    empty: allItems.length === 0,
    messageCount: replies.length,
  };
};

/** A runtime for components that read the composer or thread without the whole thread, such as ConversationComposer. */
export const FixtureConversationRuntime = ({
  conversation,
  children,
  ...draft
}: DraftState & {
  conversation: FixtureConversation;
  children: ReactNode;
}) => {
  const { runtime } = useFixtureRuntime(conversation, draft);
  return (
    <AssistantRuntimeProvider runtime={runtime}>
      {children}
    </AssistantRuntimeProvider>
  );
};

/**
 * ConversationThread over a fixture conversation. Session state comes from the
 * fixture; the story's args supply the draft state, callbacks, and display.
 */
export const FixtureConversationThread = ({
  conversation,
  state: args,
  display,
  composer,
  ...props
}: ComponentProps<typeof ConversationThread> & {
  conversation: FixtureConversation;
}) => {
  const { state, runtime, empty, messageCount } = useFixtureRuntime(
    conversation,
    args
  );
  // Story args are serialized between stories; a mounted textarea must not end up in them.
  const prompt = useRef<HTMLTextAreaElement>(null);
  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <ConversationThread
        {...props}
        state={{
          ...args,
          empty,
          loading: state.loading,
          busy: state.busy,
          error: state.error,
          failedPrompt: state.failedPrompt,
          needsRecovery: state.needsRecovery,
          queued: state.queued,
        }}
        display={{ ...display, threadKey: state.key, messageCount }}
        composer={{ ...composer, prompt }}
      />
    </AssistantRuntimeProvider>
  );
};
