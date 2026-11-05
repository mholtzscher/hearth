import { AssistantRuntimeProvider, ComposerPrimitive, ThreadPrimitive, useExternalStoreRuntime, useThreadViewport, type ThreadMessageLike } from "@assistant-ui/react";
import { ArrowDownIcon, ArrowRightIcon, ArrowUpIcon, PanelsTopLeftIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { createConversation, loadHistory, streamTurn } from "../api/agent";
import { useBaseUrlVersion } from "../api/client";
import { Canvas } from "../features/dashboards/Canvas";
import { useWorkspace } from "../features/dashboards/Workspace";
import { useConversations } from "../features/conversation/ConversationsProvider";
import { ConversationMessages } from "../features/conversation/Messages";
import { emptyTurn, groupReplies, projectHistory, reduceTurn, type ConversationItem, type LiveTurn, type PresentedArtifact } from "../features/conversation/runtime";

const STARTERS = [
  { label: "Show me the temperatures", prompt: "Show me the temperatures throughout the house." },
  { label: "Which devices are unavailable?", prompt: "Which devices are unavailable right now?" },
  { label: "Chart light brightness today", prompt: "Show light brightness over the past day." },
];

function JumpToLatest() {
  const isAtBottom = useThreadViewport(state => state.isAtBottom);
  return isAtBottom ? null : <ThreadPrimitive.ScrollToBottom className="jump-to-latest" aria-label="Jump to latest"><ArrowDownIcon aria-hidden="true" />Jump to latest</ThreadPrimitive.ScrollToBottom>;
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
    // Summarize tool activity before the presented views and combined reply text.
    content: item.role === "user" ? [
      ...text,
      ...(item.context ? [{ type: "data-hearth-context" as const, data: item.context }] : []),
    ] : [
      ...item.activities.map(call => ({ type: "tool-call" as const, toolCallId: call.key, toolName: call.name, argsText: call.arguments, ...(call.result !== undefined ? { result: call.result } : {}) })),
      ...item.artifacts.map(({ artifact, base }) => ({ type: "data-hearth-view" as const, id: artifact.artifact_id, data: { artifact, base } })),
      ...text,
    ],
  };
}

