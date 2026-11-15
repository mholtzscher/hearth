import { Component, useContext, type CSSProperties, type ReactNode } from "react";
import { Button } from "../../../components/button";
import type { ViewElement } from "../catalog";
import { readDefinition } from "../catalog/compatibility";
import { CardFrame, HeadingLevel } from "../cards/CardFrame";
import { BoundMetric } from "../cards/metric/Metric";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";
import { BoundState } from "../cards/state/State";
import { BoundReadingList } from "../cards/reading-list/ReadingList";
import { BoundDevice } from "../cards/device/Device";
import { BoundStateTimeline } from "../cards/state-timeline/StateTimeline";
import { BoundWeekPattern } from "../cards/week-pattern/WeekPattern";
import { BoundToggleTile } from "../cards/toggle-tile/ToggleTile";
import { BoundToggleList } from "../cards/toggle-list/ToggleList";
import { BoundAirPurifier } from "../cards/air-purifier/AirPurifier";
import { BoundCover } from "../cards/cover/Cover";
import { BoundOutsideNow } from "../cards/outside-now/OutsideNow";
import { BoundRain } from "../cards/rain/Rain";
import { BoundWind } from "../cards/wind/Wind";
import { BoundPressure } from "../cards/pressure/Pressure";
import { BoundForecast } from "../cards/forecast/Forecast";

/** A validated element graph. A null element is preserved content this version cannot render. */
type Graph = { root: string; elements: Record<string, ViewElement | null>; children: Record<string, string[]> };

/**
 * Quick switches and single readings fit narrower cells than other cards, so
 * grids made only of them keep more columns, including on phones.
 */
function gridKind(graph: Graph, id: string) {
  const elements = graph.children[id].map(child => graph.elements[child]);
  const types = elements.map(element => element?.type);
  const isTile = (element: ViewElement | null | undefined) => element?.type === "ToggleTile" ||
    ((element?.type === "AirPurifier" || element?.type === "Cover") && element.props.layout === "tile");
  if (elements.every(isTile)) return "toggle";
  if (types.every(type => type === "Metric" || type === "State")) return "reading";
  return undefined;
}

const unsupportedStyles = "rounded-card border border-dashed border-gray-4 px-4 py-3.5 text-label leading-normal text-muted-foreground";

/**
 * Keeps one failing card from blanking the whole view, and says so rather than
 * leaving a silent gap. A changed definition gets a fresh attempt.
 */
class CardBoundary extends Component<{ resetKey: string; title: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidUpdate(previous: { resetKey: string }) {
    if (this.state.failed && previous.resetKey !== this.props.resetKey) this.setState({ failed: false });
  }
  render() {
    if (!this.state.failed) return this.props.children;
    return <CardFrame title={this.props.title} status={[{ key: "error", tone: "error", text: "This card couldn't be shown" }]} sources={[]}>
      <p className="text-label leading-normal text-muted-foreground">Edit the view or ask Hearth to change it.</p>
    </CardFrame>;
  }
}

