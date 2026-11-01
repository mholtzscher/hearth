import { AssistantRuntimeProvider, MessagePrimitive, ThreadPrimitive, useExternalStoreRuntime, type DataMessagePartProps, type ToolCallMessagePartProps } from "@assistant-ui/react";
import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { createConversation, listConversations, loadHistory, streamTurn, viewContextSchema } from "../api/agent";
import { useBaseUrlVersion } from "../api/client";
import { Canvas } from "../features/dashboards/Canvas";
import { useWorkspace } from "../features/dashboards/Workspace";
import { readDefinition } from "../features/views/catalog/compatibility";
import { ViewRenderer } from "../features/views/renderer/ViewRenderer";
import { emptyTurn, projectHistory, reduceTurn, type ConversationItem, type LiveTurn, type PresentedArtifact } from "../features/conversation/runtime";

type ArtifactDisplay = { select: (item: PresentedArtifact) => void; newest: string | undefined; visible: boolean };
const ArtifactContext = createContext<ArtifactDisplay>({ select: () => {}, newest: undefined, visible: true });
function ArtifactPart({ data }: DataMessagePartProps<PresentedArtifact>) {
  const { select, newest, visible } = useContext(ArtifactContext);
  const container = useRef<HTMLDivElement>(null);
  const [inViewport, setInViewport] = useState(typeof IntersectionObserver === "undefined");
  useEffect(() => {
    if (typeof IntersectionObserver === "undefined" || !container.current) return;
    const observer = new IntersectionObserver(entries => setInViewport(entries.some(entry => entry.isIntersecting)));
    observer.observe(container.current);
    return () => observer.disconnect();
  }, []);
  const view = readDefinition(data.artifact.definition);
  return <div ref={container} className="artifact-preview"><div className="artifact-top"><span className="artifact-icon">◫</span><span>Generated view</span></div><h3>{view.status === "supported" ? view.definition.title : view.status === "partial" ? view.title : "Unavailable view"}</h3><ViewRenderer definition={data.artifact.definition} active={visible && inViewport && data.artifact.artifact_id === newest} /><button onClick={() => select(data)}>Open in canvas →</button></div>;
}
function ContextPart({ data }: DataMessagePartProps<unknown>) {
  const parsed = viewContextSchema.safeParse(data);
  const view = parsed.success ? readDefinition(parsed.data.definition) : null;
  return <p className="context-badge">◫ {view?.status === "supported" ? `Selected view: ${view.definition.title}` : "Selected view context unavailable in this version"}</p>;
}
function ToolPart({ toolName, result, argsText }: ToolCallMessagePartProps) {
  return <details className="activity"><summary>{result === undefined ? "◌" : "✓"} {toolName} {result === undefined ? "· outcome unknown or pending" : "· complete"}</summary><p>{argsText}</p>{result !== undefined && <pre>{String(result)}</pre>}</details>;
}
const parts = { data: { by_name: { "hearth-view": ArtifactPart, "hearth-context": ContextPart } }, tools: { Fallback: ToolPart } };
function UserMessage() { return <div className="chat-message user"><MessagePrimitive.Parts components={parts} /></div>; }
function AssistantMessage() { return <div className="chat-message assistant"><div className="agent-label"><span>✳</span> Hearth</div><MessagePrimitive.Parts components={parts} /></div>; }

function convertMessage(item: ConversationItem, conversationId: string) {
  return {
    id: `${conversationId}:${item.id}`, role: item.role,
    content: [
      ...(item.text ? [{ type: "text" as const, text: item.text }] : []),
      ...(item.context ? [{ type: "data-hearth-context" as const, data: item.context }] : []),
      ...item.activities.map(call => ({ type: "tool-call" as const, toolCallId: call.key, toolName: call.name, argsText: call.arguments, ...(call.result !== undefined ? { result: call.result } : {}) })),
      ...item.artifacts.map(({ artifact, base }) => ({ type: "data-hearth-view" as const, id: artifact.artifact_id, data: { artifact, base } })),
    ],
  };
}

