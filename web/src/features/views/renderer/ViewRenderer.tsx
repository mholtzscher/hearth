import { defineRegistry, JSONUIProvider, Renderer } from "@json-render/react";
import { catalog, type ViewElement } from "../catalog";
import { readDefinition, type ReadView } from "../catalog/compatibility";
import { BoundMetric } from "../cards/metric/Metric";
import { BoundTimeSeries } from "../cards/time-series/TimeSeries";

const { registry } = defineRegistry(catalog, { components: {
  Stack: ({ props, children }) => <div className={`view-stack gap-${props.gap}`}>{children}</div>,
  Grid: ({ props, children }) => <div className="view-grid" style={{ gridTemplateColumns: `repeat(${props.columns}, minmax(0, 1fr))` }}>{children}</div>,
  Heading: ({ props }) => <h2 className="view-heading">{props.text}</h2>,
  Note: ({ props }) => <p className="view-note">{props.text}</p>,
  Metric: ({ props }) => <BoundMetric {...props} />,
  TimeSeries: ({ props }) => <BoundTimeSeries {...props} />,
} });

function SupportedNode({ node, children }: { node: ViewElement; children?: React.ReactNode }) {
  if (node.type === "Stack") return <div className={`view-stack gap-${node.props.gap}`}>{children}</div>;
  if (node.type === "Grid") return <div className="view-grid" style={{ gridTemplateColumns: `repeat(${node.props.columns}, minmax(0, 1fr))` }}>{children}</div>;
  // Leaf projection enters json-render only after strict node validation.
  return <Renderer spec={{ root: "node", elements: { node: { ...node, children: [] } } }} registry={registry} />;
}
function PartialView({ view, id }: { view: Extract<ReadView, { status: "partial" }>; id: string }) {
  const node = view.elements[id];
  if (!node) return <div className="view-unsupported">Unsupported component · original content preserved</div>;
  return <SupportedNode node={node}>{view.children[id].map(child => <PartialView key={child} view={view} id={child} />)}</SupportedNode>;
}
export function DownloadDefinition({ raw }: { raw: unknown }) {
  return <button className="view-text-button" onClick={() => {
    const url = URL.createObjectURL(new Blob([JSON.stringify(raw, null, 2)], { type: "application/json" }));
    const link = document.createElement("a"); link.href = url; link.download = "hearth-view.json"; link.click(); URL.revokeObjectURL(url);
  }}>Download original definition</button>;
}
export function ViewRenderer({ definition, active = true }: { definition: unknown; active?: boolean }) {
  const view = readDefinition(definition);
  if (view.status === "unavailable") return <div className="view-unsupported"><p>{view.reason}</p><DownloadDefinition raw={definition} /></div>;
  if (!active) return <div className="view-summary">{view.status === "supported" ? view.definition.title : view.title}<span>Open view to load live readings</span></div>;
  return <JSONUIProvider registry={registry}>
    {view.status === "partial" ? <><div className="view-unsupported">Some content requires a newer Hearth version. Editing is disabled. <DownloadDefinition raw={definition} /></div><PartialView view={view} id={view.root} /></> :
      <Renderer spec={{ root: view.definition.spec.root, elements: Object.fromEntries(Object.entries(view.definition.spec.elements).map(([key, node]) => [key, { ...node, children: "children" in node ? node.children : [] }])) }} registry={registry} />}
  </JSONUIProvider>;
}
