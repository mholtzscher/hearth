import type { z } from "zod";
import { ArrowDownRightIcon, ArrowRightIcon, ArrowUpRightIcon } from "lucide-react";
import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import type { UnitSystem } from "../../../units/units";
import { useUnitSystem } from "../../../units/household";
import { CardFrame, StatusList } from "../CardFrame";
import { useMinuteClock } from "../observation";
import { cardStatus, type CardStatus } from "../status";
import { Trend, trendMethod } from "../metric/Trend";
import { pressureTendency } from "../weather/formulas";
import { shownData, threeHourChange, working } from "../weather/readings";
import { useWeatherReads, WeatherReading } from "../weather/WeatherParts";
import { pressureSchema } from "./schema";

type Props = z.infer<typeof pressureSchema>;
const icons = { rising: ArrowUpRightIcon, falling: ArrowDownRightIcon, steady: ArrowRightIcon };

/** The three-hour tendency in words, with the change in the displayed unit. Never guessed without a reading from three hours ago. */
function TendencyLine({ data, history, status, system }: { data?: MetricData; history: Resolved<SeriesData>; status: CardStatus; system: UnitSystem }) {
  const clock = useMinuteClock();
  if (!data) return null;
  const change = threeHourChange(history.data, clock);
  const now = change && working(change.now, "pressure", "hPa", system), then = change && working(change.then, "pressure", "hPa", system);
  if (!change || now === undefined || then === undefined) {
    const words = history.status === "loading" ? "Loading the last 3 hours…" : history.status === "error" ? "Couldn't load the last 3 hours." : "Tendency unavailable · no recent or 3 h reading";
    return <p className="view-weather-detail">{words}</p>;
  }
  const tendency = pressureTendency(now - then);
  const Icon = icons[tendency.direction];
  const digits = history.data!.format.fraction_digits;
  const shown = Number((change.now - change.then).toFixed(digits));
  const sign = shown > 0 ? "+" : shown < 0 ? "−" : "±";
  return <p className="view-weather-tendency" data-value={status.value}>
    <Icon aria-hidden="true" />{tendency.words}
    <span>{sign}{Math.abs(shown).toFixed(digits)} {history.data!.format.unit_label} in 3 h</span>
  </p>;
}

/** Barometric pressure with its three-hour tendency in words and an optional history line. */
export function BoundPressure({ label, source, trend }: Props) {
  const system = useUnitSystem() ?? "metric";
  const reads = useWeatherReads<"pressure" | "trend">([
    ["pressure", { source, window: "6h" }],
    ["trend", trend && trend !== "6h" ? { source, window: trend } : undefined],
  ]);
  const reading = reads.current("pressure")!, status = cardStatus(reading);
  const data = shownData(reading);
  const recent = reads.history("pressure")!;
  const history = trend === "6h" ? recent : reads.history("trend");
  const metadata = reading.metadata;
  return <CardFrame className="view-weather view-pressure" title={label ?? "Pressure"} status={status.entries}
    observedAt={data?.observed_at} arrival={data ? { updatedAt: data.observed_at, shown: `${data.value.toFixed(data.format.fraction_digits)} ${data.format.unit_label}`, eligible: status.glows } : undefined}
    sources={metadata ? [{ entityId: metadata.id, label: metadata.label, availability: metadata.availability, enabled: metadata.enabled }] : []}
    sourceExtra={<>
      <p>The tendency compares the latest minute with the minute nearest three hours earlier, using Met Office thresholds: steady below 0.1 hPa, slowly to 1.5 hPa, quickly above 3.5 hPa and very rapidly above 6 hPa. With no reading within ten minutes of three hours ago, there is no tendency.</p>
      {trend && trendMethod(trend)}
    </>}>
    <WeatherReading data={data} status={status} />
    <TendencyLine data={data} history={recent} status={status} system={system} />
    {recent.refreshFailed && <StatusList entries={[{ key: "refresh-failed", tone: "warn", text: "Couldn't refresh pressure history" }]} />}
    {trend && history && status.value !== "empty" && <Trend window={trend} resource={history} dimmed={status.value === "dimmed"} />}
  </CardFrame>;
}
