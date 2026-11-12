import { AssistantRuntimeProvider, ComposerPrimitive, SuggestionPrimitive, ThreadPrimitive, useExternalStoreRuntime, useThreadViewport, type ThreadMessageLike, type ThreadSuggestion } from "@assistant-ui/react";
import { ArrowDownIcon, ArrowRightIcon, ArrowUpIcon, PanelsTopLeftIcon, XIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useParams } from "react-router-dom";
import { usePhoneBarSlot } from "../components/PhoneBarSlot";
import { Canvas } from "../features/dashboards/Canvas";
import { useWorkspace } from "../features/dashboards/Workspace";
import { useConversationSession } from "../features/conversation/ConversationsProvider";
import { ConversationMessages } from "../features/conversation/Messages";
import { groupReplies, pendingArtifactCalls, type ConversationItem, type PresentedArtifact } from "../features/conversation/runtime";

const STARTERS: readonly ThreadSuggestion[] = [
  { title: "Show me the temperatures", prompt: "Show me the temperatures throughout the house." },
  { title: "Which devices are unavailable?", prompt: "Which devices are unavailable right now?" },
  { title: "Chart light brightness today", prompt: "Show light brightness over the past day." },
];

function JumpToLatest() {
  const isAtBottom = useThreadViewport(state => state.isAtBottom);
  return isAtBottom ? null : <ThreadPrimitive.ScrollToBottom className="jump-to-latest" aria-label="Jump to latest"><ArrowDownIcon aria-hidden="true" />Jump to latest</ThreadPrimitive.ScrollToBottom>;
}

// The view or Automation on the canvas travels with the next message; the chip shows it and closes it like the canvas's own Close.
// Its revision and saved state stay in the canvas header, which is beside the composer or one tap away.
function SelectedContextChip({ kind, title, closeDisabled, onShow, onClose }: { kind: string; title: string; closeDisabled: boolean; onShow: () => void; onClose: () => void }) {
  return <div className="context-chip" role="group" aria-label={`Selected ${kind}`}>
    <button type="button" className="context-chip-show" aria-label={`Show selected ${kind}: ${title}`} title={title} onClick={onShow}><PanelsTopLeftIcon aria-hidden="true" /><span className="context-chip-title">{title}</span></button>
    <button type="button" className="context-chip-close" aria-label={`Close selected ${kind}`} title={`Close ${kind}`} disabled={closeDisabled} onClick={onClose}><XIcon aria-hidden="true" /></button>
  </div>;
}

function convertMessage(item: ConversationItem, conversationId: string, running: boolean): ThreadMessageLike {
  const text = item.text ? [{ type: "text" as const, text: item.text }] : [];
  const status: ThreadMessageLike["status"] = item.id === "live" && running
    ? { type: "running" }
    : item.activities.some(call => call.result === undefined)
      ? { type: "incomplete", reason: "other" }
      : { type: "complete", reason: "stop" };
  return {
    id: `${conversationId}:${item.id}`, role: item.role,
    ...(item.role === "assistant" ? { status } : {}),
    // Summarize tool activity before the presented views and terminal reply text.
    content: item.role === "user" ? [
      ...text,
      ...(item.context ? [{ type: "data-hearth-context" as const, data: item.context }] : []),
    ] : [
      ...item.activities.map(call => ({ type: "tool-call" as const, toolCallId: call.key, toolName: call.name, argsText: call.arguments, ...(call.result !== undefined ? { result: call.result } : {}) })),
      ...item.artifacts.map(({ artifact, base }) => ({ type: "data-hearth-view" as const, id: artifact.artifact_id, data: { artifact, base } })),
      // Hold each presentation's place until Core has checked and committed it.
      ...(running ? (item.pendingArtifacts ?? []).map(({ id, kind }) => ({ type: "data-hearth-view-pending" as const, id: `pending:${id}`, data: { kind } })) : []),
      ...text,
    ],
  };
}