export default function AgentPage() {
  const { id: routeId } = useParams();
  const workspace = useWorkspace();
  const version = useBaseUrlVersion();
  const { newEpoch, setTurnActive } = useConversations();
  const [items, setItems] = useState<ConversationItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
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
  const [narrow, setNarrow] = useState(() => window.matchMedia?.("(max-width: 720px)").matches ?? false);
  const [live, setLive] = useState<LiveTurn | null>(null);
  const [optimistic, setOptimistic] = useState<ConversationItem | null>(null);
  const [failedPrompt, setFailedPrompt] = useState("");
  const [recoveryId, setRecoveryId] = useState<string | null>(null);
  const [sessionId, setSessionId] = useState<string | undefined>();
  const abort = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const routeRef = useRef(routeId);
  const versionRef = useRef(version);
  routeRef.current = routeId;
  versionRef.current = version;
  useEffect(() => {
    const media = window.matchMedia?.("(max-width: 720px)");
    if (!media) return;
    const change = () => setNarrow(media.matches);
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    const current = ++generation.current;
    setItems([]); setLive(null); setOptimistic(null); setError(""); setFailedPrompt(""); setRecoveryId(null); setBusy(false); setSessionId(undefined); setLoading(!!routeId);
    if (routeId) void loadHistory(routeId, controller.signal).then(rows => { if (generation.current === current) setItems(projectHistory(rows)); }).catch(err => { if (!controller.signal.aborted) { setError(err instanceof Error ? err.message : "Could not load conversation."); setRecoveryId(routeId); } }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => { controller.abort(); abort.current?.abort(); };
  }, [routeId, version, newEpoch]);
  useEffect(() => { setTurnActive(busy); return () => setTurnActive(false); }, [busy, setTurnActive]);

  async function send(text: string) {
    if (!text.trim() || busy || loading || recoveryId || workspace.saving || !workspace.editorValid) return;
    const prompt = text.trim();
    const context = workspace.context;
    const draftVersion = workspace.draftVersion;
    const base = context?.source_dashboard_id && context.source_revision ? { dashboard_id: context.source_dashboard_id, revision: context.source_revision } : null;
    const controller = new AbortController(); abort.current = controller;
    const turnGeneration = generation.current;
    const isCurrent = () => !controller.signal.aborted && generation.current === turnGeneration && routeRef.current === routeId && versionRef.current === version;
    setBusy(true); setError(""); setFailedPrompt("");
    let current = emptyTurn(); setLive(current);
    setOptimistic({ id: `pending-user-${Date.now()}`, role: "user", text: prompt, activities: [], artifacts: [], context });
    let id = routeId ?? sessionId;
    let failed = false;
    let reconciled = false;
    try {
      if (!id) { id = await createConversation(controller.signal); if (!isCurrent()) return; setSessionId(id); }
      const scope = `${id}:${crypto.randomUUID()}`;
      await streamTurn(id, prompt, context, event => {
        if (!isCurrent()) return;
        current = reduceTurn(current, event, scope, base); setLive(current);
        // New views appear in the reply; only a view already open in the canvas follows Hearth's revision.
        if (event.type === "view.presented" && event.artifact && context) workspace.receiveArtifact({ artifact: event.artifact, base }, draftVersion);
      }, controller.signal);
      if (!isCurrent()) return;
      if (!current.terminal) throw new Error("The connection ended before the turn completed.");
      if (current.failed) throw new Error(current.failed);
    } catch (err) {
      failed = true;
      if (isCurrent()) { setError(err instanceof Error ? err.message : "The request failed."); runtime.thread.composer.setText(prompt); setFailedPrompt(prompt); }
    } finally {
      if (isCurrent() && id) {
        try {
          const rows = await loadHistory(id, controller.signal);
          if (isCurrent()) {
            const projected = projectHistory(rows);
            const previousIds = new Set(items.flatMap(item => item.artifacts.map(value => value.artifact.artifact_id)));
            const discovered = projected.flatMap(item => item.artifacts).filter(value => !previousIds.has(value.artifact.artifact_id));
            const latest = discovered[discovered.length - 1];
            if (latest && context) workspace.receiveArtifact(latest, draftVersion);
            setItems(projected); setOptimistic(null); setLive(null); reconciled = true;
          }
        }
        catch { if (isCurrent()) { setRecoveryId(id); setError(previous => `${previous ? `${previous} ` : ""}Could not reconcile history; streamed activity is retained here.`); } }
      }
      if (isCurrent()) setBusy(false);
      if (abort.current === controller) abort.current = null;
      if (isCurrent() && !routeId && id && !failed && reconciled) workspace.navigateRetainingDraft(`/conversations/${encodeURIComponent(id)}`);
    }
  }
  async function recoverHistory() {
    if (!recoveryId) return;
    const controller = new AbortController(); abort.current = controller;
    const recoveryGeneration = generation.current;
    const isCurrent = () => !controller.signal.aborted && generation.current === recoveryGeneration && routeRef.current === routeId && versionRef.current === version;
    setLoading(true);
    try { const rows = await loadHistory(recoveryId, controller.signal); if (isCurrent()) { setItems(projectHistory(rows)); setLive(null); setOptimistic(null); setRecoveryId(null); setError(""); } }
    catch (err) { if (isCurrent()) setError(err instanceof Error ? err.message : "Could not reload history."); }
    finally { if (isCurrent()) setLoading(false); if (abort.current === controller) abort.current = null; }
  }
  const allItems = useMemo(() => [...items, ...(optimistic ? [optimistic] : []), ...(live ? [{ id: "live", role: "assistant" as const, text: "", activities: live.activities, artifacts: live.artifacts }] : [])], [items, optimistic, live]);
  const replies = useMemo(() => groupReplies(allItems), [allItems]);
  const sendBlocked = busy || loading || !!recoveryId || workspace.saving || !workspace.editorValid;
  const runtime = useExternalStoreRuntime({
    messages: replies, isRunning: busy, isLoading: loading, isSendDisabled: sendBlocked, convertMessage: item => convertMessage(item, routeId ?? "new", busy),
    onNew: async message => { const text = message.content.filter(part => part.type === "text").map(part => part.text).join("\n"); await send(text); },
  });
  useEffect(() => runtime.thread.composer.setText(""), [newEpoch, version, runtime]);
  const draft = workspace.draft;
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
  return <div className={`conversation-workspace${mobileCanvas ? " show-canvas" : ""}`}>
    <h1 className="sr-only">Home workspace</h1>
    {draft && <div className="mobile-tabs" role="group" aria-label="Workspace panels"><button aria-pressed={!mobileCanvas} onClick={() => setMobileCanvas(false)}>Conversation</button><button aria-pressed={mobileCanvas} onClick={() => setMobileCanvas(true)}>Canvas</button></div>}
    <div className={`workspace-body${draft ? " has-canvas" : ""}`}><AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root className="conversation" role="region" aria-label="Conversation with Hearth"><ThreadPrimitive.ViewportProvider>
      <div className="message-area"><ThreadPrimitive.Viewport className="messages" role="log" aria-live="polite" scrollToBottomOnInitialize scrollToBottomOnThreadSwitch>
        {loading && <p role="status">Loading conversation…</p>}
        {!loading && allItems.length === 0 && <div className="empty-chat"><h2>What would make home feel better?</h2>
          <ul className="starters" aria-label="Suggested questions">{STARTERS.map(starter => <li key={starter.label}><ThreadPrimitive.Suggestion prompt={starter.prompt} send disabled={sendBlocked}>{starter.label}<ArrowRightIcon aria-hidden="true" /></ThreadPrimitive.Suggestion></li>)}</ul></div>}
        <ConversationMessages select={select} openId={draft?.origin_artifact_id ?? null} visible={!narrow || !mobileCanvas} />
        {busy && <p className="working" role="status">Looking through your home…</p>}
        {error && <div className="workspace-notice" role="alert">{error}{failedPrompt && <p>Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}{recoveryId && <button disabled={loading} onClick={() => void recoverHistory()}>Retry loading history</button>}</div>}
        {!error && failedPrompt && <p className="working">Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}
      </ThreadPrimitive.Viewport><JumpToLatest /></div>
      <ComposerPrimitive.Root className="composer-wrap">
        <div className="composer">{workspace.context && <div className="context-badge"><PanelsTopLeftIcon aria-hidden="true" />Selected view: {workspace.context.definition.title} {workspace.draft?.base ? `· revision ${workspace.draft.base.revision}` : "· unsaved"}</div>}
          <label className="sr-only" htmlFor="hearth-prompt">Ask Hearth</label><ComposerPrimitive.Input ref={prompt} id="hearth-prompt" minRows={2} maxRows={6} maxLength={4000} disabled={busy || loading} submitMode="enter" cancelOnEscape={false} addAttachmentOnPaste={false} placeholder="Ask about your home, or ask for a view…" />
          <ComposerPrimitive.Send className="send" aria-label={failedPrompt ? "Retry message" : "Send message"}>{failedPrompt && "Retry"}<ArrowUpIcon aria-hidden="true" /></ComposerPrimitive.Send>
          {!workspace.editorValid && <p className="composer-note">Fix or discard invalid view edits before sending</p>}</div>
      </ComposerPrimitive.Root>
    </ThreadPrimitive.ViewportProvider></ThreadPrimitive.Root></AssistantRuntimeProvider>{draft && <Canvas ref={canvas} active={!narrow || mobileCanvas} onClose={workspace.closeDraft} />}</div>
  </div>;
}
