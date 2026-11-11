import type { z } from "zod";
import { CardFrame, StatusList } from "../CardFrame";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates } from "../reading-list/ReadingRows";
import { beaufort, compassPoint, rainWord, windWord } from "../weather/formulas";
import { canonicalValue, derivedTreatment, extremes, shownData } from "../weather/readings";
import { SlotStatus, slotRows, useWeatherReads, WeatherReading } from "../weather/WeatherParts";
import { outsideNowSchema } from "./schema";

type Props = z.infer<typeof outsideNowSchema>;
type Slot = "temperature" | "humidity" | "wind" | "windCanonical" | "direction" | "rate" | "rateCanonical" | "today";

/**
 * Outdoor temperature with conditions in words, the last 24 hours' extremes,
 * and the other readings as rows.
 */
export function BoundOutsideNow({ label, temperature, humidity, wind, wind_direction, rain_rate, rain_today }: Props) {
  const reads = useWeatherReads<Slot>([
    ["temperature", { source: temperature, window: "24h" }], ["humidity", humidity && { source: humidity }],
    ["wind", wind && { source: wind }], ["windCanonical", wind && { source: wind, units: "canonical" }],
    ["direction", wind_direction && { source: wind_direction }],
    ["rate", rain_rate && { source: rain_rate }], ["rateCanonical", rain_rate && { source: rain_rate, units: "canonical" }],
    ["today", rain_today && { source: rain_today }],
  ]);
  const reading = reads.current("temperature")!;
  const status = cardStatus(reading);
  const data = shownData(reading);
  const speed = canonicalValue(reads.current("wind"), reads.current("windCanonical"));
  const rate = canonicalValue(reads.current("rate"), reads.current("rateCanonical"));
  const bearing = shownData(reads.current("direction"))?.value;
  const rain = rate === undefined ? undefined : rainWord(rate), windy = speed === undefined ? undefined : windWord(speed);
  const words = [rain, windy].filter(Boolean);
  // Words dim with the readings that produced them, not with a dry rate that added none.
  const wordTreatment = derivedTreatment(rain ? reads.current("rate") : undefined, windy ? reads.current("wind") : undefined);
  const from = bearing !== undefined && speed !== undefined && beaufort(speed) > 0 ? compassPoint(bearing) : undefined;
  const history = reads.history("temperature")!;
  const range = extremes(history.data);
  const all = slotRows([
    ["temperature", "Temperature", reading], ["humidity", "Humidity", reads.current("humidity")],
    ["wind", from ? `Wind from ${from.short}` : "Wind", reads.current("wind")], ["direction", "Wind direction", reads.current("direction")],
    ["rate", "Rain rate", reads.current("rate")], ["today", "Rain today", reads.current("today")],
  ]);
  const displayed = rowStates(all);
  // Rate and direction normally speak through words. Without the canonical
  // reading those words need, they show as rows so no measurement disappears.
  const rateRow = rate === undefined, directionRow = speed === undefined;
  const rows = displayed.filter(row => row.key === "humidity" || row.key === "wind" || row.key === "today" ||
    (row.key === "rate" && rateRow) || (row.key === "direction" && directionRow));
  const title = label ?? "Outside";
  const digits = history.data?.format.fraction_digits ?? 1;
  return <CardFrame className="view-weather view-outside-now" title={title} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)} sourceExtra={<>
      <p>Conditions come from measurements only: rain words from the rain rate, wind words from the Beaufort scale.</p>
      <p>High and low use a 24-hour history interval, not since midnight, from 5-minute minimums and maximums. If a refresh fails, the card keeps the last loaded interval.</p>
      {history.data && <dl className="view-source-facts">
        {[{ label: "Temperature history from", at: history.data.from }, { label: "Temperature history to", at: history.data.to }].map(({ label, at }) =>
          <div key={label}><dt>{label}</dt><dd><time dateTime={at}>{new Date(at).toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit", timeZoneName: "short" })}</time></dd></div>)}
      </dl>}
    </>}>
    <WeatherReading data={data} status={status} />
    {words.length > 0 && <p className="view-weather-conditions" data-value={wordTreatment}>{words.join(" · ")}</p>}
    {data && <p className="view-weather-detail">{range
      ? `${history.refreshFailed ? "Last loaded 24 h:" : "24 h"} high ${range.high.toFixed(digits)}, low ${range.low.toFixed(digits)} ${history.data!.format.unit_label}`
      : history.status === "loading" ? "Loading the last 24 hours…" : history.status === "error" ? "Couldn't load the last 24 hours." : "No history in the last 24 h."}</p>}
    {history.refreshFailed && <StatusList entries={[{ key: "refresh-failed", tone: "warn", text: "Couldn't refresh temperature history" }]} />}
    {rows.length > 0 && <div className="view-weather-rows"><ReadingRows rows={rows} /></div>}
    {!directionRow && <SlotStatus name="Wind direction" resource={reads.current("direction")} />}
    {!rateRow && <SlotStatus name="Rain rate" resource={reads.current("rate")} />}
  </CardFrame>;
}
