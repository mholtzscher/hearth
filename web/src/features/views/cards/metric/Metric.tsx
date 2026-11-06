import { useNumericReads } from "../../bindings/registry";
import type { NumericSource, Resolved, MetricData, SeriesData, SeriesWindow } from "../../bindings/types";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";
import { RangeTrack } from "./RangeTrack";
import { Trend, trendMethod } from "./Trend";

export function Metric({ label, resource, trend, range = false }: {
  label?: string;
  resource: Resolved<MetricData>;
  /** A history line under the reading over this window. */
  trend?: { window: SeriesWindow; resource: Resolved<SeriesData> };
  /** Show the reading against its field's declared range, when it has one. */
  range?: boolean;
}) {
  const status = cardStatus(resource);
  const data = status.value === "empty" ? undefined : resource.data;
  const title = label ?? resource.metadata?.label ?? "Reading";
  const dimmed = status.value === "dimmed";
  return <CardFrame className="view-metric" title={title} status={status.entries} observedAt={data?.observed_at}
    arrival={data ? { updatedAt: data.observed_at, shown: `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`, eligible: status.glows } : undefined}
    sources={resource.metadata ? [{ entityId: resource.metadata.id, label: resource.metadata.label, availability: resource.metadata.availability, enabled: resource.metadata.enabled }] : []}
    sourceExtra={trend ? trendMethod(trend.window) : undefined}>
    {/* An unavailable or disabled Entity keeps its last value at the reading scale, dimmed, so the status leads. */}
    <p className="view-metric-value" data-value={status.value}>
      {data ? data.value.toFixed(data.format.fraction_digits) : "—"}{data?.format.unit_label && <small>{data.format.unit_label}</small>}
    </p>
    {range && data?.range && <RangeTrack data={{ ...data, range: data.range }} dimmed={dimmed} />}
    {range && data && !data.range && <p className="view-range-note">Range unavailable.</p>}
    {trend && status.value !== "empty" && <Trend window={trend.window} resource={trend.resource} dimmed={dimmed} />}
  </CardFrame>;
}
export function BoundMetric({ source, label, trend, range }: { source: NumericSource; label?: string; trend?: SeriesWindow; range?: true }) {
  const [current, history] = useNumericReads(trend ? [{ source }, { source, window: trend }] : [{ source }]);
  const data = current.data && "value" in current.data ? current.data : undefined;
  const series = history?.data && "buckets" in history.data ? history.data : undefined;
  return <Metric label={label} resource={{ ...current, data }} range={range}
    trend={trend && history ? { window: trend, resource: { ...history, data: series } } : undefined} />;
}
