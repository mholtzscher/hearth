import { useNumericRead } from "../../bindings/registry";
import type { NumericSource, Resolved, MetricData } from "../../bindings/types";
import { ResourceStatus } from "../resource-status";

export function Metric({ label, resource }: { label?: string; resource: Resolved<MetricData> }) {
  const data = resource.data;
  return <section className="view-metric" aria-label={label ?? resource.metadata?.label ?? "Reading"}>
    <div className="view-eyebrow">{label ?? resource.metadata?.label ?? "Reading"}</div>
    <div className="view-metric-value">{data ? data.value.toFixed(data.format.fraction_digits) : "—"}<small>{data?.format.unit_label}</small></div>
    <ResourceStatus resource={resource} />
    {data && <time dateTime={data.observed_at}>Observed {new Date(data.observed_at).toLocaleTimeString()}</time>}
    {resource.metadata && <details><summary>Source</summary><p>{resource.metadata.label}</p><code>{resource.metadata.id}</code></details>}
  </section>;
}
export function BoundMetric({ source, label, display }: { source: NumericSource; label?: string; display: { unit: string } }) {
  const result = useNumericRead(source, display.unit);
  const data = result.data && "value" in result.data ? result.data : undefined;
  return <Metric label={label} resource={{ ...result, data }} />;
}
