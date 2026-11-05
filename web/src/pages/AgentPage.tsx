import { AssistantRuntimeProvider, MessagePrimitive, ThreadPrimitive, useExternalStoreRuntime, type DataMessagePartProps } from "@assistant-ui/react";
import { ArrowRightIcon, ArrowUpIcon, AsteriskIcon, CheckIcon, ChevronRightIcon, CircleDashedIcon, PanelsTopLeftIcon } from "lucide-react";
import { createContext, useContext, useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { createConversation, loadHistory, streamTurn, viewContextSchema } from "../api/agent";
import { useBaseUrlVersion } from "../api/client";
import { Canvas } from "../features/dashboards/Canvas";
import { useWorkspace } from "../features/dashboards/Workspace";
import { readDefinition } from "../features/views/catalog/compatibility";
import { useConversations } from "../features/conversation/ConversationsProvider";
import { emptyTurn, groupReplies, projectHistory, reduceTurn, type Activity, type ConversationItem, type LiveTurn, type PresentedArtifact } from "../features/conversation/runtime";

const STARTERS = [
  { label: "Show me the temperatures", prompt: "Show me the temperatures throughout the house." },
  { label: "Which devices are unavailable?", prompt: "Which devices are unavailable right now?" },
  { label: "Chart light brightness today", prompt: "Show light brightness over the past day." },
];

function plural(count: number, noun: string) { return `${count} ${noun}${count === 1 ? "" : "s"}`; }
/** Title and a short content summary, so the conversation can point at a view without rendering it twice. */
function describeView(definition: unknown) {
  const view = readDefinition(definition);
  if (view.status === "unavailable") return { title: "Unavailable view", summary: "Needs a newer Hearth version" };
  const elements = view.status === "supported" ? Object.values(view.definition.spec.elements) : Object.values(view.elements);
  const readings = elements.filter(element => element?.type === "Metric").length;
  const charts = elements.filter(element => element?.type === "TimeSeries").length;
  const parts = [readings ? plural(readings, "reading") : "", charts ? plural(charts, "chart") : ""].filter(Boolean);
  return {
    title: view.status === "supported" ? view.definition.title : view.title,
    summary: view.status === "partial" ? "View · Some content needs a newer Hearth version" : ["View", ...parts].join(" · "),
  };
}

type ArtifactDisplay = { select: (item: PresentedArtifact) => void; openId: string | null };
const ArtifactContext = createContext<ArtifactDisplay>({ select: () => {}, openId: null });
function ArtifactPart({ data }: DataMessagePartProps<PresentedArtifact>) {
  const { select, openId } = useContext(ArtifactContext);
  const { title, summary } = describeView(data.artifact.definition);
  const open = openId === data.artifact.artifact_id;
  return <div className={`artifact-preview${open ? " open" : ""}`}>
    <span className="artifact-icon"><PanelsTopLeftIcon aria-hidden="true" /></span>
    <div className="artifact-text"><h3>{title}</h3><p>{summary}</p></div>
    <button onClick={() => select(data)}>{open ? <><CheckIcon aria-hidden="true" />Showing in canvas</> : <>Open in canvas <ArrowRightIcon aria-hidden="true" /></>}</button>
  </div>;
}
function ContextPart({ data }: DataMessagePartProps<unknown>) {
  const parsed = viewContextSchema.safeParse(data);
  const view = parsed.success ? readDefinition(parsed.data.definition) : null;
  return <p className="context-badge"><PanelsTopLeftIcon aria-hidden="true" />{view?.status === "supported" ? `Selected view: ${view.definition.title}` : "Selected view context unavailable in this version"}</p>;
}
/** A reply's tool calls stay one line until expanded; each call keeps its arguments and result. */
function ActivityPart({ data }: DataMessagePartProps<Activity[]>) {
  const pending = data.filter((call: Activity) => call.result === undefined).length;
  return <details className="activity-group"><summary><ChevronRightIcon className="activity-chevron" aria-hidden="true" />{pending ? <CircleDashedIcon aria-hidden="true" /> : <CheckIcon aria-hidden="true" />}Used {plural(data.length, "tool")}{pending ? ` · ${pending} pending or unknown` : ""}</summary>
    <ul>{data.map((call: Activity) => <li key={call.key}><details className="activity"><summary><ChevronRightIcon className="activity-chevron" aria-hidden="true" />{call.result === undefined ? <CircleDashedIcon aria-hidden="true" /> : <CheckIcon aria-hidden="true" />} {call.name} {call.result === undefined ? "· outcome unknown or pending" : "· complete"}</summary><p>{call.arguments}</p>{call.result !== undefined && <pre>{call.result}</pre>}</details></li>)}</ul>
  </details>;
}
const parts = { data: { by_name: { "hearth-view": ArtifactPart, "hearth-context": ContextPart, "hearth-activity": ActivityPart } } };
function UserMessage() { return <div className="chat-message user"><MessagePrimitive.Parts components={parts} /></div>; }
function AssistantMessage() { return <div className="chat-message assistant"><div className="agent-label"><AsteriskIcon aria-hidden="true" /> Hearth</div><MessagePrimitive.Parts components={parts} /></div>; }

function convertMessage(item: ConversationItem, conversationId: string) {
  const text = item.text ? [{ type: "text" as const, text: item.text }] : [];
  return {
    id: `${conversationId}:${item.id}`, role: item.role,
    // A reply reads in the order it happened: tools, then the views they presented, then the answer.
    content: item.role === "user" ? [
      ...text,
      ...(item.context ? [{ type: "data-hearth-context" as const, data: item.context }] : []),
    ] : [
      ...(item.activities.length ? [{ type: "data-hearth-activity" as const, data: item.activities }] : []),
      ...item.artifacts.map(({ artifact, base }) => ({ type: "data-hearth-view" as const, id: artifact.artifact_id, data: { artifact, base } })),
      ...text,
    ],
  };
}

export default function AgentPage() {
  const { id: routeId } = useParams();
  const workspace = useWorkspace();
  const version = useBaseUrlVersion();
  const { newEpoch, setTurnActive, refresh: refreshConversations } = useConversations();
  const [items, setItems] = useState<ConversationItem[]>([]);
  const [input, setInput] = useState("");
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
  useEffect(() => setInput(""), [newEpoch]);
  useEffect(() => { setTurnActive(busy); return () => setTurnActive(false); }, [busy, setTurnActive]);

  async function send(text: string) {
    if (!text.trim() || busy || loading || recoveryId || workspace.saving || !workspace.editorValid) return;
    const prompt = text.trim();
    const context = workspace.context;
    const selectedDraft = workspace.draft;
    const draftVersion = workspace.draftVersion;
    const base = context?.source_dashboard_id && context.source_revision ? { dashboard_id: context.source_dashboard_id, revision: context.source_revision } : null;
    const controller = new AbortController(); abort.current = controller;
    const turnGeneration = generation.current;
    const isCurrent = () => !controller.signal.aborted && generation.current === turnGeneration && routeRef.current === routeId && versionRef.current === version;
    setInput(""); setBusy(true); setError(""); setFailedPrompt("");
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
        if (event.type === "view.presented" && event.artifact && (!selectedDraft || context)) workspace.receiveArtifact({ artifact: event.artifact, base }, draftVersion);
      }, controller.signal);
      if (!isCurrent()) return;
      if (!current.terminal) throw new Error("The connection ended before the turn completed.");
      if (current.failed) throw new Error(current.failed);
    } catch (err) {
      failed = true;
      if (isCurrent()) { setError(err instanceof Error ? err.message : "The request failed."); setInput(prompt); setFailedPrompt(prompt); }
    } finally {
      if (isCurrent() && id) {
        try {
          const rows = await loadHistory(id, controller.signal);
          if (isCurrent()) {
            const projected = projectHistory(rows);
            const previousIds = new Set(items.flatMap(item => item.artifacts.map(value => value.artifact.artifact_id)));
            const discovered = projected.flatMap(item => item.artifacts).filter(value => !previousIds.has(value.artifact.artifact_id));
            const latest = discovered[discovered.length - 1];
            if (latest && (!selectedDraft || context)) workspace.receiveArtifact(latest, draftVersion);
            setItems(projected); setOptimistic(null); setLive(null); reconciled = true;
          }
        }
        catch { if (isCurrent()) { setRecoveryId(id); setError(previous => `${previous ? `${previous} ` : ""}Could not reconcile history; streamed activity is retained here.`); } }
        if (isCurrent()) refreshConversations();
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
  const sendBlocked = loading || !!recoveryId || workspace.saving || !workspace.editorValid;
  const canSend = !busy && !sendBlocked;
  const runtime = useExternalStoreRuntime({
    messages: replies, isRunning: busy, isLoading: loading, isSendDisabled: sendBlocked, convertMessage: item => convertMessage(item, routeId ?? "new"),
    onNew: async message => { const text = message.content.filter(part => part.type === "text").map(part => part.text).join("\n"); await send(text); },
  });
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
    <div className={`workspace-body${draft ? " has-canvas" : ""}`}><section className="conversation" aria-label="Conversation with Hearth">
      <AssistantRuntimeProvider runtime={runtime}><ArtifactContext.Provider value={{ select, openId: draft?.origin_artifact_id ?? null }}><ThreadPrimitive.Viewport className="messages" role="log" aria-live="polite" scrollToBottomOnInitialize scrollToBottomOnThreadSwitch>
        {loading && <p role="status">Loading conversation…</p>}
        {!loading && allItems.length === 0 && <div className="empty-chat"><AsteriskIcon aria-hidden="true" /><h2>What would make home feel better?</h2><p>Ask about your home, or ask for a view you can keep as a dashboard.</p>
          <ul className="starters" aria-label="Suggested questions">{STARTERS.map(starter => <li key={starter.label}><button disabled={!canSend} onClick={() => runtime.thread.append(starter.prompt)}>{starter.label}<ArrowRightIcon aria-hidden="true" /></button></li>)}</ul></div>}
        <ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage }} />
        {busy && <p className="working" role="status">Looking through your home…</p>}
        {error && <div className="workspace-notice" role="alert">{error}{failedPrompt && <p>Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}{recoveryId && <button disabled={loading} onClick={() => void recoverHistory()}>Retry loading history</button>}</div>}
        {!error && failedPrompt && <p className="working">Your prompt is preserved below. Retrying starts a new request; tools may already have run.</p>}
      </ThreadPrimitive.Viewport></ArtifactContext.Provider></AssistantRuntimeProvider>
      <form className="composer-wrap" onSubmit={event => { event.preventDefault(); if (input.trim() && canSend) runtime.thread.append(input.trim()); }}>
        <div className="composer">{workspace.context && <div className="context-badge"><PanelsTopLeftIcon aria-hidden="true" />Selected view: {workspace.context.definition.title} {workspace.draft?.base ? `· revision ${workspace.draft.base.revision}` : "· unsaved"}</div>}
          <label className="sr-only" htmlFor="hearth-prompt">Ask Hearth</label><textarea ref={prompt} id="hearth-prompt" value={input} maxLength={4000} disabled={busy || loading} placeholder="Ask about your home, or ask for a view…" onChange={event => setInput(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); if (input.trim() && canSend) runtime.thread.append(input.trim()); } }} />
          <div className="composer-footer"><span>{!workspace.editorValid && "Fix or discard invalid view edits before sending"}</span><button className="send" disabled={!canSend || !input.trim()} aria-label={failedPrompt ? "Retry message" : "Send message"}>{failedPrompt && "Retry"}<ArrowUpIcon aria-hidden="true" /></button></div></div>
      </form>
    </section>{draft && <Canvas ref={canvas} active={!narrow || mobileCanvas} onClose={workspace.closeDraft} />}</div>
  </div>;
}
