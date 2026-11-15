import type { z } from "zod";
import { DataCard, StatusList } from "../DataCard";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates } from "../reading-list/ReadingRows";
import { beaufort, compassPoint, rainWord, windWord } from "../weather/formulas";
import { canonicalValue, derivedTreatment, extremes, shownData } from "../weather/readings";
import { SlotStatus, slotRows, useWeatherReads, WeatherReading, WeatherZones, weatherConditions, weatherDetail } from "../weather/WeatherParts";
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
    ["direction", wind_direction && { source: wind_direction, canonicalUnit: "deg" }],
    ["rate", rain_rate && { source: rain_rate, canonicalUnit: "mm/h" }], ["rateCanonical", rain_rate && { source: rain_rate, units: "canonical", canonicalUnit: "mm/h" }],
    ["today", rain_today && { source: rain_today, canonicalUnit: "mm" }],
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
    (row.key === "rate" && rateRow) || (row.key === "direction" && directionRow))
    .map(row => row.key === "wind" && from ? { ...row, nameTreatment: derivedTreatment(reads.current("wind"), reads.current("direction")) } : row);
  const title = label ?? "Outside";
  const digits = history.data?.format.fraction_digits ?? 1;
  return <DataCard className="@container" classNames={{ body: "flex flex-col gap-1.5" }} title={title} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)} sourceExtra={<>
      <p>Conditions come from measurements only: rain words from the rain rate, wind words from the Beaufort scale.</p>
      <p>High and low use a 24-hour history interval, not since midnight, from 5-minute minimums and maximums. If a refresh fails, the card keeps the last loaded interval.</p>
      {history.data && <dl className="m-0 grid gap-4 tabular-nums">
        {[{ label: "Temperature history from", at: history.data.from }, { label: "Temperature history to", at: history.data.to }].map(({ label, at }) =>
          <div className="grid gap-[3px]" key={label}><dt className="text-label text-gray-8">{label}</dt><dd className="m-0"><time dateTime={at}>{new Date(at).toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit", timeZoneName: "short" })}</time></dd></div>)}
      </dl>}
    </>}>
    <WeatherZones aside={rows.length > 0 && <div data-slot="weather-rows" className="mt-2.5 border-t border-border @min-[460px]:mt-0 @min-[460px]:border-t-0"><ReadingRows className="mt-0" rows={rows} /></div>}>
      <WeatherReading data={data} status={status} />
      {words.length > 0 && <p data-slot="weather-conditions" className={weatherConditions} data-value={wordTreatment}>{words.join(" · ")}</p>}
      {data && <p className={weatherDetail}>{range
        ? `${history.refreshFailed ? "Last loaded 24 h:" : "24 h"} high ${range.high.toFixed(digits)}, low ${range.low.toFixed(digits)} ${history.data!.format.unit_label}`
        : history.status === "loading" ? "Loading the last 24 hours…" : history.status === "error" ? "Couldn't load the last 24 hours." : "No history in the last 24 h."}</p>}
      {history.refreshFailed && <StatusList entries={[{ key: "refresh-failed", tone: "warn", text: "Couldn't refresh temperature history" }]} />}
    </WeatherZones>
    {!directionRow && <SlotStatus name="Wind direction" resource={reads.current("direction")} />}
    {!rateRow && <SlotStatus name="Rain rate" resource={reads.current("rate")} />}
  </DataCard>;
}
