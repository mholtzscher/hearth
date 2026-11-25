import {
  ThreadPrimitive,
  SuggestionPrimitive,
  useThreadViewport,
} from "@assistant-ui/react";
import { ArrowDownIcon, ArrowRightIcon, RefreshCwIcon } from "lucide-react";

import { Button } from "../../components/button";
import { Notice } from "../../components/notice";
import { cn } from "../../lib/utils";
import { ConversationComposer } from "./composer";
import type { ComposerProps } from "./composer";
import { ConversationMessages } from "./messages";
import type { ConversationItem, PresentedArtifact } from "./runtime";

export interface ThreadState {
  loading: boolean;
  /** No stored, optimistic, live, or queued item; shows the starter questions. */
  empty: boolean;
  busy: boolean;
  error: string;
  failedPrompt: string;
  needsRecovery: boolean;
  queued: ConversationItem | null;
  /** The canvas draft is saving; blocks queued sends and closing the selected context. */
  saving: boolean;
  editorValid: boolean;
}

export interface ThreadActions {
  onSendQueued: () => Promise<void>;
  onDiscardFailed: () => void;
  onCancelQueued: () => void;
  onRetryHistory: () => Promise<void>;
}

/** The "Suggest other questions" control under the starter questions. */
export interface StarterQuestionControls {
  canRegenerate: boolean;
  regenerating: boolean;
  regenerateFailed: boolean;
  onRegenerate: () => Promise<void>;
}

export interface ThreadDisplay {
  /** Selects an artifact for the canvas. */
  select: (item: PresentedArtifact) => void;
  openId: string | null;
  visible: boolean;
  threadKey: string;
  messageCount: number;
  /** A canvas is open beside the thread; narrows its gutter and adds a border. */
  canvasOpen: boolean;
  /** Phone layout with the canvas panel showing. */
  hidden: boolean;
}

const JumpToLatest = () => {
  const isAtBottom = useThreadViewport((state) => state.isAtBottom);
  return isAtBottom ? null : (
    <ThreadPrimitive.ScrollToBottom asChild>
      <Button
        variant="outline"
        size="touch"
        className="absolute bottom-3 left-1/2 max-w-[calc(100%-32px)] -translate-x-1/2"
        aria-label="Jump to latest"
      >
        <ArrowDownIcon aria-hidden="true" />
        Jump to latest
      </Button>
    </ThreadPrimitive.ScrollToBottom>
  );
};

const ConversationPlaceholder = ({
  loading,
  empty,
  starters,
}: {
  loading: boolean;
  empty: boolean;
  starters?: StarterQuestionControls;
}) => {
  if (loading) {
    return <output className="block">Loading conversation…</output>;
  }
  if (!empty) {
    return null;
  }
  return (
    <div className="max-tablet:pt-[clamp(20px,5vh,48px)] short:pt-3 max-w-[520px] pt-[clamp(36px,12vh,140px)] pb-6">
      <h2 className="text-headline max-tablet:max-w-[18ch] m-0 text-balance">
        What would make home feel better?
      </h2>
      <ul
        className="mt-7 flex list-none flex-wrap gap-2"
        aria-label="Suggested questions"
      >
        <ThreadPrimitive.Suggestions>
          {() => (
            <li>
              <SuggestionPrimitive.Trigger send asChild>
                <Button
                  variant="outline"
                  size="touch"
                  className="group text-left"
                >
                  <SuggestionPrimitive.Title />
                  <ArrowRightIcon
                    className="text-muted-foreground group-hover:text-violet size-4 shrink-0"
                    aria-hidden="true"
                  />
                </Button>
              </SuggestionPrimitive.Trigger>
            </li>
          )}
        </ThreadPrimitive.Suggestions>
      </ul>
      {starters?.canRegenerate && (
        <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1">
          <Button
            variant="ghost"
            size="touch"
            disabled={starters.regenerating}
            onClick={() => {
              void starters.onRegenerate();
            }}
          >
            <RefreshCwIcon aria-hidden="true" />
            {starters.regenerating
              ? "Suggesting questions…"
              : "Suggest other questions"}
          </Button>
          {starters.regenerateFailed && (
            <output className="text-label text-muted-foreground">
              Couldn’t suggest new questions. These are still available.
            </output>
          )}
        </div>
      )}
    </div>
  );
};

const QueuedPromptStatus = ({
  queued,
  busy,
  loading,
  failedPrompt,
  needsRecovery,
  saving,
  editorValid,
  onSendQueued,
  onDiscardFailed,
  onCancelQueued,
}: {
  queued: ConversationItem | null;
  busy: boolean;
  loading: boolean;
  failedPrompt: string;
  needsRecovery: boolean;
  saving: boolean;
  editorValid: boolean;
  onSendQueued: () => Promise<void>;
  onDiscardFailed: () => void;
  onCancelQueued: () => void;
}) => {
  if (!queued) {
    return null;
  }
  const paused = busy || loading;
  return (
    <div className="text-label text-muted-foreground mt-3">
      <output className="block">
        {paused
          ? "Message queued until the current reply finishes."
          : "Queued message has not been sent."}
      </output>
      <div className="flex flex-wrap items-center gap-2">
        {!paused && (
          <>
            <Button
              variant="outline"
              size="touch"
              disabled={
                !!failedPrompt || needsRecovery || saving || !editorValid
              }
              onClick={() => {
                void onSendQueued();
              }}
            >
              Send queued message
            </Button>
            {failedPrompt && (
              <Button variant="outline" size="touch" onClick={onDiscardFailed}>
                Discard failed prompt
              </Button>
            )}
          </>
        )}
        <Button variant="outline" size="touch" onClick={onCancelQueued}>
          Cancel queued message
        </Button>
      </div>
    </div>
  );
};

