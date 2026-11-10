import type { z } from "zod";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates } from "../reading-list/ReadingRows";
import { Trend, trendMethod } from "../metric/Trend";
import { shownData } from "../weather/readings";
import { slotRows, useWeatherReads, WeatherReading, type WeatherRead } from "../weather/WeatherParts";
import { rainPeriods, rainSchema, type RainPeriod } from "./schema";

type Props = z.infer<typeof rainSchema>;
type Slot = "rate" | "storm" | RainPeriod;
const periodNames: Record<RainPeriod, string> = { day: "Today", week: "This week", month: "This month", year: "This year" };

/** The selected rain rate and station counters, without inferring conditions. */
export function BoundRain({ label, rate, storm, totals, trend }: Props) {
  const reads = useWeatherReads<Slot>([
    ["rate", { source: rate, window: trend }], ["storm", storm && { source: storm }],
    ...rainPeriods.map((period): [Slot, WeatherRead | undefined] => [period, totals?.[period] && { source: totals[period] }]),
  ]);
  const reading = reads.current("rate")!, status = cardStatus(reading);
  const all = slotRows([["rate", "Rain rate", reading], ["storm", "This storm", reads.current("storm")],
    ...rainPeriods.map((period): [string, string, ReturnType<typeof reads.current>] => [period, periodNames[period], reads.current(period)])]);
  const displayed = rowStates(all), rows = displayed.filter(row => row.key !== "rate"), history = reads.history("rate");
  return <CardFrame className="view-weather view-rain" title={label ?? "Rain"} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)} sourceExtra={<>
      <p>Totals are the station's own counters, which it resets for each storm, day, week, month and year.</p>
      {trend && trendMethod(trend)}
    </>}>
    <WeatherReading data={shownData(reading)} status={status} />
    {trend && history && status.value !== "empty" && <Trend window={trend} resource={history} dimmed={status.value === "dimmed"} />}
    {rows.length > 0 && <div className="view-weather-rows"><ReadingRows rows={rows} /></div>}
  </CardFrame>;
}
