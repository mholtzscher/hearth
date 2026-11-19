import {
  AssistantRuntimeProvider,
  ComposerPrimitive,
  SuggestionPrimitive,
  ThreadPrimitive,
  useExternalStoreRuntime,
  useThreadViewport,
} from "@assistant-ui/react";
import type { ThreadMessageLike, ThreadSuggestion } from "@assistant-ui/react";
import {
  ArrowDownIcon,
  ArrowRightIcon,
  ArrowUpIcon,
  PanelsTopLeftIcon,
  XIcon,
} from "lucide-react";
import type { RefObject } from "react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useParams } from "react-router-dom";

import { Button } from "../components/button";
import { Notice } from "../components/notice";
import { SegmentedControl } from "../components/segmented-control";
import { useConversationSession } from "../features/conversation/conversations-provider";
import { ConversationMessages } from "../features/conversation/messages";
import {
  groupReplies,
  pendingArtifactCalls,
} from "../features/conversation/runtime";
import type {
  ConversationItem,
  LiveTurn,
  PresentedArtifact,
} from "../features/conversation/runtime";
import { Canvas } from "../features/dashboards/canvas";
import { useWorkspace } from "../features/dashboards/workspace";
import { usePhoneBarSlot } from "../features/workspace/phone-bar-slot";
import { cn } from "../lib/utils";

const STARTERS: readonly ThreadSuggestion[] = [
  {
    title: "Show me the temperatures",
    prompt: "Show me the temperatures throughout the house.",
  },
  {
    title: "Which devices are unavailable?",
    prompt: "Which devices are unavailable right now?",
  },
  {
    title: "Chart light brightness today",
    prompt: "Show light brightness over the past day.",
  },
];

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

// The view or Automation on the canvas travels with the next message; the chip shows it and closes it like the canvas's own Close.
// Its revision and saved state stay in the canvas header, which is beside the composer or one tap away.
const SelectedContextChip = ({
  kind,
  title,
  closeDisabled,
  onShow,
  onClose,
}: {
  kind: string;
  title: string;
  closeDisabled: boolean;
  onShow: () => void;
  onClose: () => void;
}) => (
  <fieldset
    className="border-violet/35 text-label text-violet-text -ms-2 flex max-w-[calc(100%+8px)] min-w-0 justify-self-start rounded-md border [min-inline-size:0]"
    aria-label={`Selected ${kind}`}
  >
    <Button
      variant="ghost"
      size="touch"
      type="button"
      className="text-violet-text hover:bg-violet-soft min-w-0 shrink justify-start gap-2 ps-[7px] pe-1 text-left"
      aria-label={`Show selected ${kind}: ${title}`}
      title={title}
      onClick={onShow}
    >
      <PanelsTopLeftIcon aria-hidden="true" />
      <span className="min-w-0 truncate">{title}</span>
    </Button>
    <Button
      variant="ghost"
      size="icon-touch"
      type="button"
      className="hover:bg-violet-soft"
      aria-label={`Close selected ${kind}`}
      title={`Close ${kind}`}
      disabled={closeDisabled}
      onClick={onClose}
    >
      <XIcon aria-hidden="true" />
    </Button>
  </fieldset>
);

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

const convertMessage = (
  item: ConversationItem,
  conversationId: string,
  running: boolean
): ThreadMessageLike => {
  const text = item.text ? [{ type: "text" as const, text: item.text }] : [];
  return {
    id: `${conversationId}:${item.id}`,
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
            ...item.artifacts.map(({ artifact, base }) => ({
              type: "data-hearth-view" as const,
              id: artifact.artifact_id,
              data: { artifact, base },
            })),
            // Hold each presentation's place until Core has checked and committed it.
            ...(running
              ? (item.pendingArtifacts ?? []).map(({ id, kind }) => ({
                  type: "data-hearth-view-pending" as const,
                  id: `pending:${id}`,
                  data: { kind },
                }))
              : []),
            ...text,
          ],
  };
};

