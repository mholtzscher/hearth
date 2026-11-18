import {
  ActionBarPrimitive,
  AuiIf,
  groupPartByType,
  MessagePrimitive,
  ThreadPrimitive,
  useAuiState,
  useThreadViewport,
} from "@assistant-ui/react";
import type {
  DataMessagePartProps,
  ToolCallMessagePartProps,
} from "@assistant-ui/react";
import { MarkdownTextPrimitive } from "@assistant-ui/react-markdown";
import {
  ArrowRightIcon,
  CheckIcon,
  ChevronRightIcon,
  CircleDashedIcon,
  CopyIcon,
  HouseIcon,
  PanelsTopLeftIcon,
} from "lucide-react";
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { ReactNode } from "react";
import remarkGfm from "remark-gfm";

import { automationContextSchema, viewContextSchema } from "../../api/agent";
import { Button } from "../../components/button";
import { Card } from "../../components/card";
import { StatusNotice } from "../../components/notice";
import { cn } from "../../lib/utils";
import { readAutomationDocument } from "../automations/document";
import { readDefinition } from "../views/catalog/compatibility";
import { ViewRenderer } from "../views/renderer/view-renderer";
import {
  artifactAction,
  artifactFrame,
  artifactHeader,
  artifactIcon,
  artifactSummary,
  artifactTitle,
} from "./artifact-styles";
import { AutomationArtifactPreview } from "./automation-artifact-preview";
import type { PresentedArtifact } from "./runtime";

interface ArtifactDisplay {
  select: (item: PresentedArtifact) => void;
  openId: string | null;
  visible: boolean;
}
const ArtifactContext = createContext<ArtifactDisplay>({
  select: () => null,
  openId: null,
  visible: true,
});
const toolGroups = groupPartByType({ "tool-call": ["group-tools"] });
const markdownPlugins = [remarkGfm];
// Markdown output is authored by the renderer, so its semantic elements are styled here.
const markdownContainerProps = {
  "data-slot": "message-markdown",
  className:
    "min-w-0 max-w-[720px] overflow-x-auto leading-[1.7] [&_:is(p,ul,ol,blockquote,pre,table)]:my-3 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0 [&_ul]:list-disc [&_ol]:list-decimal [&_:is(ul,ol)]:ps-6 [&_li]:my-1 [&_li>p]:my-1 [&_:is(h1,h2,h3,h4,h5,h6)]:mt-6 [&_:is(h1,h2,h3,h4,h5,h6)]:mb-2 [&_:is(h1,h2,h3,h4,h5,h6)]:text-title [&_:is(h1,h2,h3,h4,h5,h6)]:leading-[1.4] [&_:is(h1,h2,h3,h4,h5,h6)]:font-semibold [&_:is(h1,h2,h3,h4,h5,h6)]:text-foreground [&_a]:text-link [&_a]:underline [&_a]:underline-offset-[3px] [&_strong]:font-semibold [&_strong]:text-foreground [&_:not(pre)>code]:rounded-xs [&_:not(pre)>code]:border [&_:not(pre)>code]:border-border [&_:not(pre)>code]:bg-gray-2 [&_:not(pre)>code]:px-[5px] [&_:not(pre)>code]:py-px [&_:not(pre)>code]:text-[.85em] [&_:not(pre)>code]:[overflow-wrap:anywhere] [&_pre]:overflow-x-auto [&_pre]:rounded-card [&_pre]:border [&_pre]:border-border [&_pre]:bg-card [&_pre]:px-4 [&_pre]:py-3 [&_pre]:text-label [&_pre]:leading-normal [&_blockquote]:border-s [&_blockquote]:border-border [&_blockquote]:ps-3 [&_blockquote]:text-muted-foreground [&_table]:w-full [&_table]:border-separate [&_table]:border-spacing-0 [&_table]:rounded-card [&_table]:border [&_table]:border-border [&_table]:bg-card [&_table]:text-label [&_table]:leading-normal [&_table]:tabular-nums [&_:is(th,td)]:px-4 [&_:is(th,td)]:py-2.5 [&_:is(th,td)]:text-start [&_:is(th,td)]:align-top [&_th]:font-semibold [&_th]:text-muted-foreground [&_tbody>tr>*]:border-t [&_tbody>tr>*]:border-border",
};

const plural = (count: number, noun: string) =>
  `${count} ${noun}${count === 1 ? "" : "s"}`;