export default function AgentPage() {
  const { id: routeId } = useParams();
  const workspace = useWorkspace();
  const version = useBaseUrlVersion();
  const [items, setItems] = useState<ConversationItem[]>([]);
  const [conversations, setConversations] = useState<Awaited<ReturnType<typeof listConversations>>>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [mobileCanvas, setMobileCanvas] = useState(false);
  const [narrow, setNarrow] = useState(() => window.matchMedia?.("(max-width: 720px)").matches ?? false);
  const [live, setLive] = useState<LiveTurn | null>(null);
  const [optimistic, setOptimistic] = useState<ConversationItem | null>(null);
  const [failedPrompt, setFailedPrompt] = useState("");
  const [recoveryId, setRecoveryId] = useState<string | null>(null);
  const [sessionId, setSessionId] = useState<string | undefined>();
  const abort = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const list = async (signal?: AbortSignal) => setConversations(await listConversations(signal));
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
    void listConversations(controller.signal).then(value => { if (generation.current === current) setConversations(value); }).catch(err => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : "Could not load conversations."); });
    if (routeId) void loadHistory(routeId, controller.signal).then(rows => { if (generation.current === current) setItems(projectHistory(rows)); }).catch(err => { if (!controller.signal.aborted) { setError(err instanceof Error ? err.message : "Could not load conversation."); setRecoveryId(routeId); } }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => { controller.abort(); abort.current?.abort(); };
  }, [routeId, version]);

  async function send(text: string) {
    if (!text.trim() || busy || loading || recoveryId || workspace.saving || !workspace.editorValid) return;
    const prompt = text.trim();
    const context = workspace.context;
    const selectedDefinition = workspace.draft?.definition;
    const base = context?.source_dashboard_id && context.source_revision ? { dashboard_id: context.source_dashboard_id, revision: context.source_revision } : null;
    const controller = new AbortController(); abort.current = controller;
    setInput(""); setBusy(true); setError(""); setFailedPrompt("");
    let current = emptyTurn(); setLive(current);
    setOptimistic({ id: `pending-user-${Date.now()}`, role: "user", text: prompt, activities: [], artifacts: [], context });
    let id = routeId ?? sessionId;
    let failed = false;
    let reconciled = false;
    try {
      if (!id) { id = await createConversation(controller.signal); setSessionId(id); }
      const scope = `${id}:${crypto.randomUUID()}`;
      await streamTurn(id, prompt, context, event => {
        current = reduceTurn(current, event, scope, base); setLive(current);
        if (event.type === "view.presented" && event.artifact && (!workspace.draft || context)) workspace.receiveArtifact({ artifact: event.artifact, base }, selectedDefinition);
      }, controller.signal);
      if (!current.terminal) throw new Error("The connection ended before the turn completed.");
      if (current.failed) throw new Error(current.failed);
    } catch (err) {
      failed = true;
      if (!controller.signal.aborted) { setError(err instanceof Error ? err.message : "The request failed."); setInput(prompt); setFailedPrompt(prompt); }
    } finally {
      if (!controller.signal.aborted && id) {
        try {
          const projected = projectHistory(await loadHistory(id));
          const previousIds = new Set(items.flatMap(item => item.artifacts.map(value => value.artifact.artifact_id)));
          const discovered = projected.flatMap(item => item.artifacts).filter(value => !previousIds.has(value.artifact.artifact_id));
          const latest = discovered[discovered.length - 1];
          if (latest && (!workspace.draft || context)) workspace.receiveArtifact(latest, selectedDefinition);
          setItems(projected); setOptimistic(null); setLive(null); reconciled = true;
        }
        catch { setRecoveryId(id); setError(previous => `${previous ? `${previous} ` : ""}Could not reconcile history; streamed activity is retained here.`); }
        void list().catch(() => {});
      }
      if (!controller.signal.aborted) setBusy(false);
      abort.current = null;
      if (!controller.signal.aborted && !routeId && id && !failed && reconciled) workspace.navigateRetainingDraft(`/conversations/${encodeURIComponent(id)}`);
    }
  }
  async function recoverHistory() {
    if (!recoveryId) return;
    setLoading(true);
    try { setItems(projectHistory(await loadHistory(recoveryId))); setLive(null); setOptimistic(null); setRecoveryId(null); setError(""); }
    catch (err) { setError(err instanceof Error ? err.message : "Could not reload history."); }
    finally { setLoading(false); }
  }
  const allItems = useMemo(() => [...items, ...(optimistic ? [optimistic] : []), ...(live ? [{ id: "live", role: "assistant" as const, text: "", activities: live.activities, artifacts: live.artifacts }] : [])], [items, optimistic, live]);
  const artifacts = allItems.flatMap(item => item.artifacts);
  const newest = artifacts[artifacts.length - 1]?.artifact.artifact_id;
  const runtime = useExternalStoreRuntime({
    messages: allItems, isRunning: busy, isLoading: loading, isSendDisabled: loading || !!recoveryId || workspace.saving || !workspace.editorValid, convertMessage: item => convertMessage(item, routeId ?? "new"),
    onNew: async message => { const text = message.content.filter(part => part.type === "text").map(part => part.text).join("\n"); await send(text); },
  });
  const select = (item: PresentedArtifact) => workspace.selectArtifact(item, () => setMobileCanvas(true));
  return <div className={`conversation-workspace${mobileCanvas ? " show-canvas" : ""}`}>
    <div className="mobile-tabs" role="group" aria-label="Workspace panels"><button aria-pressed={!mobileCanvas} onClick={() => setMobileCanvas(false)}>Conversation</button><button aria-pressed={mobileCanvas} onClick={() => setMobileCanvas(true)}>Canvas ↗</button></div>
    <div className="workspace-body"><section className="conversation" aria-label="Conversation with Hearth">
      <div className="conversation-heading"><div><h1>A conversation with your home.</h1><p>Ask a question. Make yourself at home.</p></div><button className="new-conversation" disabled={busy} onClick={() => workspace.startFresh(() => { if (routeId) workspace.navigateRetainingDraft("/"); else { setSessionId(undefined); setItems([]); setLive(null); setOptimistic(null); setInput(""); setError(""); setFailedPrompt(""); setRecoveryId(null); } })}>＋ New</button></div>
      {conversations.length > 0 && <details className="conversation-list"><summary>Conversations</summary>{conversations.map(c => <Link key={c.id} to={`/conversations/${encodeURIComponent(c.id)}`} aria-current={routeId === c.id ? "page" : undefined}>{c.preview || "Empty conversation"}</Link>)}</details>}
      <AssistantRuntimeProvider runtime={runtime}><ArtifactContext.Provider value={{ select, newest, visible: !narrow || !mobileCanvas }}><div className="messages" role="log" aria-live="polite">
        {loading && <p role="status">Loading conversation…</p>}
        {!loading && allItems.length === 0 && <div className="empty-chat"><span>✳</span><h2>What would make<br />home feel better?</h2><p>Ask about your home or make something useful.</p><button onClick={() => setInput("Show me the temperatures throughout the house.")}>Show me the temperatures →</button></div>}
        <ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage }} />
        {busy && <p className="working" role="status">Looking through your home…</p>}
        {error && <div className="workspace-notice" role="alert">{error}{failedPrompt && <p>Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}{recoveryId && <button disabled={loading} onClick={() => void recoverHistory()}>Retry loading history</button>}</div>}
        {!error && failedPrompt && <p className="working">Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}
      </div></ArtifactContext.Provider></AssistantRuntimeProvider>
      <form className="composer-wrap" onSubmit={event => { event.preventDefault(); if (input.trim() && !busy && !loading && !recoveryId && !workspace.saving && workspace.editorValid) runtime.thread.append(input.trim()); }}>
        <div className="composer">{workspace.context && <div className="context-badge">◫ Selected view: {workspace.context.definition.title} {workspace.draft?.base ? `· revision ${workspace.draft.base.revision}` : "· unsaved"}</div>}
          <label className="sr-only" htmlFor="hearth-prompt">Ask Hearth</label><textarea id="hearth-prompt" value={input} maxLength={4000} disabled={busy || loading} placeholder="Ask a question. Make yourself at home." onChange={event => setInput(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); if (input.trim() && !busy && !loading && !recoveryId && !workspace.saving && workspace.editorValid) runtime.thread.append(input.trim()); } }} />
          <div className="composer-footer"><span>{workspace.editorValid ? `Your home · ${workspace.context ? "view in context" : "conversation"}` : "Fix or discard invalid view edits before sending"}</span><button className="send" disabled={busy || loading || !!recoveryId || workspace.saving || !workspace.editorValid || !input.trim()} aria-label={failedPrompt ? "Retry message" : "Send message"}>{failedPrompt ? "Retry ↑" : "↑"}</button></div></div>
        <p>Hearth can use household tools; actions may have real effects.</p>
      </form>
    </section><Canvas active={!narrow || mobileCanvas} /></div>
  </div>;
}