const ConversationPlaceholder = ({
  loading,
  empty,
}: {
  loading: boolean;
  empty: boolean;
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

// On phones the switch takes the top bar's center rather than a row of its own.
const WorkspacePanelSwitch = ({
  visible,
  phoneBar,
  canvas,
  onChange,
}: {
  visible: boolean;
  phoneBar: HTMLElement | null;
  canvas: boolean;
  onChange: (canvas: boolean) => void;
}) => {
  if (!visible) {
    return null;
  }
  const panels = (
    <div className={phoneBar ? "-translate-y-0.5" : "hidden"}>
      <SegmentedControl
        label="Workspace panels"
        size="touch"
        value={canvas ? "canvas" : "conversation"}
        options={[
          { value: "conversation", label: "Conversation" },
          { value: "canvas", label: "Canvas" },
        ]}
        onValueChange={(value) => onChange(value === "canvas")}
      />
    </div>
  );
  return phoneBar ? createPortal(panels, phoneBar) : panels;
};

const ConversationComposer = ({
  prompt,
  busy,
  loading,
  failedPrompt,
  automationName,
  viewTitle,
  saving,
  editorValid,
  onShow,
  onCloseDraft,
}: {
  prompt: RefObject<HTMLTextAreaElement | null>;
  busy: boolean;
  loading: boolean;
  failedPrompt: string;
  automationName: string | undefined;
  viewTitle: string | undefined;
  saving: boolean;
  editorValid: boolean;
  onShow: () => void;
  onCloseDraft: () => void;
}) => {
  let selected: { kind: string; title: string } | undefined;
  if (automationName !== undefined) {
    selected = { kind: "Automation", title: automationName };
  } else if (viewTitle !== undefined) {
    selected = { kind: "view", title: viewTitle };
  }
  const handleCloseDraft = () => {
    onCloseDraft();
  };
  return (
    <ComposerPrimitive.Root className="max-tablet:pb-[max(12px,var(--safe-bottom,env(safe-area-inset-bottom)))] shrink-0 px-[var(--pad)] pt-3 pb-5">
      <div className="rounded-card border-gray-4 bg-card focus-within:border-ring flex flex-col border p-2">
        <label className="sr-only" htmlFor="hearth-prompt">
          Ask Hearth
        </label>
        <ComposerPrimitive.Input
          className="text-body text-foreground caret-violet placeholder:text-muted-foreground block min-h-11 w-full resize-none overflow-y-auto border-0 bg-transparent px-2 py-2.5 leading-normal outline-none placeholder:opacity-100 focus-visible:outline-none"
          ref={prompt}
          id="hearth-prompt"
          minRows={1}
          maxRows={6}
          maxLength={4000}
          disabled={busy || loading}
          submitMode="enter"
          cancelOnEscape={false}
          addAttachmentOnPaste={false}
          placeholder="Ask about your home, or ask for a view…"
        />
        {/* The selected view and any note share Send's row rather than adding rows of their own. */}
        <div className="flex items-center gap-3">
          <div className="grid min-w-0 flex-1 gap-0.5 px-2">
            {selected && (
              <SelectedContextChip
                {...selected}
                closeDisabled={saving}
                onShow={onShow}
                onClose={handleCloseDraft}
              />
            )}
            {!editorValid && (
              <p className="text-caption text-muted-foreground">
                Fix or discard invalid draft edits before sending
              </p>
            )}
          </div>
          <ComposerPrimitive.Send asChild>
            <Button
              size="touch"
              className="ms-auto min-w-11 gap-1"
              aria-label={failedPrompt ? "Retry message" : "Send message"}
            >
              {failedPrompt && "Retry"}
              <ArrowUpIcon className="size-[18px]" aria-hidden="true" />
            </Button>
          </ComposerPrimitive.Send>
        </div>
      </div>
    </ComposerPrimitive.Root>
  );
};

const conversationItems = ({
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

const sendIsBlocked = ({
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

const AgentPage = () => {
  const { id: routeId } = useParams();
  const workspace = useWorkspace();
  const {
    session,
    items,
    busy,
    loading,
    error,
    live,
    optimistic,
    failedPrompt,
    needsRecovery,
    queued,
    id,
  } = useConversationSession(routeId);
  const [mobileCanvas, setMobileCanvas] = useState(false);
  const canvas = useRef<HTMLElement>(null);
  const prompt = useRef<HTMLTextAreaElement>(null);
  const {
    closeDraft,
    composerFocusRequested,
    finishComposerFocus,
    navigateRetainingDraft,
    receiveArtifact,
  } = workspace;
  const focusReady = composerFocusRequested && !routeId && !loading && !busy;
  // A ready focus request always lands in the conversation panel. Adjusting the
  // panel here, before the focus effect, keeps the old phone canvas from
  // covering the composer without a second render from an effect.
  if (focusReady && mobileCanvas) {
    setMobileCanvas(false);
  }
  useEffect(() => {
    if (!focusReady) {
      return;
    }
    const frame = requestAnimationFrame(() => {
      prompt.current?.focus();
      finishComposerFocus();
    });
    return () => cancelAnimationFrame(frame);
  }, [focusReady, finishComposerFocus]);
  const phoneBar = usePhoneBarSlot();
  const [narrow, setNarrow] = useState(
    () => window.matchMedia?.("(max-width: 720px)").matches ?? false
  );
  const visibleSession = useRef<typeof session | null>(session);
  useLayoutEffect(() => {
    visibleSession.current = session;
    return () => {
      visibleSession.current = null;
    };
  }, [session]);
  useEffect(() => {
    if (!routeId && id) {
      navigateRetainingDraft(`/conversations/${encodeURIComponent(id)}`);
    }
  }, [routeId, id, navigateRetainingDraft]);
  // Rebind when returning to a live conversation; offscreen turns have no canvas listener.
  useEffect(
    () =>
      session.subscribeArtifacts((item, expectedVersion) => {
        receiveArtifact(item, expectedVersion);
      }),
    [session, receiveArtifact]
  );
  useEffect(() => {
    const media = window.matchMedia?.("(max-width: 720px)");
    if (!media) {
      return;
    }
    const change = () => setNarrow(media.matches);
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  const send = async (text: string) => {
    if (workspace.saving || !workspace.editorValid) {
      return;
    }
    await session.send(text, {
      context: workspace.automationContext ?? workspace.context,
      draftVersion: workspace.draftVersion,
      onCreated: (createdId) => {
        if (visibleSession.current === session) {
          navigateRetainingDraft(
            `/conversations/${encodeURIComponent(createdId)}`
          );
        }
      },
    });
  };
  const allItems = useMemo(
    () => conversationItems({ items, optimistic, live, queued }),
    [items, optimistic, live, queued]
  );
  const replies = useMemo(() => groupReplies(allItems), [allItems]);
  const sendBlocked = sendIsBlocked({
    busy,
    loading,
    needsRecovery,
    saving: workspace.saving,
    editorValid: workspace.editorValid,
  });
  const runtime = useExternalStoreRuntime({
    messages: replies,
    isRunning: busy,
    isLoading: loading,
    isSendDisabled: sendBlocked,
    suggestions: STARTERS,
    convertMessage: (item) => convertMessage(item, session.key, busy),
    onNew: async (message) => {
      const text = message.content
        .filter((part) => part.type === "text")
        .map((part) => part.text)
        .join("\n");
      await send(text);
    },
  });
  useEffect(() => {
    // Only the visible session may seed the composer; reading its snapshot keeps
    // a conversation switch from carrying over the previous prompt.
    const { failedPrompt: sessionPrompt } = session.getSnapshot();
    if (sessionPrompt !== failedPrompt) {
      return;
    }
    runtime.thread.composer.setText(failedPrompt);
  }, [failedPrompt, runtime, session]);
  const draft = workspace.canvasDraft;
  const showCanvas = () => {
    setMobileCanvas(true);
    requestAnimationFrame(() => canvas.current?.focus());
  };
  const select = (item: PresentedArtifact) => {
    if (draft?.origin_artifact_id === item.artifact.artifact_id) {
      showCanvas();
    } else {
      workspace.selectArtifact(item, showCanvas);
    }
  };
  // Closing or discarding the view removes the canvas; keep keyboard focus in the conversation.
  const hadDraft = useRef(!!draft);
  useEffect(() => {
    const closed = hadDraft.current && !draft;
    hadDraft.current = !!draft;
    if (!closed) {
      return;
    }
    setMobileCanvas(false);
    // Wait for a closing confirmation dialog to release the page before moving focus.
    const frame = requestAnimationFrame(() => {
      if (document.activeElement === document.body) {
        prompt.current?.focus();
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [draft]);
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <h1 className="sr-only">Home workspace</h1>
      <WorkspacePanelSwitch
        visible={!!draft}
        phoneBar={phoneBar}
        canvas={mobileCanvas}
        onChange={setMobileCanvas}
      />
      <div
        className={cn(
          "max-tablet:block max-tablet:overflow-hidden grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)]",
          draft &&
            "tablet:max-desktop:grid-cols-[minmax(280px,.9fr)_minmax(0,1.1fr)] grid-cols-[clamp(368px,28vw,480px)_minmax(0,1fr)]"
        )}
      >
        <AssistantRuntimeProvider runtime={runtime}>
          <ThreadPrimitive.Root asChild>
            <section
              className={cn(
                "bg-background selection:bg-violet/40 selection:text-foreground max-desktop:[--gutter:20px] max-tablet:h-full short:overflow-y-auto flex min-h-0 min-w-0 flex-col [--gutter:26px] [--pad:var(--gutter)]",
                draft
                  ? "border-border max-tablet:border-r-0 border-r [--gutter:20px]"
                  : "[--pad:max(var(--gutter),calc((100%_-_960px)/2))]",
                mobileCanvas && "max-tablet:hidden"
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
                      loading={loading}
                      empty={allItems.length === 0}
                    />
                    <ConversationMessages
                      select={select}
                      openId={draft?.origin_artifact_id ?? null}
                      visible={!narrow || !mobileCanvas}
                    />
                    {busy && (
                      <output className="text-label text-muted-foreground mt-3 block">
                        Looking through your home…
                      </output>
                    )}
                    <QueuedPromptStatus
                      queued={queued}
                      busy={busy}
                      loading={loading}
                      failedPrompt={failedPrompt}
                      needsRecovery={needsRecovery}
                      saving={workspace.saving}
                      editorValid={workspace.editorValid}
                      onSendQueued={() => session.sendQueued()}
                      onDiscardFailed={() => session.discardFailedPrompt()}
                      onCancelQueued={() => session.cancelQueued()}
                    />
                    <ConversationErrorStatus
                      error={error}
                      failedPrompt={failedPrompt}
                      needsRecovery={needsRecovery}
                      loading={loading}
                      onRetry={() => session.load()}
                    />
                  </ThreadPrimitive.Viewport>
                  <JumpToLatest />
                </div>
                <ConversationComposer
                  prompt={prompt}
                  busy={busy}
                  loading={loading}
                  failedPrompt={failedPrompt}
                  automationName={
                    workspace.automationDraft
                      ? workspace.automationDraft.document.definition.name
                      : undefined
                  }
                  viewTitle={workspace.context?.definition.title}
                  saving={workspace.saving}
                  editorValid={workspace.editorValid}
                  onShow={showCanvas}
                  onCloseDraft={closeDraft}
                />
              </ThreadPrimitive.ViewportProvider>
            </section>
          </ThreadPrimitive.Root>
        </AssistantRuntimeProvider>
        {draft && (
          <Canvas
            ref={canvas}
            placement="conversation"
            active={!narrow || mobileCanvas}
            onClose={() => closeDraft()}
          />
        )}
      </div>
    </div>
  );
};

export default AgentPage;
