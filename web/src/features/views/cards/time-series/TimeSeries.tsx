import { Area, CartesianGrid, ComposedChart, Line, Tooltip, XAxis, YAxis } from "recharts";
import { useEffect, useState } from "react";
import { ChartContainer } from "../../../../components/ui/chart";
import { useNumericReads } from "../../bindings/registry";
import type { NumericSource, Resolved, SeriesData, SeriesWindow } from "../../bindings/types";
import { CardFrame, StatusList, type SourceItem } from "../CardFrame";
import { cardStatus } from "../status";
import { coverageStart, niceTicks, sameDay, timeLabel, timeTicks } from "./scale";

// Series colors come from the eight chart tokens; orange is never a series color.
const colors = [1, 2, 3, 4, 5, 6, 7, 8].map(n => `var(--chart-${n})`);

function windowWords(data: SeriesData): string {
  const hours = Math.round((Date.parse(data.to) - Date.parse(data.from)) / 3_600_000);
  if (hours >= 48 && hours % 24 === 0) return `${hours / 24} days`;
  return hours === 1 ? "1 hour" : `${hours} hours`;
}

/** One sentence per series, so the plot has a text equivalent that names the trend rather than the ticks. */
function seriesSummary(label: string, data: SeriesData | undefined): string {
  if (!data) return `${label}: no readings yet.`;
  const digits = data.format.fraction_digits;
  const unit = data.format.unit_label ? ` ${data.format.unit_label}` : "";
  const sampled = data.buckets.filter(b => b.mean !== null);
  const latest = sampled[sampled.length - 1]?.mean;
  if (latest === undefined || latest === null) return `${label}: no samples over the last ${windowWords(data)}.`;
  const lows = sampled.flatMap(b => b.min === null ? [] : [b.min]);
  const highs = sampled.flatMap(b => b.max === null ? [] : [b.max]);
  return `${label}: latest ${latest.toFixed(digits)}${unit}, range ${Math.min(...lows).toFixed(digits)} to ${Math.max(...highs).toFixed(digits)} over the last ${windowWords(data)}.`;
}

