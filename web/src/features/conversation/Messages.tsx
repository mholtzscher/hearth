import { ActionBarPrimitive, AuiIf, groupPartByType, MessagePrimitive, ThreadPrimitive, useAuiState, useThreadViewport, type DataMessagePartProps, type ToolCallMessagePartProps } from "@assistant-ui/react";
import { MarkdownTextPrimitive } from "@assistant-ui/react-markdown";
import { ArrowRightIcon, CheckIcon, ChevronRightIcon, CircleDashedIcon, CopyIcon, HouseIcon, PanelsTopLeftIcon } from "lucide-react";
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import remarkGfm from "remark-gfm";
import { viewContextSchema } from "../../api/agent";
import { readDefinition } from "../views/catalog/compatibility";
import { ViewRenderer } from "../views/renderer/ViewRenderer";
import type { PresentedArtifact } from "./runtime";

type ArtifactDisplay = { select: (item: PresentedArtifact) => void; openId: string | null; visible: boolean };
const ArtifactContext = createContext<ArtifactDisplay>({ select: () => {}, openId: null, visible: true });
const toolGroups = groupPartByType({ "tool-call": ["group-tools"] });
const markdownPlugins = [remarkGfm];

function plural(count: number, noun: string) { return `${count} ${noun}${count === 1 ? "" : "s"}`; }

/**
 * Keeps a view's readings live only while its card is within a screen of view, so long conversations do not load every view.
 * The margin lets a card switch while the reader cannot see it.
 */
