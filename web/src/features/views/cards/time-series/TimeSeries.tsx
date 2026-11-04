import { Area, CartesianGrid, ComposedChart, Line, Tooltip, XAxis, YAxis } from "recharts";
import { useEffect, useState } from "react";
import { ChartContainer } from "../../../../components/ui/chart";
import { useNumericReads } from "../../bindings/registry";
import type { NumericSource, Resolved, SeriesData, SeriesWindow } from "../../bindings/types";
import { ResourceStatus } from "../resource-status";

const colors = ["#e0b17e", "#82c9ba", "#99b8ec", "#cf9cdb", "#c9cb83", "#ee9f9f", "#8cc9d7", "#b2aca3"];
export function TimeSeries({ title, series }: { title: string; series: Array<{ label?: string; resource: Resolved<SeriesData> }> }) {
  const latest = series.find(item => item.resource.data)?.resource.data;
  const aligned = series.every(item => !item.resource.data || !latest || (item.resource.data.from === latest.from && item.resource.data.to === latest.to));
  const [retained, setRetained] = useState(() => aligned ? series : []);
  useEffect(() => { if (aligned && latest) setRetained(series); }, [aligned, latest, series]);
  // Keep one coherent interval while sibling reads settle or fail. Status still
  // comes from the latest reads, so a retained chart never hides refresh failure.
  const displayed = aligned ? series : series.map((item, i) => ({ ...item, resource: { ...item.resource, data: retained[i]?.resource.data } }));
  const first = displayed.find(item => item.resource.data)?.resource.data;
  const points = first?.buckets.map((bucket, index) => {
    const point: Record<string, number | string | number[] | null> = { time: new Date(bucket.start).getTime(), end: bucket.end };
    displayed.forEach((item, i) => {
      const b = item.resource.data?.buckets[index];
      point[`mean${i}`] = b?.mean ?? null;
      point[`band${i}`] = b && b.min !== null && b.max !== null ? [b.min, b.max] : null;
    });
    return point;
  });
  const bucketSeconds = first ? (Date.parse(first.to) - Date.parse(first.from)) / 288000 : null;
  const bucketLabel = bucketSeconds === null ? "—" : bucketSeconds >= 120 ? `${bucketSeconds / 60} minute` : `${bucketSeconds} second`;
  return <section className="view-chart">
    <header><h3>{title}</h3><span>{first?.format.unit_label}</span></header>
    <div className="view-chart-legend">{series.map((item, index) => <div key={index}>
      <span style={{ color: colors[index % colors.length] }}>●</span> {item.label ?? item.resource.metadata?.label ?? `Series ${index + 1}`}
      <ResourceStatus resource={item.resource} />
    </div>)}</div>
    {!aligned && first && <p className="view-caption">Showing the last synchronized interval while readings refresh.</p>}
    {first ? <div className="view-chart-scroll" role="region" tabIndex={0} aria-label={`${title}, mean line and minimum to maximum band`}>
      <ChartContainer className="view-chart-container">
        <ComposedChart data={points} margin={{ top: 8, right: 16, left: 0, bottom: 0 }} accessibilityLayer>
          <CartesianGrid vertical={false} stroke="#ffffff12" />
          <XAxis dataKey="time" type="number" domain={[Date.parse(first.from), Date.parse(first.to)]} tickFormatter={time => new Date(Number(time)).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })} stroke="#92938e" tickLine={false} minTickGap={45} />
          <YAxis domain={["auto", "auto"]} tickFormatter={value => Number(value).toFixed(first.format.fraction_digits)} width={45} stroke="#92938e" tickLine={false} axisLine={false} />
          <Tooltip content={({ active, label }) => {
            if (!active || label === undefined) return null;
            const index = first.buckets.findIndex(bucket => Date.parse(bucket.start) === Number(label));
            const bucket = first.buckets[index];
            if (!bucket) return null;
            return <div className="view-tooltip"><p>{new Date(bucket.start).toLocaleString()} – {new Date(bucket.end).toLocaleTimeString()}</p>
              {displayed.map((item, i) => {
                const b = item.resource.data?.buckets[index];
                const precision = item.resource.data?.format.fraction_digits;
                return <p key={i}>{item.label ?? item.resource.metadata?.label}: {b?.mean === null || b?.mean === undefined ? "No observations" : `mean ${b.mean.toFixed(precision)}, min ${b.min?.toFixed(precision)}, max ${b.max?.toFixed(precision)} ${item.resource.data?.format.unit_label}`}</p>;
              })}</div>;
          }} />
          {series.map((_, i) => <Area key={`band${i}`} type="linear" dataKey={`band${i}`} stroke="none" fill={colors[i % colors.length]} fillOpacity={0.12} connectNulls={false} isAnimationActive={false} />)}
          {series.map((_, i) => <Line key={`line${i}`} type="linear" dataKey={`mean${i}`} stroke={colors[i % colors.length]} strokeWidth={2} dot={{ r: 2, strokeWidth: 0, fill: colors[i % colors.length] }} connectNulls={false} isAnimationActive={false} />)}
        </ComposedChart>
      </ChartContainer>
    </div> : <p className="view-empty">{aligned ? "Historical readings will appear here." : "Synchronizing series intervals…"}</p>}
    <p className="view-caption">Retained observations · {bucketLabel} buckets · gaps mean no retained samples</p>
    <details><summary>Sources and interval</summary>{series.map((item, index) => <p key={index}><code>{item.resource.metadata?.id}</code></p>)}{first && <p>{first.from} – {first.to}</p>}</details>
  </section>;
}
export function BoundTimeSeries({ title, series, display }: { title: string; series: Array<{ source: NumericSource & { window: SeriesWindow }; label?: string }>; display: { unit: string } }) {
  const results = useNumericReads(series.map(item => ({ source: item.source, unit: display.unit, window: item.source.window })));
  return <TimeSeries key={JSON.stringify([display.unit, series.map(item => [item.source.entity_id, item.source.window])])} title={title} series={results.map((result, index) => ({ label: series[index].label,
    resource: { ...result, data: result.data && "buckets" in result.data ? result.data : undefined },
  }))} />;
}
