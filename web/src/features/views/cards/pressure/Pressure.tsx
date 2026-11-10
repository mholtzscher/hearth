import type { z } from "zod";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";
import { Trend, trendMethod } from "../metric/Trend";
import { shownData } from "../weather/readings";
import { useWeatherReads, WeatherReading } from "../weather/WeatherParts";
import { pressureSchema } from "./schema";

type Props = z.infer<typeof pressureSchema>;

/** Barometric pressure with an optional history chart. */
export function BoundPressure({ label, source, trend }: Props) {
  const reads = useWeatherReads<"pressure">([["pressure", { source, window: trend }]]);
  const reading = reads.current("pressure")!, status = cardStatus(reading);
  const data = shownData(reading), history = reads.history("pressure"), metadata = reading.metadata;
  return <CardFrame className="view-weather view-pressure" title={label ?? "Pressure"} status={status.entries}
    observedAt={data?.observed_at} arrival={data ? { updatedAt: data.observed_at, shown: `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`, eligible: status.glows } : undefined}
    sources={metadata ? [{ entityId: metadata.id, label: metadata.label, availability: metadata.availability, enabled: metadata.enabled }] : []}
    sourceExtra={trend && trendMethod(trend)}>
    <WeatherReading data={data} status={status} />
    {trend && history && status.value !== "empty" && <Trend window={trend} resource={history} dimmed={status.value === "dimmed"} />}
  </CardFrame>;
}
