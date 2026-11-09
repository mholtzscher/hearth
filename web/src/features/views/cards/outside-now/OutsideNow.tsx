import type { z } from "zod";
import { useUnitSystem } from "../../../units/household";
import { CardFrame, StatusList } from "../CardFrame";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates } from "../reading-list/ReadingRows";
import { dewPoint, feelsLike, rainWord, windWord, beaufort, compassPoint } from "../weather/formulas";
import { derivedTreatment, extremes, formatTemperature, shownData, workingReading } from "../weather/readings";
import { SlotStatus, slotRows, useWeatherReads, WeatherReading } from "../weather/WeatherParts";
import { outsideNowSchema } from "./schema";

type Props = z.infer<typeof outsideNowSchema>;
type Slot = "temperature" | "humidity" | "wind" | "direction" | "rate" | "today";

/**
 * Outdoor temperature with conditions in words, derived feels-like and dew
 * point, the last 24 hours' extremes, and the other readings as rows.
 */
export function BoundOutsideNow({ label, temperature, humidity, wind, wind_direction, rain_rate, rain_today }: Props) {
  const system = useUnitSystem() ?? "metric";
  const reads = useWeatherReads<Slot>([
    ["temperature", { source: temperature, window: "24h" }], ["humidity", humidity && { source: humidity }],
    ["wind", wind && { source: wind }], ["direction", wind_direction && { source: wind_direction }],
    ["rate", rain_rate && { source: rain_rate }], ["today", rain_today && { source: rain_today }],
  ]);
  const reading = reads.current("temperature")!;
  const status = cardStatus(reading);
  const data = shownData(reading);
  const celsius = workingReading(reading, "temperature", "Cel", system);
  const percent = workingReading(reads.current("humidity"), "humidity", "%", system);
  const speed = workingReading(reads.current("wind"), "speed", "m/s", system);
  const rate = workingReading(reads.current("rate"), "rainRate", "mm/h", system);
  const bearing = shownData(reads.current("direction"))?.value;
  const feels = celsius === undefined ? undefined : feelsLike(celsius, percent, speed);
  const dew = celsius === undefined || percent === undefined ? undefined : dewPoint(celsius, percent);
  const words = [rate === undefined ? undefined : rainWord(rate), speed === undefined ? undefined : windWord(speed)].filter(Boolean);
  const derivedFrom = feels?.method === "wind chill" ? reads.current("wind") : reads.current("humidity");
  const derived = derivedTreatment(reading, derivedFrom, dew === undefined ? undefined : reads.current("humidity"));
  const from = bearing !== undefined && speed !== undefined && beaufort(speed) > 0 ? compassPoint(bearing) : undefined;
  const history = reads.history("temperature")!;
  const range = extremes(history.data);
  const all = slotRows([
    ["temperature", "Temperature", reading], ["humidity", "Humidity", reads.current("humidity")],
    ["wind", from ? `Wind from ${from.short}` : "Wind", reads.current("wind")], ["direction", "Wind direction", reads.current("direction")],
    ["rate", "Rain rate", reads.current("rate")], ["today", "Rain today", reads.current("today")],
  ]);
  const displayed = rowStates(all);
  const rows = displayed.filter(row => row.key === "humidity" || row.key === "wind" || row.key === "today");
  const title = label ?? "Outside";
  const digits = data?.format.fraction_digits ?? 1;
  return <CardFrame className="view-weather view-outside-now" title={title} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)} sourceExtra={<>
      {feels && <p>Feels like uses the NWS {feels.method} formula from the temperature and {feels.method === "heat index" ? "humidity" : "wind speed"}. It appears only where that formula applies.</p>}
      <p>Dew point uses the Magnus formula from temperature and humidity. Conditions come from measurements only: rain words from the rain rate, wind words from the Beaufort scale.</p>
      <p>High and low use a 24-hour history interval, not since midnight, from 5-minute minimums and maximums. If a refresh fails, the card keeps the last loaded interval.</p>
      {history.data && <dl className="view-source-facts">
        {[{ label: "Temperature history from", at: history.data.from }, { label: "Temperature history to", at: history.data.to }].map(({ label, at }) =>
          <div key={label}><dt>{label}</dt><dd><time dateTime={at}>{new Date(at).toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit", timeZoneName: "short" })}</time></dd></div>)}
      </dl>}
    </>}>
    <WeatherReading data={data} status={status} />
    {words.length > 0 && <p className="view-weather-conditions" data-value={derivedTreatment(reads.current("rate"), reads.current("wind"))}>{words.join(" · ")}</p>}
    {(feels || dew !== undefined) && <p className="view-weather-detail" data-value={derived}>
      {[feels && `Feels like ${formatTemperature(feels.celsius, system)}`, dew !== undefined && `Dew point ${formatTemperature(dew, system)}`].filter(Boolean).join(" · ")}
    </p>}
    {data && <p className="view-weather-detail">{range
      ? `${history.refreshFailed ? "Last loaded 24 h:" : "24 h"} high ${range.high.toFixed(digits)}, low ${range.low.toFixed(digits)} ${data.format.unit_label}`
      : history.status === "loading" ? "Loading the last 24 hours…" : history.status === "error" ? "Couldn't load the last 24 hours." : "No history in the last 24 h."}</p>}
    {history.refreshFailed && <StatusList entries={[{ key: "refresh-failed", tone: "warn", text: "Couldn't refresh temperature history" }]} />}
    {rows.length > 0 && <div className="view-weather-rows"><ReadingRows rows={rows} /></div>}
    <SlotStatus name="Wind direction" resource={reads.current("direction")} />
    <SlotStatus name="Rain rate" resource={reads.current("rate")} />
  </CardFrame>;
}