function ViewNode({ graph, id, followsHeading = false }: { graph: Graph; id: string; followsHeading?: boolean }) {
  const node = graph.elements[id];
  const Heading = `h${useContext(HeadingLevel)}` as "h3";
  // Unsupported content stays unrendered with its descendants, so none of them start reads.
  if (!node) return <div className={unsupportedStyles}>Unsupported component · original content preserved</div>;
  const children = graph.children[id].map((child, index, siblings) => <ViewNode key={child} graph={graph} id={child} followsHeading={node.type === "Stack" && index > 0 && graph.elements[siblings[index - 1]]?.type === "Heading"} />);
  switch (node.type) {
    case "Stack": return <div className={`grid min-w-0 gap-(--stack-gap) ${node.props.gap === "small" ? "[--stack-gap:8px]" : node.props.gap === "large" ? "[--stack-gap:24px]" : "[--stack-gap:16px]"}`}>{children}</div>;
    case "Grid": {
      const kind = gridKind(graph, id);
      return <div data-slot="view-grid" data-grid-kind={kind} className={`grid min-w-0 grid-cols-[repeat(auto-fit,minmax(min(100%,max(var(--view-min-card,248px),calc((100%_-_(var(--view-columns)_-_1)_*_12px)_/_var(--view-columns)))),1fr))] gap-3 ${kind ? "[--view-min-card:160px]" : "max-tablet:grid-cols-1"}`} style={{ "--view-columns": node.props.columns } as CSSProperties}>{children}</div>;
    }
    case "Heading": return <Heading className="text-title leading-[1.4] font-semibold text-balance">{node.props.text}</Heading>;
    case "Note": return <p className={`max-w-[70ch] text-body leading-[1.7] text-muted-foreground ${followsHeading ? "mt-[calc(4px-var(--stack-gap))]" : ""}`}>{node.props.text}</p>;
    case "Metric": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Reading"}><BoundMetric {...node.props} /></CardBoundary>;
    case "TimeSeries": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundTimeSeries {...node.props} /></CardBoundary>;
    case "State": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "State"}><BoundState {...node.props} /></CardBoundary>;
    case "ReadingList": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundReadingList {...node.props} /></CardBoundary>;
    case "Device": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Device"}><BoundDevice {...node.props} /></CardBoundary>;
    case "StateTimeline": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundStateTimeline {...node.props} /></CardBoundary>;
    case "WeekPattern": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "State"}><BoundWeekPattern {...node.props} /></CardBoundary>;
    case "ToggleTile": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Power"}><BoundToggleTile {...node.props} /></CardBoundary>;
    case "ToggleList": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundToggleList {...node.props} /></CardBoundary>;
    case "AirPurifier": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Air purifier"}><BoundAirPurifier {...node.props} /></CardBoundary>;
    case "Cover": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Cover"}><BoundCover {...node.props} /></CardBoundary>;
    case "OutsideNow": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Outside"}><BoundOutsideNow {...node.props} /></CardBoundary>;
    case "Rain": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Rain"}><BoundRain {...node.props} /></CardBoundary>;
    case "Wind": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Wind"}><BoundWind {...node.props} /></CardBoundary>;
    case "Pressure": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Pressure"}><BoundPressure {...node.props} /></CardBoundary>;
    case "Forecast": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Forecast"}><BoundForecast {...node.props} /></CardBoundary>;
  }
}

export function DownloadDefinition({ raw }: { raw: unknown }) {
  return <Button variant="link" size="default" className="p-0 underline" onClick={() => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(raw, null, 2)], { type: "application/json" }));
    const link = document.createElement("a"); link.href = url; link.download = "hearth-view.json"; link.click(); URL.revokeObjectURL(url);
  }}>Download original definition</Button>;
}
/** headingLevel is the level of the view's own Heading elements; the host's title sits one level above. Card titles use the next level. */
export function ViewRenderer({ definition, active = true, headingLevel }: { definition: unknown; active?: boolean; headingLevel: 3 | 4 }) {
  const view = readDefinition(definition);
  if (view.status === "unavailable") return <div className={unsupportedStyles}><p>{view.reason}</p><DownloadDefinition raw={definition} /></div>;
  if (!active) return <div className="text-foreground">{view.status === "supported" ? view.definition.title : view.title}<span className="block text-label leading-normal text-muted-foreground">Open view to load live readings</span></div>;
  const graph: Graph = view.status === "partial" ? view : {
    root: view.definition.spec.root,
    elements: view.definition.spec.elements,
    children: Object.fromEntries(Object.entries(view.definition.spec.elements).map(([key, node]) => [key, "children" in node ? node.children : []])),
  };
  return <HeadingLevel.Provider value={headingLevel}>
    {view.status === "partial" && <div className={unsupportedStyles}>Some content requires a newer Hearth version. Editing is disabled. <DownloadDefinition raw={definition} /></div>}
    <ViewNode graph={graph} id={graph.root} />
  </HeadingLevel.Provider>;
}
