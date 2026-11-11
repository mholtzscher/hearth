import type { z } from "zod";
import type { MetricData, Resolved } from "../../bindings/types";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, rowSources, rowStates } from "../reading-list/ReadingRows";
import { Trend, trendMethod } from "../metric/Trend";
import { rainWord } from "../weather/formulas";
import { canonicalValue, derivedTreatment, formatReading, shownData } from "../weather/readings";
import { SlotStatus, slotRows, useWeatherReads, WeatherReading, WeatherZones, type WeatherRead } from "../weather/WeatherParts";
import { rainPeriods, rainSchema, type RainPeriod } from "./schema";

type Props = z.infer<typeof rainSchema>;
type Slot = "rate" | "rateCanonical" | "storm" | RainPeriod;
const periodNames: Record<RainPeriod, string> = { day: "Today", week: "This week", month: "This month", year: "This year" };

/** One status line for totals that share their statuses, such as a station that stopped reporting; otherwise one per total. */
function TotalsStatus({ totals }: { totals: Array<{ period: RainPeriod; resource: Resolved<MetricData> }> }) {
  const flagged = totals.map(total => ({ ...total, keys: cardStatus(total.resource).entries.map(entry => entry.key).join() })).filter(total => total.keys);
  if (flagged.length > 1 && flagged.length === totals.length && flagged.every(total => total.keys === flagged[0].keys)) {
    return <SlotStatus name="Rain totals" resource={flagged[0].resource} />;
  }
  return <>{flagged.map(total => <SlotStatus key={total.period} name={periodNames[total.period]} resource={total.resource} />)}</>;
}

/**
 * While the station's storm total is above zero it leads, even after the rain
 * stops; otherwise the rain rate does, or Dry at zero. The station's own
 * counters follow in one line, never sums of the rate.
 */
export function BoundRain({ label, rate, storm, totals, trend }: Props) {
  const reads = useWeatherReads<Slot>([
    ["rate", { source: rate, window: trend }], ["rateCanonical", { source: rate, units: "canonical" }], ["storm", storm && { source: storm }],
    ...rainPeriods.map((period): [Slot, WeatherRead | undefined] => [period, totals?.[period] && { source: totals[period] }]),
  ]);
  const rateReading = reads.current("rate")!, rateStatus = cardStatus(rateReading);
  const rateData = shownData(rateReading);
  const mmPerHour = canonicalValue(rateReading, reads.current("rateCanonical"));
  const stormReading = reads.current("storm"), stormData = shownData(stormReading);
  const stormLeads = stormData !== undefined && stormData.value > 0;
  // The header speaks for the leading reading; the other keeps its own status line.
  const status = stormLeads ? cardStatus(stormReading!) : rateStatus;
  const shownTotals = rainPeriods.flatMap(period => {
    const resource = reads.current(period);
    return resource ? [{ period, resource, data: shownData(resource) }] : [];
  });
  const totalsLine = shownTotals.filter(total => total.data);
  // Core can project counters differently, such as classified inches beside an unclassified mm sensor.
  const sharedUnit = totalsLine.every(total => total.data!.format.unit_label === totalsLine[0].data!.format.unit_label);
  const all = slotRows([["rate", "Rain rate", rateReading], ["storm", "This storm", stormReading],
    ...shownTotals.map(total => [total.period, periodNames[total.period], total.resource] as [string, string, Resolved<MetricData>])]);
  const displayed = rowStates(all);
  const history = reads.history("rate");
  const word = mmPerHour === undefined ? undefined : rainWord(mmPerHour);
  return <CardFrame className="view-weather view-rain" title={label ?? "Rain"} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)} sourceExtra={<>
      <p>Rain or Dry comes from the rain rate: light rain is below 2.5 mm/h and heavy rain above 7.6 mm/h. The storm total leads while it is above zero.</p>
      <p>Totals are the station's own counters, which it resets for each storm, day, week, month and year.</p>
      {trend && trendMethod(trend)}
    </>}>
    <WeatherZones aside={trend && history && rateStatus.value !== "empty" && <Trend window={trend} resource={history} dimmed={rateStatus.value === "dimmed"} />}>
      {stormLeads ? <>
        <WeatherReading data={stormData} status={status} />
        <p className="view-weather-conditions" data-value={derivedTreatment(stormReading, rateReading)}>This storm{!rateData ? "" : mmPerHour === undefined ? ` · Rate ${formatReading(rateData)}` : word ? ` · ${word} now, ${formatReading(rateData)}` : " · Stopped for now"}</p>
      </> : rateData && mmPerHour === 0 ? <>
        <WeatherReading status={rateStatus} words="Dry" />
        <p className="view-weather-detail" data-value={rateStatus.value}>Rate {formatReading(rateData)}</p>
      </> : <>
        <WeatherReading data={rateData} status={rateStatus} />
        {word && <p className="view-weather-conditions" data-value={rateStatus.value}>{word}</p>}
      </>}
    </WeatherZones>
    {totalsLine.length > 0 && <p className="view-weather-inline">
      {totalsLine.map((total, index) => <span key={total.period} data-value={cardStatus(total.resource).value}>
        {index > 0 && " · "}{periodNames[total.period]} {sharedUnit ? total.data!.value.toFixed(total.data!.format.fraction_digits) : formatReading(total.data!)}
      </span>)}{sharedUnit && ` ${totalsLine[0].data!.format.unit_label}`}
    </p>}
    <SlotStatus name={stormLeads ? "Rain rate" : "This storm"} resource={stormLeads ? rateReading : stormReading} />
    <TotalsStatus totals={shownTotals} />
  </CardFrame>;
}
