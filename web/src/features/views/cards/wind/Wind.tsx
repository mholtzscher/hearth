import type { z } from "zod";
import { cn } from "../../../../lib/utils";
import { DataCard } from "../DataCard";
import { Trend, trendMethod } from "../metric/Trend";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates } from "../reading-list/ReadingRows";
import { beaufort, beaufortNames, compassPoint } from "../weather/formulas";
import { canonicalValue, derivedTreatment, formatReading, shownData } from "../weather/readings";
import { SlotStatus, slotRows, useWeatherReads, WeatherReading, WeatherZones, weatherConditions, weatherDetail, weatherTrendParts } from "../weather/WeatherParts";
import { windSchema } from "./schema";

type Props = z.infer<typeof windSchema>;
type Slot = "speed" | "speedCanonical" | "gust" | "direction" | "max";

const ticks = Array.from({ length: 16 }, (_, index) => index * 22.5);

/** A compass rose. The arrow points where the wind blows, the opposite of where it comes from. */
function Compass({ bearing, calm, dimmed, wideTrend }: { bearing?: number; calm: boolean; dimmed: boolean; wideTrend: boolean }) {
  return <svg className={cn("size-[104px] self-center overflow-visible [grid-area:compass] @max-[290px]:size-[76px]", wideTrend && "@min-[460px]:size-[88px]")} viewBox="-60 -60 120 120" aria-hidden="true" focusable="false" data-value={dimmed ? "dimmed" : "live"}>
    <circle r="50" className="fill-none stroke-gray-4 [stroke-width:1]" />
    {ticks.map(angle => <line key={angle} className={angle % 90 === 0 ? "stroke-gray-7 [stroke-width:1.5] [stroke-linecap:round]" : "stroke-gray-5 [stroke-width:1] [stroke-linecap:round]"}
      x1="0" y1={-50} x2="0" y2={angle % 90 === 0 ? -42 : -46} transform={`rotate(${angle})`} />)}
    {(["N", "E", "S", "W"] as const).map((letter, index) => <text key={letter} className="fill-gray-8 text-[11px] font-medium [text-anchor:middle] [dominant-baseline:central]"
      x={Math.sin(index * Math.PI / 2) * 32} y={-Math.cos(index * Math.PI / 2) * 32}>{letter}</text>)}
    {calm ? <circle r="4" className="fill-none stroke-gray-7 [stroke-width:1.5]" />
      : bearing !== undefined && <g data-slot="weather-compass-arrow" className="transition-transform duration-[600ms] ease-out-expo motion-reduce:transition-none" transform={`rotate(${bearing + 180})`}>
        <line className={cn("[stroke-width:2] [stroke-linecap:round]", dimmed ? "stroke-gray-7 [stroke-dasharray:3_3]" : "stroke-gray-10")} x1="0" y1="26" x2="0" y2="-20" /><path className={dimmed ? "fill-gray-7" : "fill-gray-10"} d="M0,-30 L7,-16 L-7,-16 Z" /><circle className={dimmed ? "fill-gray-7" : "fill-gray-10"} cy="26" r="3" />
      </g>}
  </svg>;
}