export function TimeSeries({ title, series }: { title: string; series: Array<{ label?: string; resource: Resolved<SeriesData> }> }) {
  const latest = series.find(item => item.resource.data)?.resource.data;
  const aligned = series.every(item => !item.resource.data || !latest || (item.resource.data.from === latest.from && item.resource.data.to === latest.to));
  const [retained, setRetained] = useState(() => aligned ? series : []);
  useEffect(() => { if (aligned && latest) setRetained(series); }, [aligned, latest, series]);
  // Keep one coherent interval while sibling reads settle or fail. Status still
  // comes from the latest reads, so a retained chart never hides refresh failure.
  const displayed = aligned ? series : series.map((item, i) => ({ ...item, resource: { ...item.resource, data: retained[i]?.resource.data } }));
  const first = displayed.find(item => item.resource.data)?.resource.data;
  const items = displayed.map((item, i) => {
    const status = cardStatus(series[i].resource);
    return {
      label: item.label ?? item.resource.metadata?.label ?? `Series ${i + 1}`,
      color: colors[i % colors.length],
      data: item.resource.data,
      status,
      // A series whose Entity is unavailable or disabled is drawn dashed without its band.
      dimmed: status.value === "dimmed",
    };
  });
  const points = first?.buckets.map((bucket, index) => {
    const point: Record<string, number | string | number[] | null> = { time: Date.parse(bucket.start), end: bucket.end };
    items.forEach((item, i) => {
      const b = item.data?.buckets[index];
      point[`mean${i}`] = b?.mean ?? null;
      point[`band${i}`] = b && b.min !== null && b.max !== null ? [b.min, b.max] : null;
    });
    return point;
  });
  const values = items.flatMap(item => item.data?.buckets.flatMap(b => [b.min, b.max, b.mean].filter((v): v is number => v !== null)) ?? []);
  const y = values.length ? niceTicks(Math.min(...values), Math.max(...values), 4) : undefined;
  const from = first ? Date.parse(first.from) : 0;
  const to = first ? Date.parse(first.to) : 0;
  const x = first ? timeTicks(from, to) : [];
  const xLabels = new Map(x.map(tick => [tick.value, tick.label]));
  const since = coverageStart(items.flatMap(item => item.data ? [item.data] : []));
  const bucketMinutes = first ? (to - from) / first.buckets.length / 60_000 : null;
  const unit = first?.format.unit_label;
  // History and current State refresh independently. Compare the displayed
  // history interval, not State's timestamp, so only a changed plot glows.
  const arrivals = items.flatMap((item, index) => {
    if (!item.data) return [];
    const data = item.data;
    const latest = [...data.buckets].reverse().find(bucket => bucket.mean !== null)?.mean;
    return [{ key: String(index), updatedAt: data.to, shown: latest === undefined || latest === null ? "no samples" : `${latest.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`,
      eligible: aligned && item.status.glows }];
  });
  const sources = new Map<string, SourceItem>();
  items.forEach((item, i) => {
    const metadata = series[i].resource.metadata;
    if (metadata && !sources.has(metadata.id)) sources.set(metadata.id, { entityId: metadata.id, label: metadata.label === item.label ? item.label : `${item.label} · ${metadata.label}`, availability: metadata.availability, enabled: metadata.enabled });
  });
  const method = bucketMinutes === null ? undefined : <>
    <p>Each point averages the samples in a {bucketMinutes >= 1 ? `${Number(bucketMinutes.toFixed(1))}-minute` : `${Math.round(bucketMinutes * 60)}-second`} interval; the shaded band spans their minimum and maximum. Gaps mean no samples or a reported absent field.</p>
    {first && <p>{new Date(from).toLocaleString()} to {new Date(to).toLocaleString()}</p>}
  </>;
  return <CardFrame className="view-chart" title={title} unit={unit} status={[]} arrivals={arrivals} sources={[...sources.values()]} sourceExtra={method}>
    <ul className="view-chart-legend">{items.map((item, i) => <li key={i} data-dimmed={item.dimmed || undefined}>
      <span className="view-chart-swatch" aria-hidden="true" style={{ background: item.color }} />{item.label}
      <StatusList entries={item.status.entries} />
    </li>)}</ul>
    {!aligned && first && <p className="view-chart-note">Showing the last synchronized interval while readings refresh.</p>}
    {since && <p className="view-chart-note">History since {timeLabel(Date.parse(since), { weekday: !sameDay(Date.parse(since), Date.now()) })}</p>}
    {first ? <div className="view-chart-scroll" role="region" tabIndex={0} aria-label={`${title}, average line with minimum to maximum band`}>
      <ul className="sr-only">{items.map((item, i) => <li key={i}>{seriesSummary(item.label, item.data)}{item.dimmed ? " Unavailable." : ""}</li>)}</ul>
      <ChartContainer className="view-chart-container" aria-hidden="true">
        <ComposedChart data={points} margin={{ top: 8, right: 16, left: 0, bottom: 0 }} accessibilityLayer={false}>
          <CartesianGrid vertical={false} stroke="var(--gray-3)" />
          <XAxis dataKey="time" type="number" domain={[from, to]} ticks={x.map(tick => tick.value)} tickFormatter={value => xLabels.get(Number(value)) ?? ""} stroke="var(--gray-8)" tickLine={false} tickMargin={8} interval="preserveStart" minTickGap={12} />
          <YAxis domain={y?.domain ?? ["auto", "auto"]} ticks={y?.ticks} tickFormatter={value => Number(value).toFixed(first.format.fraction_digits)} width={48} stroke="var(--gray-8)" tickLine={false} axisLine={false} tickMargin={6} />
          <Tooltip content={({ active, label }) => {
            if (!active || label === undefined) return null;
            const index = first.buckets.findIndex(bucket => Date.parse(bucket.start) === Number(label));
            const bucket = first.buckets[index];
            if (!bucket) return null;
            const start = Date.parse(bucket.start);
            return <div className="view-tooltip"><p>{timeLabel(start, { weekday: !sameDay(start, Date.now()) })}</p>
              {items.map((item, i) => {
                const b = item.data?.buckets[index];
                const digits = item.data?.format.fraction_digits;
                const itemUnit = item.data?.format.unit_label ? ` ${item.data.format.unit_label}` : "";
                const reading = b?.mean === null || b?.mean === undefined ? "no samples" : `avg ${b.mean.toFixed(digits)}${itemUnit} (${b.min?.toFixed(digits)}–${b.max?.toFixed(digits)})`;
                return <p key={i}><span className="view-chart-swatch" aria-hidden="true" style={{ background: item.color }} />{item.label} {reading}{item.dimmed ? " (unavailable)" : ""}</p>;
              })}</div>;
          }} />
          {items.map((item, i) => item.dimmed ? null : <Area key={`band${i}`} type="linear" dataKey={`band${i}`} stroke="none" fill={item.color} fillOpacity={0.12} connectNulls={false} isAnimationActive={false} />)}
          {items.map((item, i) => <Line key={`line${i}`} type="linear" dataKey={`mean${i}`} stroke={item.color} strokeWidth={2} strokeDasharray={item.dimmed ? "4 4" : undefined} strokeOpacity={item.dimmed ? 0.6 : 1}
            dot={{ r: 2, strokeWidth: 0, fill: item.color, fillOpacity: item.dimmed ? 0.6 : 1 }} connectNulls={false} isAnimationActive={false} />)}
        </ComposedChart>
      </ChartContainer>
    </div> : <p className="view-empty">{aligned ? "Historical readings will appear here." : "Synchronizing series intervals…"}</p>}
  </CardFrame>;
}
export function BoundTimeSeries({ title, series }: { title: string; series: Array<{ source: NumericSource & { window: SeriesWindow }; label?: string }> }) {
  const results = useNumericReads(series.map(item => ({ source: item.source, window: item.source.window })));
  return <TimeSeries key={JSON.stringify([series.map(item => [item.source.entity_id, item.source.field ?? "", item.source.window])])} title={title} series={results.map((result, index) => ({ label: series[index].label,
    resource: { ...result, data: result.data && "buckets" in result.data ? result.data : undefined },
  }))} />;
}
