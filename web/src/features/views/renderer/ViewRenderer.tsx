import { Component, useContext, type CSSProperties, type ReactNode } from "react";
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
import { BoundPowerTile } from "../cards/power-tile/PowerTile";
import { BoundSwitchList } from "../cards/switch-list/SwitchList";

/** A validated element graph. A null element is preserved content this version cannot render. */
type Graph = { root: string; elements: Record<string, ViewElement | null>; children: Record<string, string[]> };

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
    return <CardFrame className="view-card-failed" title={this.props.title} status={[{ key: "error", tone: "error", text: "This card couldn't be shown" }]} sources={[]}>
      <p className="view-card-recovery">Edit the view or ask Hearth to change it.</p>
    </CardFrame>;
  }
}

function ViewNode({ graph, id }: { graph: Graph; id: string }) {
  const node = graph.elements[id];
  const Heading = `h${useContext(HeadingLevel)}` as "h3";
  // Unsupported content stays unrendered with its descendants, so none of them start reads.
  if (!node) return <div className="view-unsupported">Unsupported component · original content preserved</div>;
  const children = graph.children[id].map(child => <ViewNode key={child} graph={graph} id={child} />);
  switch (node.type) {
    case "Stack": return <div className={`view-stack gap-${node.props.gap}`}>{children}</div>;
    case "Grid": return <div className={`view-grid${graph.children[id].every(child => graph.elements[child]?.type === "PowerTile") ? " view-grid-power" : ""}`} style={{ "--view-columns": node.props.columns } as CSSProperties}>{children}</div>;
    case "Heading": return <Heading className="view-heading">{node.props.text}</Heading>;
    case "Note": return <p className="view-note">{node.props.text}</p>;
    case "Metric": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Reading"}><BoundMetric {...node.props} /></CardBoundary>;
    case "TimeSeries": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundTimeSeries {...node.props} /></CardBoundary>;
    case "State": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "State"}><BoundState {...node.props} /></CardBoundary>;
    case "ReadingList": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundReadingList {...node.props} /></CardBoundary>;
    case "Device": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Device"}><BoundDevice {...node.props} /></CardBoundary>;
    case "StateTimeline": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundStateTimeline {...node.props} /></CardBoundary>;
    case "WeekPattern": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "State"}><BoundWeekPattern {...node.props} /></CardBoundary>;
    case "PowerTile": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.label ?? "Power"}><BoundPowerTile {...node.props} /></CardBoundary>;
    case "SwitchList": return <CardBoundary resetKey={JSON.stringify(node.props)} title={node.props.title}><BoundSwitchList {...node.props} /></CardBoundary>;
  }
}

export function DownloadDefinition({ raw }: { raw: unknown }) {
  return <button className="view-text-button" onClick={() => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(raw, null, 2)], { type: "application/json" }));
    const link = document.createElement("a"); link.href = url; link.download = "hearth-view.json"; link.click(); URL.revokeObjectURL(url);
  }}>Download original definition</button>;
}
/** headingLevel is the level of the view's own Heading elements; the host's title sits one level above. Card titles use the next level. */
export function ViewRenderer({ definition, active = true, headingLevel }: { definition: unknown; active?: boolean; headingLevel: 3 | 4 }) {
  const view = readDefinition(definition);
  if (view.status === "unavailable") return <div className="view-unsupported"><p>{view.reason}</p><DownloadDefinition raw={definition} /></div>;
  if (!active) return <div className="view-summary">{view.status === "supported" ? view.definition.title : view.title}<span>Open view to load live readings</span></div>;
  const graph: Graph = view.status === "partial" ? view : {
    root: view.definition.spec.root,
    elements: view.definition.spec.elements,
    children: Object.fromEntries(Object.entries(view.definition.spec.elements).map(([key, node]) => [key, "children" in node ? node.children : []])),
  };
  return <HeadingLevel.Provider value={headingLevel}>
    {view.status === "partial" && <div className="view-unsupported">Some content requires a newer Hearth version. Editing is disabled. <DownloadDefinition raw={definition} /></div>}
    <ViewNode graph={graph} id={graph.root} />
  </HeadingLevel.Provider>;
}
