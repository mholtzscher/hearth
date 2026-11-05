import { ActionBarPrimitive, AuiIf, groupPartByType, MessagePrimitive, ThreadPrimitive, useAuiState, type DataMessagePartProps, type ToolCallMessagePartProps } from "@assistant-ui/react";
import { MarkdownTextPrimitive } from "@assistant-ui/react-markdown";
import { ArrowRightIcon, CheckIcon, ChevronRightIcon, CircleDashedIcon, CopyIcon, HouseIcon, PanelsTopLeftIcon } from "lucide-react";
import { createContext, useContext, type ReactNode } from "react";
import remarkGfm from "remark-gfm";
import { viewContextSchema } from "../../api/agent";
import { readDefinition } from "../views/catalog/compatibility";
import type { PresentedArtifact } from "./runtime";

type ArtifactDisplay = { select: (item: PresentedArtifact) => void; openId: string | null };
const ArtifactContext = createContext<ArtifactDisplay>({ select: () => {}, openId: null });
const toolGroups = groupPartByType({ "tool-call": ["group-tools"] });
const markdownPlugins = [remarkGfm];

function plural(count: number, noun: string) { return `${count} ${noun}${count === 1 ? "" : "s"}`; }

function ArtifactPart({ data }: DataMessagePartProps<PresentedArtifact>) {
  const { select, openId } = useContext(ArtifactContext);
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

function ToolGroup({ count, pending, children }: { count: number; pending: number; children: ReactNode }) {
  return <details className="activity-group"><summary><ChevronRightIcon className="activity-chevron" aria-hidden="true" />{pending ? <CircleDashedIcon aria-hidden="true" /> : <CheckIcon aria-hidden="true" />}Used {plural(count, "tool")}{pending ? ` · ${pending} pending or unknown` : ""}</summary><ul>{children}</ul></details>;
}

function ToolPart({ toolName, argsText, result }: ToolCallMessagePartProps) {
  return <li><details className="activity"><summary><ChevronRightIcon className="activity-chevron" aria-hidden="true" />{result === undefined ? <CircleDashedIcon aria-hidden="true" /> : <CheckIcon aria-hidden="true" />} {toolName} {result === undefined ? "· outcome unknown or pending" : "· complete"}</summary><p>{argsText}</p>{result !== undefined && <pre>{String(result)}</pre>}</details></li>;
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
    <div className="agent-label"><HouseIcon aria-hidden="true" /> Hearth</div>
    <MessagePrimitive.GroupedParts groupBy={toolGroups} indicator="never">{({ part, children }) => {
      switch (part.type) {
        case "group-tools": return <ToolGroup count={part.indices.length} pending={part.indices.length - part.counts.complete}>{children}</ToolGroup>;
        case "tool-call": return <ToolPart {...part} />;
        case "text": return <MarkdownTextPrimitive className="message-markdown" remarkPlugins={markdownPlugins} smooth={false} skipHtml />;
        case "data": return part.name === "hearth-view" ? <ArtifactPart {...part} /> : null;
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

export function ConversationMessages({ select, openId }: ArtifactDisplay) {
  return <ArtifactContext.Provider value={{ select, openId }}><ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage }} /></ArtifactContext.Provider>;
}