/** Wind speed with its Beaufort name, gusts, and where the wind comes from on a compass. */
export function BoundWind({ label, speed, gust, direction, max_gust, trend }: Props) {
  const reads = useWeatherReads<Slot>([
    ["speed", { source: speed, window: trend }], ["speedCanonical", { source: speed, units: "canonical" }], ["gust", gust && { source: gust }],
    ["direction", direction && { source: direction, canonicalUnit: "deg" }], ["max", max_gust && { source: max_gust }],
  ]);
  const reading = reads.current("speed")!, status = cardStatus(reading);
  const metres = canonicalValue(reading, reads.current("speedCanonical"));
  const force = metres === undefined ? undefined : beaufort(metres);
  const directionReading = reads.current("direction");
  const bearing = shownData(directionReading)?.value;
  const from = bearing === undefined ? undefined : compassPoint(bearing);
  const calm = force === 0;
  const gustReading = reads.current("gust"), gustData = shownData(gustReading);
  const all = slotRows([["speed", "Wind speed", reading], ["gust", "Gusts", gustReading], ["direction", "Wind direction", directionReading], ["max", "Today's max gust", reads.current("max")]]);
  const displayed = rowStates(all);
  const maxRow = displayed.filter(row => row.key === "max");
  const history = reads.history("speed");
  const hasTrend = !!(trend && history && status.value !== "empty");
  return <DataCard className="@container" classNames={{ body: "flex flex-col gap-1.5" }} title={label ?? "Wind"} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)} sourceExtra={<>
      <p>Words follow the Beaufort scale for the current wind speed.</p>
      <p>Direction is where the wind comes from; the arrow points where it blows. There is no direction history, because averaging compass bearings gives wrong answers.</p>
      {trend && <><p>The trend shows sustained wind speed, not gusts or direction.</p>{trendMethod(trend)}</>}
    </>}>
    <WeatherZones wind aside={trend && history && status.value !== "empty" && <Trend window={trend} resource={history} dimmed={status.value === "dimmed"} className="@min-[460px]:mt-0" classNames={weatherTrendParts} />}>
      <div data-slot="wind-current" className={cn(
        "grid grid-cols-[minmax(0,1fr)] gap-x-[18px] gap-y-0 [grid-template-areas:'reading'_'gust'_'force'_'direction'_'rows'] @max-[290px]:gap-x-[14px]",
        direction && "grid-cols-[auto_minmax(0,1fr)] [grid-template-areas:'compass_reading'_'compass_gust'_'compass_force'_'compass_direction'_'rows_rows']",
        !hasTrend && "@min-[460px]:grid-cols-[minmax(max-content,1fr)_minmax(0,1fr)] @min-[460px]:items-center @min-[460px]:gap-x-6 @min-[460px]:[grid-template-areas:'reading_gust'_'force_direction'_'._rows']",
        !hasTrend && direction && "@min-[460px]:grid-cols-[auto_minmax(max-content,1fr)_minmax(0,2fr)] @min-[460px]:[grid-template-areas:'compass_reading_gust'_'compass_force_direction'_'compass_._rows']",
        hasTrend && "@min-[460px]:items-center",
        hasTrend && direction && "@min-[460px]:gap-x-3",
      )}>
        {direction && <Compass bearing={bearing} calm={calm} dimmed={directionReading !== undefined && cardStatus(directionReading).value === "dimmed"} wideTrend={hasTrend} />}
        <div className="min-w-0 [grid-area:reading]">
          <WeatherReading data={shownData(reading)} status={status} />
        </div>
        {gustReading && <p className={cn(weatherDetail, "[grid-area:gust] @min-[460px]:[overflow-wrap:anywhere]")} data-value={cardStatus(gustReading).value}>Gusts {gustData ? formatReading(gustData) : "—"}</p>}
        {force !== undefined && <p data-slot="weather-conditions" className={cn(weatherConditions, "[grid-area:force]")} data-value={status.value}>{beaufortNames[force]}</p>}
        {from && !calm && <p className={cn(weatherDetail, "[grid-area:direction] @min-[460px]:[overflow-wrap:anywhere]")} data-value={derivedTreatment(reading, directionReading)}>From {from.short} · {Math.round(bearing!)}°<span className="sr-only">, from the {from.words}</span></p>}
        {maxRow.length > 0 && <div className="mt-2.5 border-t border-border [grid-area:rows] @min-[460px]:mt-1"><ReadingRows className="mt-0" rows={maxRow} /></div>}
      </div>
    </WeatherZones>
    <SlotStatus name="Gusts" resource={gustReading} />
    <SlotStatus name="Wind direction" resource={directionReading} />
  </DataCard>;
}
