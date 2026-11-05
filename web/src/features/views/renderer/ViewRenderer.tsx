import { Component, type ReactNode } from "react";
import type { ViewElement } from "../catalog";
import { readDefinition } from "../catalog/compatibility";
import { BoundMetric } from "../cards/metric/Metric";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";

/** A validated element graph. A null element is preserved content this version cannot render. */
type Graph = { root: string; elements: Record<string, ViewElement | null>; children: Record<string, string[]> };

/**
 * Keeps one failing card from blanking the whole view, and says so rather than
 * leaving a silent gap. A changed definition gets a fresh attempt.
 */
class CardBoundary extends Component<{ resetKey: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidUpdate(previous: { resetKey: string }) {
    if (this.state.failed && previous.resetKey !== this.props.resetKey) this.setState({ failed: false });
  }
  render() {
    return this.state.failed ? <div className="view-unsupported" role="status">This card could not be shown.</div> : this.props.children;
  }
}

function ViewNode({ graph, id }: { graph: Graph; id: string }) {
  const node = graph.elements[id];
  // Unsupported content stays unrendered with its descendants, so none of them start reads.
  if (!node) return <div className="view-unsupported">Unsupported component · original content preserved</div>;
  const children = graph.children[id].map(child => <ViewNode key={child} graph={graph} id={child} />);
  switch (node.type) {
    case "Stack": return <div className={`view-stack gap-${node.props.gap}`}>{children}</div>;
    case "Grid": return <div className="view-grid" style={{ gridTemplateColumns: `repeat(${node.props.columns}, minmax(0, 1fr))` }}>{children}</div>;
    case "Heading": return <h2 className="view-heading">{node.props.text}</h2>;
    case "Note": return <p className="view-note">{node.props.text}</p>;
    case "Metric": return <CardBoundary resetKey={JSON.stringify(node.props)}><BoundMetric {...node.props} /></CardBoundary>;
    case "TimeSeries": return <CardBoundary resetKey={JSON.stringify(node.props)}><BoundTimeSeries {...node.props} /></CardBoundary>;
  }
}

export function DownloadDefinition({ raw }: { raw: unknown }) {
  return <button className="view-text-button" onClick={() => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(raw, null, 2)], { type: "application/json" }));
    const link = document.createElement("a"); link.href = url; link.download = "hearth-view.json"; link.click(); URL.revokeObjectURL(url);
  }}>Download original definition</button>;
}
/** Explains the afterglow line once per view, so its colors are never the only explanation of a reading's age. */
function AfterglowKey() {
  return <p className="view-afterglow-key"><span aria-hidden="true" />Line above a reading: orange when just observed, cooling to violet over an hour; no line when unavailable or its availability is unknown.</p>;
}
export function ViewRenderer({ definition, active = true }: { definition: unknown; active?: boolean }) {
  const view = readDefinition(definition);
  if (view.status === "unavailable") return <div className="view-unsupported"><p>{view.reason}</p><DownloadDefinition raw={definition} /></div>;
  if (!active) return <div className="view-summary">{view.status === "supported" ? view.definition.title : view.title}<span>Open view to load live readings</span></div>;
  const graph: Graph = view.status === "partial" ? view : {
    root: view.definition.spec.root,
    elements: view.definition.spec.elements,
    children: Object.fromEntries(Object.entries(view.definition.spec.elements).map(([key, node]) => [key, "children" in node ? node.children : []])),
  };
  const hasMetric = Object.values(graph.elements).some(node => node?.type === "Metric");
  return <>
    {view.status === "partial" && <div className="view-unsupported">Some content requires a newer Hearth version. Editing is disabled. <DownloadDefinition raw={definition} /></div>}
    <ViewNode graph={graph} id={graph.root} />
    {hasMetric && <AfterglowKey />}
  </>;
}
