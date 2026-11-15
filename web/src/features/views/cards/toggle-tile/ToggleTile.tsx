import type { z } from "zod";
import { usePowerReadings } from "../../bindings/power";
import { useNumericReads } from "../../bindings/registry";
import { useControl } from "../../data/provider";
import { supportsOperation } from "../../controls/support";
import { ControlStatus } from "../../controls/ControlStatus";
import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { StatusList } from "../CardFrame";
import { TileFrame, tileActionClass } from "../TileFrame";
import { Reading } from "../Reading";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates, type ReadingRow } from "../reading-list/ReadingRows";
import { Trend, trendMethod } from "../metric/Trend";
import { toggleTileSchema } from "./schema";

export function BoundToggleTile({ source, label, metric }: z.infer<typeof toggleTileSchema>) {
  const [power] = usePowerReadings([source]);
  const numeric = useNumericReads(metric ? [{ source: metric.source }, { source: metric.source, window: metric.window }, ...(metric.detail ? [{ source: metric.detail.source }] : [])] : []);
  const current = numeric[0];
  const reading: Resolved<MetricData> | undefined = current && { ...current, data: current.data && "value" in current.data ? current.data : undefined };
  const history = numeric[1];
  const series: Resolved<SeriesData> | undefined = history && { ...history, data: history.data && "buckets" in history.data ? history.data : undefined };
  const detail = numeric[2];
  const [control, request] = useControl({ entityId: source.entity_id, operation: "set" });
  const working = control.phase === "pending" || control.phase === "confirming";
  const status = cardStatus(power.resource), metricStatus = reading && cardStatus(reading);
  const data = status.value === "empty" ? undefined : power.resource.data;
  const measured = metricStatus?.value === "empty" ? undefined : reading?.data;
  const title = label ?? power.resource.metadata?.label ?? "Power";
  const canSet = !!power.entity && supportsOperation(power.entity, "set");
  const on = data?.on;
  const rows: ReadingRow[] = [{ key: source.entity_id, name: title, resource: { ...power.resource, data: data && { text: on ? "On" : "Off", observed_at: data.observed_at } } }];
  if (reading) rows.push({ key: `metric:${metric!.source.entity_id}:${metric!.source.field ?? ""}`, name: reading.metadata?.label ?? "Metering reading", resource: reading });
  if (detail) rows.push({ key: `detail:${metric!.detail!.source.entity_id}:${metric!.detail!.source.field ?? ""}`, name: metric!.detail!.label ?? detail.metadata?.label ?? "Reading", resource: { ...detail, data: detail.data && "value" in detail.data ? detail.data : undefined } });
  const displayed = rowStates(rows), sources = rowSources(rows);
  const referenced = [source, ...(metric ? [metric.source, ...(metric.detail ? [metric.detail.source] : [])] : [])];
  rows.forEach((row, index) => { const id = referenced[index].entity_id; if (!sources.some(source => source.entityId === id)) sources.push({ entityId: id, label: row.name }); });
  const readingBody = <Reading as="span" value={status.value} className={metric ? "block @max-[200px]:text-reading-narrow max-tablet:flex max-tablet:flex-wrap max-tablet:items-baseline max-tablet:justify-between max-tablet:gap-x-3 max-tablet:gap-y-1" : "block @max-[200px]:text-reading-narrow"}>
    {data ? on ? "On" : "Off" : "—"}
    {metric && <small data-slot="power-metric" className="ms-1.5 inline-block whitespace-nowrap text-label tracking-normal text-muted-foreground data-[value=empty]:text-gray-6 max-tablet:ms-0 max-tablet:max-w-full max-tablet:whitespace-normal max-tablet:[overflow-wrap:anywhere]" data-value={metricStatus?.value}>{measured ? <><span className="max-tablet:text-compact-reading max-tablet:font-normal max-tablet:tracking-[-.02em]">{measured.value.toFixed(measured.format.fraction_digits)}</span>{measured.format.unit_label && ` ${measured.format.unit_label}`}</> : "—"}</small>}
  </Reading>;
  return <TileFrame title={title} on={!!on && status.value === "live"} interactive={canSet} metered={!!metric} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={metric ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={sources}
    sourceExtra={<><p>On/Off comes from observed power State. Pressing the tile requests a change; the reading and surface wait for observed State.</p><p>Since times use recorded changes over the last 24 hours, not observation age.</p>{power.historyError && <p>Couldn't load the since time. {power.historyError}</p>}{metric && <><p>Metering source: {reading?.metadata?.label ?? metric.source.entity_id}. The number and sparkline share this source.</p>{trendMethod(metric.window)}</>}</>}>
    {canSet ? <button type="button" role="switch" data-slot="tile-action" className={tileActionClass} aria-label={`${title} power`} aria-checked={on ?? false} aria-busy={working || undefined}
      disabled={!power.entity!.enabled} onClick={() => request(!(working ? control.requested === true : on === true))}>{readingBody}</button> : <p>{readingBody}</p>}
    {canSet ? <ControlStatus className="mt-0 min-h-[21px]" entityId={source.entity_id} subject="power" pending={value => value === true ? "Turning on…" : "Turning off…"} idle={power.since} /> : <p className="mt-0 min-h-[21px] text-label leading-normal text-muted-foreground">{power.entity ? "Read-only" : power.since}</p>}
    {metric && <>
      {metricStatus && metricStatus.entries.length > 0 && <div className="grid gap-0.5 text-label text-muted-foreground"><span>{reading?.metadata?.label ?? "Metering reading"}</span><StatusList entries={metricStatus.entries} /></div>}
      {series && <Trend className="mt-0.5" bandClassName="[fill-opacity:.05]" window={metric.window} resource={series} dimmed={metricStatus?.value === "dimmed"} />}
    </>}
    {detail && <ReadingRows className="mt-2 border-t border-border" rows={displayed.slice(2)} />}
  </TileFrame>;
}
