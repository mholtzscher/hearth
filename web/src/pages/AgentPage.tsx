import { AssistantRuntimeProvider, ComposerPrimitive, SuggestionPrimitive, ThreadPrimitive, useExternalStoreRuntime, useThreadViewport, type ThreadMessageLike, type ThreadSuggestion } from "@assistant-ui/react";
import { ArrowDownIcon, ArrowRightIcon, ArrowUpIcon, PanelsTopLeftIcon, XIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useParams } from "react-router-dom";
import { usePhoneBarSlot } from "../components/PhoneBarSlot";
import { Button } from "../components/ui/button";
import { Notice } from "../components/ui/notice";
import { SegmentedControl } from "../components/ui/segmented-control";
import { cn } from "../lib/utils";
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
  return isAtBottom ? null : <ThreadPrimitive.ScrollToBottom asChild><Button variant="outline" size="touch" className="absolute bottom-3 left-1/2 max-w-[calc(100%-32px)] -translate-x-1/2" aria-label="Jump to latest"><ArrowDownIcon aria-hidden="true" />Jump to latest</Button></ThreadPrimitive.ScrollToBottom>;
}

// The view or Automation on the canvas travels with the next message; the chip shows it and closes it like the canvas's own Close.
// Its revision and saved state stay in the canvas header, which is beside the composer or one tap away.
function SelectedContextChip({ kind, title, closeDisabled, onShow, onClose }: { kind: string; title: string; closeDisabled: boolean; onShow: () => void; onClose: () => void }) {
  return <div className="flex max-w-[calc(100%+8px)] min-w-0 justify-self-start -ms-2 rounded-md border border-violet/35 text-label leading-normal text-violet-text" role="group" aria-label={`Selected ${kind}`}>
    <Button variant="ghost" size="touch" type="button" className="min-w-0 shrink justify-start gap-2 ps-[7px] pe-1 text-left text-violet-text hover:bg-violet-soft" aria-label={`Show selected ${kind}: ${title}`} title={title} onClick={onShow}><PanelsTopLeftIcon aria-hidden="true" /><span className="min-w-0 truncate">{title}</span></Button>
    <Button variant="ghost" size="icon-touch" type="button" className="hover:bg-violet-soft" aria-label={`Close selected ${kind}`} title={`Close ${kind}`} disabled={closeDisabled} onClick={onClose}><XIcon aria-hidden="true" /></Button>
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
  const panels = draft && <div className={phoneBar ? "-translate-y-0.5" : "hidden"}><SegmentedControl
    label="Workspace panels" size="touch" value={mobileCanvas ? "canvas" : "conversation"}
    options={[{ value: "conversation", label: "Conversation" }, { value: "canvas", label: "Canvas" }]}
    onValueChange={value => setMobileCanvas(value === "canvas")}
  /></div>;
  return <div className="flex min-h-0 flex-1 flex-col">
    <h1 className="sr-only">Home workspace</h1>
    {phoneBar ? createPortal(panels, phoneBar) : panels}
    <div className={cn("grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)] max-tablet:block max-tablet:overflow-hidden", draft && "grid-cols-[clamp(368px,28vw,480px)_minmax(0,1fr)] tablet:max-desktop:grid-cols-[minmax(280px,.9fr)_minmax(0,1.1fr)]")}><AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root className={cn("flex min-h-0 min-w-0 flex-col bg-background [--gutter:26px] [--pad:var(--gutter)] selection:bg-violet/40 selection:text-foreground max-desktop:[--gutter:20px] max-tablet:h-full short:overflow-y-auto", draft ? "[--gutter:20px] border-r border-border max-tablet:border-r-0" : "[--pad:max(var(--gutter),calc((100%_-_960px)/2))]", mobileCanvas && "max-tablet:hidden")} role="region" aria-label="Conversation with Hearth"><h2 className="sr-only">Conversation with Hearth</h2><ThreadPrimitive.ViewportProvider>
      <div className="relative flex min-h-0 flex-1 flex-col"><ThreadPrimitive.Viewport className="min-h-0 flex-1 overflow-auto px-[var(--pad)] pt-2 pb-[25px] [scrollbar-color:var(--gray-5)_transparent] [scrollbar-width:thin] max-tablet:pt-0 short:flex-[1_0_180px]" role="log" aria-live="polite" scrollToBottomOnInitialize scrollToBottomOnThreadSwitch>
        {loading && <p role="status">Loading conversation…</p>}
        {!loading && allItems.length === 0 && <div className="max-w-[520px] pt-[clamp(36px,12vh,140px)] pb-6 max-tablet:pt-[clamp(20px,5vh,48px)] short:pt-3"><h2 className="m-0 text-[28px] leading-[1.25] font-medium tracking-[-.03em] text-balance max-tablet:max-w-[18ch]">What would make home feel better?</h2>
          <ul className="mt-7 flex list-none flex-wrap gap-2" aria-label="Suggested questions"><ThreadPrimitive.Suggestions>{() => <li><SuggestionPrimitive.Trigger send asChild><Button variant="outline" size="touch" className="group text-left leading-[1.4]"><SuggestionPrimitive.Title /><ArrowRightIcon className="size-4 shrink-0 text-muted-foreground group-hover:text-violet" aria-hidden="true" /></Button></SuggestionPrimitive.Trigger></li>}</ThreadPrimitive.Suggestions></ul></div>}
        <ConversationMessages select={select} openId={draft?.origin_artifact_id ?? null} visible={!narrow || !mobileCanvas} />
        {busy && <p className="mt-3 text-label leading-normal text-muted-foreground" role="status">Looking through your home…</p>}
        {queued && <div className="mt-3 text-label leading-normal text-muted-foreground" role="status"><p>{busy || loading ? "Message queued until the current reply finishes." : "Queued message has not been sent."}</p>
          <div className="flex flex-wrap items-center gap-2">{!busy && !loading && <><Button variant="outline" size="touch" disabled={!!failedPrompt || needsRecovery || workspace.saving || !workspace.editorValid} onClick={() => void session.sendQueued()}>Send queued message</Button>{failedPrompt && <Button variant="outline" size="touch" onClick={() => session.discardFailedPrompt()}>Discard failed prompt</Button>}</>}<Button variant="outline" size="touch" onClick={() => session.cancelQueued()}>Cancel queued message</Button></div>
        </div>}
        {error && <Notice role="alert">{error}{failedPrompt && <p>Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}{needsRecovery && <Button className="m-1.5" variant="outline" size="touch" disabled={loading} onClick={() => void session.load()}>Retry loading history</Button>}</Notice>}
        {!error && failedPrompt && <p className="mt-3 text-label leading-normal text-muted-foreground">Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}
      </ThreadPrimitive.Viewport><JumpToLatest /></div>
      <ComposerPrimitive.Root className="shrink-0 px-[var(--pad)] pt-3 pb-5 max-tablet:pb-[max(12px,var(--safe-bottom,env(safe-area-inset-bottom)))]">
        <div className="flex flex-col rounded-card border border-gray-4 bg-card p-2 focus-within:border-ring">
          <label className="sr-only" htmlFor="hearth-prompt">Ask Hearth</label><ComposerPrimitive.Input className="block min-h-11 w-full resize-none overflow-y-auto border-0 bg-transparent px-2 py-2.5 text-body leading-normal text-foreground caret-violet outline-none placeholder:text-muted-foreground placeholder:opacity-100 focus-visible:outline-none" ref={prompt} id="hearth-prompt" minRows={1} maxRows={6} maxLength={4000} disabled={busy || loading} submitMode="enter" cancelOnEscape={false} addAttachmentOnPaste={false} placeholder="Ask about your home, or ask for a view…" />
          {/* The selected view and any note share Send's row rather than adding rows of their own. */}
          <div className="flex items-center gap-3"><div className="grid min-w-0 flex-1 gap-0.5 px-2">{selected && <SelectedContextChip {...selected} closeDisabled={workspace.saving} onShow={showCanvas} onClose={workspace.closeDraft} />}{!workspace.editorValid && <p className="text-caption leading-normal text-muted-foreground">Fix or discard invalid draft edits before sending</p>}</div><ComposerPrimitive.Send asChild><Button size="touch" className="ms-auto min-w-11 gap-1" aria-label={failedPrompt ? "Retry message" : "Send message"}>{failedPrompt && "Retry"}<ArrowUpIcon className="size-[18px]" aria-hidden="true" /></Button></ComposerPrimitive.Send></div></div>
      </ComposerPrimitive.Root>
    </ThreadPrimitive.ViewportProvider></ThreadPrimitive.Root></AssistantRuntimeProvider>{draft && <Canvas ref={canvas} placement="conversation" active={!narrow || mobileCanvas} onClose={workspace.closeDraft} />}</div>
  </div>;
}
