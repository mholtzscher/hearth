import type { z } from "zod";
import { ArrowDownRightIcon, ArrowRightIcon, ArrowUpRightIcon } from "lucide-react";
import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { CardFrame, StatusList } from "../CardFrame";
import { useMinuteClock } from "../observation";
import { cardStatus, type CardStatus } from "../status";
import { Trend, trendMethod } from "../metric/Trend";
import { pressureTendency } from "../weather/formulas";
import { shownData, threeHourChange } from "../weather/readings";
import { useWeatherReads, WeatherReading, WeatherZones } from "../weather/WeatherParts";
import { pressureSchema } from "./schema";

type Props = z.infer<typeof pressureSchema>;
const icons = { rising: ArrowUpRightIcon, falling: ArrowDownRightIcon, steady: ArrowRightIcon };

/**
 * The three-hour tendency in words from canonical hPa, with the change in the
 * displayed unit. Never guessed without a reading from three hours ago. The two
 * histories refresh separately, so both must use the same buckets; otherwise
 * the words and the change could describe different intervals.
 */
function TendencyLine({ data, history, canonical, status }: { data?: MetricData; history: Resolved<SeriesData>; canonical: Resolved<SeriesData>; status: CardStatus }) {
  const clock = useMinuteClock();
  if (!data) return null;
  const change = threeHourChange(history.data, clock), hPa = threeHourChange(canonical.data, clock);
  if (!change || !hPa || change.nowStart !== hPa.nowStart || change.thenStart !== hPa.thenStart) {
    const loading = history.status === "loading" || canonical.status === "loading";
    const failed = history.status === "error" || canonical.status === "error";
    const words = loading ? "Loading the last 3 hours…" : failed ? "Couldn't load the last 3 hours." : "Tendency unavailable · no recent or 3\u00a0h reading";
    return <p className="view-weather-detail">{words}</p>;
  }
  const tendency = pressureTendency(hPa.now - hPa.then);
  const Icon = icons[tendency.direction];
  const digits = history.data!.format.fraction_digits;
  const shown = Number((change.now - change.then).toFixed(digits));
  const sign = shown > 0 ? "+" : shown < 0 ? "−" : "±";
  return <p className="view-weather-tendency" data-value={status.value}>
    <Icon aria-hidden="true" />{tendency.words}
    <span>{sign}{Math.abs(shown).toFixed(digits)} {history.data!.format.unit_label} in 3&nbsp;h</span>
  </p>;
}

/** Barometric pressure with its three-hour tendency in words and an optional history line. */
export function BoundPressure({ label, source, trend }: Props) {
  const reads = useWeatherReads<"pressure" | "trend" | "canonical">([
    ["pressure", { source, window: "6h" }],
    ["trend", trend && trend !== "6h" ? { source, window: trend } : undefined],
    ["canonical", { source, window: "6h", units: "canonical" }],
  ]);
  const reading = reads.current("pressure")!, status = cardStatus(reading);
  const data = shownData(reading);
  const recent = reads.history("pressure")!, canonical = reads.history("canonical")!;
  const history = trend === "6h" ? recent : reads.history("trend");
  const metadata = reading.metadata;
  return <CardFrame className="view-weather view-pressure" title={label ?? "Pressure"} status={status.entries}
    observedAt={data?.observed_at} arrival={data ? { updatedAt: data.observed_at, shown: `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`, eligible: status.glows } : undefined}
    sources={metadata ? [{ entityId: metadata.id, label: metadata.label, availability: metadata.availability, enabled: metadata.enabled }] : []}
    sourceExtra={<>
      <p>The tendency compares the latest minute with the minute nearest three hours earlier, using Met Office thresholds: steady below 0.1 hPa, slowly to 1.5 hPa, quickly above 3.5 hPa and very rapidly above 6 hPa. With no reading within ten minutes of three hours ago, there is no tendency.</p>
      {trend && trendMethod(trend)}
    </>}>
    <WeatherZones aside={trend && history && status.value !== "empty" && <Trend window={trend} resource={history} dimmed={status.value === "dimmed"} />}>
      <WeatherReading data={data} status={status} />
      <TendencyLine data={data} history={recent} canonical={canonical} status={status} />
      {(recent.refreshFailed || canonical.refreshFailed) && <StatusList entries={[{ key: "refresh-failed", tone: "warn", text: "Couldn't refresh pressure history" }]} />}
    </WeatherZones>
  </CardFrame>;
}