function useNearScreen() {
  const ref = useRef<HTMLDivElement>(null);
  // The conversation scroller clips its content, so a margin around the page viewport would never reach offscreen cards.
  const root = useThreadViewport(state => state.element.viewport);
  const [near, setNear] = useState(typeof IntersectionObserver === "undefined");
  useEffect(() => {
    if (typeof IntersectionObserver === "undefined" || !ref.current) return;
    const observer = new IntersectionObserver(entries => setNear(entries.some(entry => entry.isIntersecting)), { root, rootMargin: "100% 0px" });
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, [root]);
  return [ref, near] as const;
}

/** A card that stops loading keeps the height its readings had, so the conversation never moves under the reader. */
function ArtifactBody({ definition, live }: { definition: unknown; live: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [height, setHeight] = useState<number>();
  useEffect(() => {
    const body = ref.current;
    if (!live || !body || typeof ResizeObserver === "undefined") return;
    // A hidden conversation measures zero; keep the last visible height.
    const observer = new ResizeObserver(() => { if (body.offsetHeight > 0) setHeight(body.offsetHeight); });
    observer.observe(body);
    return () => observer.disconnect();
  }, [live]);
  return <div ref={ref} className="artifact-body" aria-live="off" style={live ? undefined : { minHeight: height }}><ViewRenderer definition={definition} active={live} headingLevel={4} /></div>;
}

function ArtifactPart({ data }: DataMessagePartProps<PresentedArtifact>) {
  const { select, openId, visible } = useContext(ArtifactContext);
  const [ref, near] = useNearScreen();
  const view = readDefinition(data.artifact.definition);
  const title = view.status === "supported" ? view.definition.title : view.status === "partial" ? view.title : "Unavailable view";
  let summary = "Needs a newer Hearth version";
  if (view.status === "partial") summary = "View · Some content needs a newer Hearth version";
  if (view.status === "supported") {
    const elements = Object.values(view.definition.spec.elements);
    const readings = elements.filter(element => element.type === "Metric").length;
    const charts = elements.filter(element => element.type === "TimeSeries").length;
    summary = ["View", readings ? plural(readings, "reading") : "", charts ? plural(charts, "chart") : ""].filter(Boolean).join(" · ");
  }
  // The canvas already shows the open view, so its card stays compact.
  const open = openId === data.artifact.artifact_id;
  return <div ref={ref} className={`artifact-preview${open ? " open" : ""}`}>
    <div className="artifact-header">
      <span className="artifact-icon"><PanelsTopLeftIcon aria-hidden="true" /></span>
      <div className="artifact-text"><h3>{title}</h3><p>{summary}</p></div>
      <button onClick={() => select(data)}>{open ? <><CheckIcon aria-hidden="true" />Showing in canvas</> : <>Open in canvas <ArrowRightIcon aria-hidden="true" /></>}</button>
    </div>
    {!open && <ArtifactBody definition={data.artifact.definition} live={visible && near} />}
  </div>;
}

/** Holds a view's place while the model writes it and Core checks it, so the reply shows a view is coming. */
function PendingViewPart() {
  return <div className="artifact-preview pending" role="status">
    <div className="artifact-header">
      <span className="artifact-icon"><PanelsTopLeftIcon aria-hidden="true" /></span>
      <div className="artifact-text"><h3>Preparing a view</h3><p>Putting it together…</p></div>
    </div>
    <div className="artifact-body pending-tiles" aria-hidden="true"><span /><span /></div>
  </div>;
}

function ContextPart({ data }: DataMessagePartProps<unknown>) {
  const parsed = viewContextSchema.safeParse(data);
  const view = parsed.success ? readDefinition(parsed.data.definition) : null;
  return <p className="context-badge"><PanelsTopLeftIcon aria-hidden="true" />{view?.status === "supported" ? `Selected view: ${view.definition.title}` : "Selected view context unavailable in this version"}</p>;
}

// Reply metadata rows share one icon column: status icon at the reply's edge, text after it, disclosure last.
function ToolGroup({ count, pending, children }: { count: number; pending: number; children: ReactNode }) {
  return <details className="activity-group"><summary>{pending ? <CircleDashedIcon aria-hidden="true" /> : <CheckIcon aria-hidden="true" />}<span>Used {plural(count, "tool")}{pending ? ` · ${pending} pending or unknown` : ""}</span><ChevronRightIcon className="activity-chevron" aria-hidden="true" /></summary><ul>{children}</ul></details>;
}

function ToolPart({ toolName, argsText, result }: ToolCallMessagePartProps) {
  return <li><details className="activity"><summary>{result === undefined ? <CircleDashedIcon aria-hidden="true" /> : <CheckIcon aria-hidden="true" />}<span>{toolName} {result === undefined ? "· outcome unknown or pending" : "· complete"}</span><ChevronRightIcon className="activity-chevron" aria-hidden="true" /></summary><p>{argsText}</p>{result !== undefined && <pre>{String(result)}</pre>}</details></li>;
}

const userParts = { data: { by_name: { "hearth-context": ContextPart } } };
function UserMessage() { return <div className="chat-message user"><MessagePrimitive.Parts components={userParts} /></div>; }

function CopyResponse() {
  const copied = useAuiState(state => state.message.isCopied);
  return <ActionBarPrimitive.Copy className="copy-response" aria-label={copied ? "Response copied" : "Copy response"} title={copied ? "Response copied" : "Copy response"}>
    {copied ? <><CheckIcon aria-hidden="true" />Copied</> : <><CopyIcon aria-hidden="true" />Copy</>}
  </ActionBarPrimitive.Copy>;
}

function AssistantMessage() {
  return <div className="chat-message assistant">
    <div className="agent-label"><HouseIcon aria-hidden="true" />Hearth</div>
    <MessagePrimitive.GroupedParts groupBy={toolGroups} indicator="never">{({ part, children }) => {
      switch (part.type) {
        case "group-tools": return <ToolGroup count={part.indices.length} pending={part.indices.length - part.counts.complete}>{children}</ToolGroup>;
        case "tool-call": return <ToolPart {...part} />;
        case "text": return <MarkdownTextPrimitive className="message-markdown" remarkPlugins={markdownPlugins} smooth={false} skipHtml />;
        case "data":
          if (part.name === "hearth-view") return <ArtifactPart {...part} />;
          return part.name === "hearth-view-pending" ? <PendingViewPart /> : null;
        default: return null;
      }
    }}</MessagePrimitive.GroupedParts>
    <AuiIf condition={s => s.message.parts.some(part => part.type === "text" && !!part.text.trim())}>
      <ActionBarPrimitive.Root className="message-actions" hideWhenRunning>
        <CopyResponse />
      </ActionBarPrimitive.Root>
    </AuiIf>
  </div>;
}

export function ConversationMessages({ select, openId, visible }: ArtifactDisplay) {
  return <ArtifactContext.Provider value={{ select, openId, visible }}><ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage }} /></ArtifactContext.Provider>;
}
