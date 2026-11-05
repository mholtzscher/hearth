import type { CSSProperties } from "react";
import { useNumericRead } from "../../bindings/registry";
import type { NumericSource, Resolved, MetricData } from "../../bindings/types";
import { ResourceStatus } from "../resource-status";
import { afterglow, observationAge, useMinuteClock } from "./afterglow";

export function Metric({ label, resource }: { label?: string; resource: Resolved<MetricData> }) {
  const data = resource.data;
  const now = useMinuteClock();
  // Only a reading from an available Entity glows; unavailable or unknown availability stays dark.
  const glow = data && resource.metadata?.availability === "available" ? afterglow(data.observed_at, now) : undefined;
  const age = data && observationAge(data.observed_at, now);
  return <section className="view-metric" aria-label={label ?? resource.metadata?.label ?? "Reading"}
    data-afterglow={glow === undefined ? undefined : ""} style={glow === undefined ? undefined : { "--afterglow": glow } as CSSProperties}>
    <div className="view-eyebrow">{label ?? resource.metadata?.label ?? "Reading"}</div>
    <div className="view-metric-value">{data ? data.value.toFixed(data.format.fraction_digits) : "—"}<small>{data?.format.unit_label}</small></div>
    <ResourceStatus resource={resource} />
    {data && <time dateTime={data.observed_at}>Observed {new Date(data.observed_at).toLocaleTimeString()}{age && ` · ${age}`}</time>}
    {resource.metadata && <details><summary>Source</summary><p>{resource.metadata.label}</p><code>{resource.metadata.id}</code></details>}
  </section>;
}
export function BoundMetric({ source, label }: { source: NumericSource; label?: string }) {
  const result = useNumericRead(source);
  const data = result.data && "value" in result.data ? result.data : undefined;
  return <Metric label={label} resource={{ ...result, data }} />;
}
