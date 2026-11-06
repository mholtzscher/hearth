import { useNumericRead } from "../../bindings/registry";
import type { NumericSource, Resolved, MetricData } from "../../bindings/types";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";

export function Metric({ label, resource }: { label?: string; resource: Resolved<MetricData> }) {
  const status = cardStatus(resource);
  const data = status.value === "empty" ? undefined : resource.data;
  const title = label ?? resource.metadata?.label ?? "Reading";
  return <CardFrame className="view-metric" title={title} status={status.entries} observedAt={data?.observed_at} glows={status.glows}
    sources={resource.metadata ? [{ entityId: resource.metadata.id, label: resource.metadata.label }] : []}>
    {/* An unavailable or disabled Entity keeps its last value at the reading scale, dimmed, so the status leads. */}
    <p className="view-metric-value" data-value={status.value}>
      {data ? data.value.toFixed(data.format.fraction_digits) : "—"}{data?.format.unit_label && <small>{data.format.unit_label}</small>}
    </p>
  </CardFrame>;
}
export function BoundMetric({ source, label }: { source: NumericSource; label?: string }) {
  const result = useNumericRead(source);
  const data = result.data && "value" in result.data ? result.data : undefined;
  return <Metric label={label} resource={{ ...result, data }} />;
}
