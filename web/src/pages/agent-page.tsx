import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
} from "@assistant-ui/react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useParams } from "react-router-dom";

import { SegmentedControl } from "../components/segmented-control";
import { ConversationThread } from "../features/conversation/conversation-thread";
import {
  useConversations,
  useConversationSession,
} from "../features/conversation/conversations-provider";
import { groupReplies } from "../features/conversation/runtime";
import type { PresentedArtifact } from "../features/conversation/runtime";
import { useStarterQuestionSelection } from "../features/conversation/starter-questions";
import {
  conversationItems,
  convertMessage,
  promptText,
  sendIsBlocked,
} from "../features/conversation/thread-messages";
import { useTurnMessageIds } from "../features/conversation/turn-ids";
import { Canvas } from "../features/dashboards/canvas";
import { useWorkspace } from "../features/dashboards/workspace";
import { usePhoneBarSlot } from "../features/workspace/phone-bar-slot";
import { canTravel, runTravel } from "../lib/canvas-travel";
import { cn } from "../lib/utils";

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
  const {
    starterQuestions,
    starterQuestionsEnabled,
    starterQuestionsPending,
    refreshStarterQuestions,
    regenerateStarterQuestions,
  } = useConversations();
  const starters = useStarterQuestionSelection({
    empty: allItems.length === 0,
    loading,
    served: starterQuestions,
    pending: starterQuestionsPending,
    enabled: starterQuestionsEnabled,
    refresh: refreshStarterQuestions,
    regenerate: regenerateStarterQuestions,
  });
  const messageId = useTurnMessageIds(session.key, replies, optimistic?.id);
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
    suggestions: starters.questions,
    convertMessage: (item, index) =>
      convertMessage(item, messageId(item, index), busy),
    onNew: async (message) => {
      await send(promptText(message));
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
  const [traveled, setTraveled] = useState<string | null>(null);
  const shareSelection = (artifactId: string) => (action: () => void) => {
    if (!canTravel()) {
      action();
      return;
    }
    runTravel(() => {
      setTraveled(artifactId);
      action();
    }, true);
  };
  const showCanvas = () => {
    setMobileCanvas(true);
    requestAnimationFrame(() => canvas.current?.focus());
  };
  const select = (item: PresentedArtifact) => {
    if (draft?.origin_artifact_id === item.artifact.artifact_id) {
      showCanvas();
    } else {
      workspace.selectArtifact(
        item,
        showCanvas,
        item.artifact.kind === "hearth.view"
          ? shareSelection(item.artifact.artifact_id)
          : undefined
      );
    }
  };
  const closeCanvas = () =>
    closeDraft(workspace.draft?.origin_artifact_id ? runTravel : undefined);
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
          <ConversationThread
            state={{
              loading,
              empty: allItems.length === 0,
              busy,
              error,
              failedPrompt,
              needsRecovery,
              queued,
              saving: workspace.saving,
              editorValid: workspace.editorValid,
            }}
            actions={{
              onSendQueued: () => session.sendQueued(),
              onDiscardFailed: () => session.discardFailedPrompt(),
              onCancelQueued: () => session.cancelQueued(),
              onRetryHistory: () => session.load(),
            }}
            display={{
              select,
              openId: draft?.origin_artifact_id ?? null,
              visible: !narrow || !mobileCanvas,
              threadKey: session.key,
              messageCount: replies.length,
              canvasOpen: !!draft,
              hidden: mobileCanvas,
            }}
            starters={{
              canRegenerate: starters.canRegenerate,
              regenerating: starters.regenerating,
              regenerateFailed: starters.regenerateFailed,
              onRegenerate: starters.regenerate,
            }}
            composer={{
              prompt,
              automationName: workspace.automationDraft
                ? workspace.automationDraft.document.definition.name
                : undefined,
              viewTitle: workspace.context?.definition.title,
              onShow: showCanvas,
              onCloseDraft: closeCanvas,
            }}
          />
        </AssistantRuntimeProvider>
        {draft && (
          <Canvas
            ref={canvas}
            placement="conversation"
            active={!narrow || mobileCanvas}
            slide={!traveled || traveled !== draft.origin_artifact_id}
            onClose={closeCanvas}
          />
        )}
      </div>
    </div>
  );
};

export default AgentPage;