const titleFor = (view: ReturnType<typeof readDefinition>) => {
  if (view.status === "supported") {
    return view.definition.title;
  }
  if (view.status === "partial") {
    return view.title;
  }
  return "Unavailable view";
};

/**
 * Keeps a view's readings live only while its card is within a screen of view, so long conversations do not load every view.
 * The margin lets a card switch while the reader cannot see it.
 */
const useNearScreen = () => {
  const ref = useRef<HTMLDivElement>(null);
  // The conversation scroller clips its content, so a margin around the page viewport would never reach offscreen cards.
  const root = useThreadViewport((state) => state.element.viewport);
  const [near, setNear] = useState(typeof IntersectionObserver === "undefined");
  useEffect(() => {
    if (typeof IntersectionObserver === "undefined" || !ref.current) {
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => setNear(entries.some((entry) => entry.isIntersecting)),
      { root, rootMargin: "100% 0px" }
    );
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, [root]);
  return [ref, near] as const;
};

/** A card that stops loading keeps the height its readings had, so the conversation never moves under the reader. */
const ArtifactBody = ({
  definition,
  live,
}: {
  definition: unknown;
  live: boolean;
}) => {
  const ref = useRef<HTMLDivElement>(null);
  const [height, setHeight] = useState<number>();
  useEffect(() => {
    const body = ref.current;
    if (!live || !body || typeof ResizeObserver === "undefined") {
      return;
    }
    // A hidden conversation measures zero; keep the last visible height.
    const observer = new ResizeObserver(() => {
      if (body.offsetHeight > 0) {
        setHeight(body.offsetHeight);
      }
    });
    observer.observe(body);
    return () => observer.disconnect();
  }, [live]);
  return (
    <div
      ref={ref}
      data-slot="artifact-body"
      className="mt-3 min-w-0 overflow-x-auto"
      aria-live="off"
      style={live ? undefined : { minHeight: height }}
    >
      <ViewRenderer definition={definition} active={live} headingLevel={4} />
    </div>
  );
};

const ArtifactPart = ({ data }: DataMessagePartProps<PresentedArtifact>) => {
  const { select, openId, visible } = useContext(ArtifactContext);
  const [ref, near] = useNearScreen();
  if (data.artifact.kind === "hearth.automation") {
    return (
      <div ref={ref}>
        <AutomationArtifactPreview
          artifact={data.artifact}
          open={openId === data.artifact.artifact_id}
          active={visible && near}
          onSelect={() => select(data)}
        />
      </div>
    );
  }
  if (data.artifact.kind === "unsupported") {
    return (
      <div ref={ref}>
        <StatusNotice>
          <p>
            This artifact cannot be shown in this Hearth version. The original
            tool result is retained.
          </p>
          <details>
            <summary>Original artifact</summary>
            <pre>{JSON.stringify(data.artifact.original, null, 2)}</pre>
          </details>
        </StatusNotice>
      </div>
    );
  }
  const view = readDefinition(data.artifact.definition);
  const title = titleFor(view);
  let summary = "Needs a newer Hearth version";
  if (view.status === "partial") {
    summary = "View · Some content needs a newer Hearth version";
  }
  if (view.status === "supported") {
    const elements = Object.values(view.definition.spec.elements);
    const readings = elements.filter(
      (element) => element.type === "Metric"
    ).length;
    const charts = elements.filter(
      (element) => element.type === "TimeSeries"
    ).length;
    summary = [
      "View",
      readings ? plural(readings, "reading") : "",
      charts ? plural(charts, "chart") : "",
    ]
      .filter(Boolean)
      .join(" · ");
  }
  // The canvas already shows the open view, so its card stays compact.
  const open = openId === data.artifact.artifact_id;
  // An inline view's cards are the frame; a narrow column keeps "Open" on the title row and names the action in full.
  return (
    <Card
      ref={ref}
      data-slot="artifact-preview"
      data-kind="view"
      data-open={open || undefined}
      className={cn(
        artifactFrame,
        open
          ? "border-violet/45"
          : "mt-3 rounded-none border-0 bg-transparent p-0"
      )}
    >
      <div className={artifactHeader}>
        <span className={artifactIcon}>
          <PanelsTopLeftIcon className="size-5" aria-hidden="true" />
        </span>
        <div>
          <h3 className={artifactTitle}>{title}</h3>
          <p className={artifactSummary}>{summary}</p>
        </div>
        {open ? (
          <Button
            variant="ghost"
            size="touch"
            className={artifactAction(true, true)}
            onClick={() => select(data)}
          >
            <CheckIcon className="size-4 shrink-0" aria-hidden="true" />
            Showing in canvas
          </Button>
        ) : (
          <Button
            variant="outline"
            size="touch"
            className={artifactAction(false, true)}
            onClick={() => select(data)}
            aria-label="Open in canvas"
          >
            <span>
              Open
              <span
                data-slot="artifact-open-detail"
                className="@max-[440px]:hidden"
              >
                {" "}
                in canvas
              </span>
            </span>
            <ArrowRightIcon className="size-4 shrink-0" aria-hidden="true" />
          </Button>
        )}
      </div>
      {!open && (
        <ArtifactBody
          definition={data.artifact.definition}
          live={visible && near}
        />
      )}
    </Card>
  );
};

/** Hold a presentation's place while the model writes it and Core checks it. */
const PendingArtifactPart = ({
  data,
}: DataMessagePartProps<{ kind?: string } | null>) => (
  <Card asChild className={artifactFrame}>
    <output className="block">
      <div className={artifactHeader}>
        <span className={artifactIcon}>
          <PanelsTopLeftIcon className="size-5" aria-hidden="true" />
        </span>
        <div>
          <h3 className={artifactTitle}>
            {data?.kind === "automation"
              ? "Preparing an Automation"
              : "Preparing a view"}
          </h3>
          <p className={artifactSummary}>Putting it together…</p>
        </div>
      </div>
      <div
        className="border-border mt-4 grid min-w-0 grid-cols-2 gap-3 overflow-x-auto border-t pt-4"
        aria-hidden="true"
      >
        <Card asChild className="animate-pending h-[157px]">
          <span />
        </Card>
        <Card
          asChild
          className="animate-pending h-[157px] [animation-delay:.2s]"
        >
          <span />
        </Card>
      </div>
    </output>
  </Card>
);

const ContextPart = ({ data }: DataMessagePartProps<unknown>) => {
  const automation = automationContextSchema.safeParse(data);
  const parsed = viewContextSchema.safeParse(data);
  const view = parsed.success ? readDefinition(parsed.data.definition) : null;
  const badge =
    "mt-1.5 flex items-start gap-2 text-label leading-normal whitespace-normal text-violet-text [overflow-wrap:anywhere]";
  const icon = "mt-[2.5px] size-4 shrink-0";
  if (automation.success) {
    return (
      <p className={badge}>
        <PanelsTopLeftIcon className={icon} aria-hidden="true" />
        Selected Automation:{" "}
        {
          readAutomationDocument(automation.data.definition_json).definition
            .name
        }
      </p>
    );
  }
  return (
    <p className={badge}>
      <PanelsTopLeftIcon className={icon} aria-hidden="true" />
      {view?.status === "supported"
        ? `Selected view: ${view.definition.title}`
        : "Selected view context unavailable in this version"}
    </p>
  );
};

// Reply metadata rows share one icon column: status icon at the reply's edge, text after it, disclosure last.
const activitySummary =
  "flex min-h-7 w-fit max-w-full cursor-pointer list-none items-center gap-2 rounded-md hover:text-foreground pointer-coarse:min-h-11 [&::-webkit-details-marker]:hidden";
const activityIcon = "size-4 shrink-0";
const ToolGroup = ({
  count,
  pending,
  children,
}: {
  count: number;
  pending: number;
  children: ReactNode;
}) => (
  <details className="group/tools text-label text-muted-foreground leading-normal">
    <summary className={activitySummary}>
      {pending ? (
        <CircleDashedIcon className={activityIcon} aria-hidden="true" />
      ) : (
        <CheckIcon className={activityIcon} aria-hidden="true" />
      )}
      <span>
        Used {plural(count, "tool")}
        {pending ? ` · ${pending} pending or unknown` : ""}
      </span>
      <ChevronRightIcon
        className="-ms-0.5 size-3.5 shrink-0 transition-transform duration-150 ease-out group-open/tools:rotate-90"
        aria-hidden="true"
      />
    </summary>
    <ul className="border-border ms-[7.5px] mt-1 list-none border-s ps-[15.5px]">
      {children}
    </ul>
  </details>
);

const ToolPart = ({ toolName, argsText, result }: ToolCallMessagePartProps) => (
  <li>
    <details className="group/tool">
      <summary className={activitySummary}>
        {result === undefined ? (
          <CircleDashedIcon className={activityIcon} aria-hidden="true" />
        ) : (
          <CheckIcon className={activityIcon} aria-hidden="true" />
        )}
        <span className="min-w-0 [overflow-wrap:anywhere]">
          {toolName}{" "}
          {result === undefined ? "· outcome unknown or pending" : "· complete"}
        </span>
        <ChevronRightIcon
          className="-ms-0.5 size-3.5 shrink-0 transition-transform duration-150 ease-out group-open/tool:rotate-90"
          aria-hidden="true"
        />
      </summary>
      <p className="ms-6 mt-1 mb-2 [overflow-wrap:anywhere]">{argsText}</p>
      {result !== undefined && (
        <pre className="border-border bg-card text-label ms-6 mt-1 mb-2 max-h-[180px] overflow-auto rounded-md border px-3 py-2 [overflow-wrap:anywhere] whitespace-pre-wrap">
          {String(result)}
        </pre>
      )}
    </details>
  </li>
);

const userParts = { data: { by_name: { "hearth-context": ContextPart } } };
const UserMessage = () => (
  <div className="rounded-card border-gray-3 bg-gray-2 text-body text-foreground ms-auto mt-8 w-fit max-w-[min(85%,560px)] rounded-br-xs border px-4 py-2.5 leading-[1.6] [overflow-wrap:break-word] whitespace-pre-wrap first:mt-4">
    <MessagePrimitive.Parts components={userParts} />
  </div>
);

const CopyResponse = () => {
  const copied = useAuiState((state) => state.message.isCopied);
  return (
    <ActionBarPrimitive.Copy asChild>
      <Button
        variant="ghost"
        size="touch"
        className="data-[copied]:text-success px-2"
        aria-label={copied ? "Response copied" : "Copy response"}
        title={copied ? "Response copied" : "Copy response"}
      >
        {copied ? (
          <>
            <CheckIcon aria-hidden="true" />
            Copied
          </>
        ) : (
          <>
            <CopyIcon aria-hidden="true" />
            Copy
          </>
        )}
      </Button>
    </ActionBarPrimitive.Copy>
  );
};

const AssistantMessage = () => (
  <div className="text-body text-gray-9 mt-6 flex flex-col gap-3 leading-[1.7] [overflow-wrap:break-word]">
    <div className="text-label text-foreground mb-[-4px] flex min-h-6 items-center gap-2 leading-normal font-semibold">
      <HouseIcon className="text-violet size-4 shrink-0" aria-hidden="true" />
      Hearth
    </div>
    <MessagePrimitive.GroupedParts groupBy={toolGroups} indicator="never">
      {({ part, children }) => {
        switch (part.type) {
          case "group-tools": {
            return (
              <ToolGroup
                count={part.indices.length}
                pending={part.indices.length - part.counts.complete}
              >
                {children}
              </ToolGroup>
            );
          }
          case "tool-call": {
            return <ToolPart {...part} />;
          }
          case "text": {
            return (
              <MarkdownTextPrimitive
                containerProps={markdownContainerProps}
                remarkPlugins={markdownPlugins}
                smooth={false}
                skipHtml
              />
            );
          }
          case "data": {
            if (part.name === "hearth-view") {
              return <ArtifactPart {...part} />;
            }
            return part.name === "hearth-view-pending" ? (
              <PendingArtifactPart {...part} />
            ) : null;
          }
          default: {
            return null;
          }
        }
      }}
    </MessagePrimitive.GroupedParts>
    <AuiIf
      condition={(s) =>
        s.message.parts.some(
          (part) => part.type === "text" && !!part.text.trim()
        )
      }
    >
      <ActionBarPrimitive.Root className="-ms-2 flex" hideWhenRunning>
        <CopyResponse />
      </ActionBarPrimitive.Root>
    </AuiIf>
  </div>
);

export const ConversationMessages = ({
  select,
  openId,
  visible,
}: ArtifactDisplay) => {
  const value = useMemo(
    () => ({ select, openId, visible }),
    [openId, select, visible]
  );
  return (
    <ArtifactContext.Provider value={value}>
      <ThreadPrimitive.Messages
        components={{ UserMessage, AssistantMessage }}
      />
    </ArtifactContext.Provider>
  );
};