const ConversationErrorStatus = ({
  error,
  failedPrompt,
  needsRecovery,
  loading,
  onRetry,
}: {
  error: string;
  failedPrompt: string;
  needsRecovery: boolean;
  loading: boolean;
  onRetry: () => Promise<void>;
}) => {
  if (error) {
    return (
      <Notice role="alert">
        {error}
        {failedPrompt && (
          <p>
            Your prompt is preserved below. Retrying starts a new request; tools
            may already have run.
          </p>
        )}
        {needsRecovery && (
          <Button
            className="m-1.5"
            variant="outline"
            size="touch"
            disabled={loading}
            onClick={() => {
              void onRetry();
            }}
          >
            Retry loading history
          </Button>
        )}
      </Notice>
    );
  }
  if (failedPrompt) {
    return (
      <p className="text-label text-muted-foreground mt-3">
        Your prompt is preserved below. Retrying starts a new request; tools may
        already have run.
      </p>
    );
  }
  return null;
};

/** The conversation log and composer. Renders inside an AssistantRuntimeProvider and owns no session or workspace state. */
export const ConversationThread = ({
  state,
  actions,
  display,
  composer,
  starters,
}: {
  state: ThreadState;
  actions: ThreadActions;
  display: ThreadDisplay;
  /** Omitted when the page offers no way to ask for other questions. */
  starters?: StarterQuestionControls;
  composer: Pick<
    ComposerProps,
    "prompt" | "automationName" | "viewTitle" | "onShow" | "onCloseDraft"
  >;
}) => {
  const { canvasOpen, hidden } = display;
  const { onSendQueued, onDiscardFailed, onCancelQueued, onRetryHistory } =
    actions;
  return (
    <ThreadPrimitive.Root asChild>
      <section
        className={cn(
          "bg-background selection:bg-violet/40 selection:text-foreground max-desktop:[--gutter:20px] max-tablet:h-full short:overflow-y-auto flex min-h-0 min-w-0 flex-col [--gutter:26px] [--pad:var(--gutter)]",
          canvasOpen
            ? "border-border max-tablet:border-r-0 border-r [--gutter:20px]"
            : "[--pad:max(var(--gutter),calc((100%_-_960px)/2))]",
          hidden && "max-tablet:hidden"
        )}
        aria-label="Conversation with Hearth"
      >
        <h2 className="sr-only">Conversation with Hearth</h2>
        <ThreadPrimitive.ViewportProvider>
          <div className="relative flex min-h-0 flex-1 flex-col">
            <ThreadPrimitive.Viewport
              className="max-tablet:pt-0 short:flex-[1_0_180px] min-h-0 flex-1 [scrollbar-width:thin] [scrollbar-color:var(--gray-5)_transparent] overflow-auto px-[var(--pad)] pt-2 pb-[25px]"
              role="log"
              aria-live="polite"
              scrollToBottomOnInitialize
              scrollToBottomOnThreadSwitch
            >
              <ConversationPlaceholder
                loading={state.loading}
                empty={state.empty}
                starters={starters}
              />
              <ConversationMessages
                select={display.select}
                openId={display.openId}
                visible={display.visible}
                threadKey={display.threadKey}
                historyLoading={state.loading}
                messageCount={display.messageCount}
              />
              {/* Pending (DESIGN.md, Motion): the words wait 600ms, so a quick reply never shows them. */}
              {state.busy && (
                <output
                  data-reduced-motion="fade"
                  className="animate-pending-words text-label text-muted-foreground mt-3 block"
                >
                  Looking through your home…
                </output>
              )}
              <QueuedPromptStatus
                queued={state.queued}
                busy={state.busy}
                loading={state.loading}
                failedPrompt={state.failedPrompt}
                needsRecovery={state.needsRecovery}
                saving={state.saving}
                editorValid={state.editorValid}
                onSendQueued={onSendQueued}
                onDiscardFailed={onDiscardFailed}
                onCancelQueued={onCancelQueued}
              />
              <ConversationErrorStatus
                error={state.error}
                failedPrompt={state.failedPrompt}
                needsRecovery={state.needsRecovery}
                loading={state.loading}
                onRetry={onRetryHistory}
              />
            </ThreadPrimitive.Viewport>
            <JumpToLatest />
          </div>
          <ConversationComposer
            {...composer}
            busy={state.busy}
            loading={state.loading}
            failedPrompt={state.failedPrompt}
            saving={state.saving}
            editorValid={state.editorValid}
          />
        </ThreadPrimitive.ViewportProvider>
      </section>
    </ThreadPrimitive.Root>
  );
};