export default function AgentPage() {
  const { id: routeId } = useParams();
  const workspace = useWorkspace();
  const { session, items, busy, loading, error, live, optimistic, failedPrompt, needsRecovery, queued, id } = useConversationSession(routeId);
  const [mobileCanvas, setMobileCanvas] = useState(false);
  const canvas = useRef<HTMLElement>(null);
  const prompt = useRef<HTMLTextAreaElement>(null);
  const { composerFocusRequested, finishComposerFocus } = workspace;
  useEffect(() => {
    if (!composerFocusRequested || routeId || loading || busy) return;
    if (mobileCanvas) { setMobileCanvas(false); return; }
    const frame = requestAnimationFrame(() => {
      prompt.current?.focus();
      finishComposerFocus();
    });
    return () => cancelAnimationFrame(frame);
  }, [composerFocusRequested, finishComposerFocus, routeId, loading, busy, mobileCanvas]);
  const phoneBar = usePhoneBarSlot();
  const [narrow, setNarrow] = useState(() => window.matchMedia?.("(max-width: 720px)").matches ?? false);
  const visibleSession = useRef<typeof session | null>(session);
  visibleSession.current = session;
  useEffect(() => {
    visibleSession.current = session;
    return () => { visibleSession.current = null; };
  }, [session]);
  useEffect(() => {
    if (!routeId && id) workspace.navigateRetainingDraft(`/conversations/${encodeURIComponent(id)}`);
  }, [routeId, id, workspace.navigateRetainingDraft]);
  // Rebind when returning to a live conversation; offscreen turns have no canvas listener.
  useEffect(() => session.subscribeArtifacts((item, expectedVersion) => {
    workspace.receiveArtifact(item, expectedVersion);
  }), [session, workspace.receiveArtifact]);
  useEffect(() => {
    const media = window.matchMedia?.("(max-width: 720px)");
    if (!media) return;
    const change = () => setNarrow(media.matches);
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  async function send(text: string) {
    if (workspace.saving || !workspace.editorValid) return;
    await session.send(text, {
      context: workspace.automationContext ?? workspace.context,
      draftVersion: workspace.draftVersion,
      onCreated: id => {
        if (visibleSession.current === session) workspace.navigateRetainingDraft(`/conversations/${encodeURIComponent(id)}`);
      },
    });
  }
  const allItems = useMemo(() => [...items, ...(optimistic ? [optimistic] : []), ...(live ? [{ id: "live", role: "assistant" as const, text: live.reply, activities: live.activities, artifacts: live.artifacts, pendingArtifacts: pendingArtifactCalls(live) }] : []), ...(queued ? [queued] : [])], [items, optimistic, live, queued]);
  const replies = useMemo(() => groupReplies(allItems), [allItems]);
  const sendBlocked = busy || loading || needsRecovery || workspace.saving || !workspace.editorValid;
  const runtime = useExternalStoreRuntime({
    messages: replies, isRunning: busy, isLoading: loading, isSendDisabled: sendBlocked, suggestions: STARTERS, convertMessage: item => convertMessage(item, session.key, busy),
    onNew: async message => { const text = message.content.filter(part => part.type === "text").map(part => part.text).join("\n"); await send(text); },
  });
  useEffect(() => runtime.thread.composer.setText(failedPrompt), [session, failedPrompt, runtime]);
  const draft = workspace.canvasDraft;
  const selected = workspace.automationDraft ? { kind: "Automation", title: workspace.automationDraft.document.definition.name }
    : workspace.context && { kind: "view", title: workspace.context.definition.title };
  function showCanvas() {
    setMobileCanvas(true);
    requestAnimationFrame(() => canvas.current?.focus());
  }
  const select = (item: PresentedArtifact) => {
    if (draft?.origin_artifact_id === item.artifact.artifact_id) showCanvas();
    else workspace.selectArtifact(item, showCanvas);
  };
  // Closing or discarding the view removes the canvas; keep keyboard focus in the conversation.
  const hadDraft = useRef(!!draft);
  useEffect(() => {
    const closed = hadDraft.current && !draft;
    hadDraft.current = !!draft;
    if (!closed) return;
    setMobileCanvas(false);
    // Wait for a closing confirmation dialog to release the page before moving focus.
    const frame = requestAnimationFrame(() => { if (document.activeElement === document.body) prompt.current?.focus(); });
    return () => cancelAnimationFrame(frame);
  }, [draft]);
  // On phones the switch takes the top bar's center rather than a row of its own.
  const panels = draft && <div className="mobile-tabs" role="group" aria-label="Workspace panels"><button aria-pressed={!mobileCanvas} onClick={() => setMobileCanvas(false)}>Conversation</button><button aria-pressed={mobileCanvas} onClick={() => setMobileCanvas(true)}>Canvas</button></div>;
  return <div className={`conversation-workspace${mobileCanvas ? " show-canvas" : ""}`}>
    <h1 className="sr-only">Home workspace</h1>
    {phoneBar ? createPortal(panels, phoneBar) : panels}
    <div className={`workspace-body${draft ? " has-canvas" : ""}`}><AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root className="conversation" role="region" aria-label="Conversation with Hearth"><ThreadPrimitive.ViewportProvider>
      <div className="message-area"><ThreadPrimitive.Viewport className="messages" role="log" aria-live="polite" scrollToBottomOnInitialize scrollToBottomOnThreadSwitch>
        {loading && <p role="status">Loading conversation…</p>}
        {!loading && allItems.length === 0 && <div className="empty-chat"><h2>What would make home feel better?</h2>
          <ul className="starters" aria-label="Suggested questions"><ThreadPrimitive.Suggestions>{() => <li><SuggestionPrimitive.Trigger send><SuggestionPrimitive.Title /><ArrowRightIcon aria-hidden="true" /></SuggestionPrimitive.Trigger></li>}</ThreadPrimitive.Suggestions></ul></div>}
        <ConversationMessages select={select} openId={draft?.origin_artifact_id ?? null} visible={!narrow || !mobileCanvas} />
        {busy && <p className="working" role="status">Looking through your home…</p>}
        {queued && <div className="working" role="status"><p>{busy || loading ? "Message queued until the current reply finishes." : "Queued message has not been sent."}</p>
          <div className="workspace-actions">{!busy && !loading && <><button disabled={!!failedPrompt || needsRecovery || workspace.saving || !workspace.editorValid} onClick={() => void session.sendQueued()}>Send queued message</button>{failedPrompt && <button onClick={() => session.discardFailedPrompt()}>Discard failed prompt</button>}</>}<button onClick={() => session.cancelQueued()}>Cancel queued message</button></div>
        </div>}
        {error && <div className="workspace-notice" role="alert">{error}{failedPrompt && <p>Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}{needsRecovery && <button disabled={loading} onClick={() => void session.load()}>Retry loading history</button>}</div>}
        {!error && failedPrompt && <p className="working">Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}
      </ThreadPrimitive.Viewport><JumpToLatest /></div>
      <ComposerPrimitive.Root className="composer-wrap">
        <div className="composer">
          <label className="sr-only" htmlFor="hearth-prompt">Ask Hearth</label><ComposerPrimitive.Input ref={prompt} id="hearth-prompt" minRows={1} maxRows={6} maxLength={4000} disabled={busy || loading} submitMode="enter" cancelOnEscape={false} addAttachmentOnPaste={false} placeholder="Ask about your home, or ask for a view…" />
          {/* The selected view and any note share Send's row rather than adding rows of their own. */}
          <div className="composer-footer"><div className="composer-meta">{selected && <SelectedContextChip {...selected} closeDisabled={workspace.saving} onShow={showCanvas} onClose={workspace.closeDraft} />}{!workspace.editorValid && <p className="composer-note">Fix or discard invalid draft edits before sending</p>}</div><ComposerPrimitive.Send className="send" aria-label={failedPrompt ? "Retry message" : "Send message"}>{failedPrompt && "Retry"}<ArrowUpIcon aria-hidden="true" /></ComposerPrimitive.Send></div></div>
      </ComposerPrimitive.Root>
    </ThreadPrimitive.ViewportProvider></ThreadPrimitive.Root></AssistantRuntimeProvider>{draft && <Canvas ref={canvas} active={!narrow || mobileCanvas} onClose={workspace.closeDraft} />}</div>
  </div>;
}
