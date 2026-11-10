import type { z } from "zod";
import { useUnitSystem } from "../../../units/household";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates } from "../reading-list/ReadingRows";
import { beaufort, beaufortNames, compassPoint } from "../weather/formulas";
import { derivedTreatment, formatReading, shownData, workingReading } from "../weather/readings";
import { SlotStatus, slotRows, useWeatherReads, WeatherReading } from "../weather/WeatherParts";
import { windSchema } from "./schema";

type Props = z.infer<typeof windSchema>;
type Slot = "speed" | "gust" | "direction" | "max";

const ticks = Array.from({ length: 16 }, (_, index) => index * 22.5);

/** A compass rose. The arrow points where the wind blows, the opposite of where it comes from. */
function Compass({ bearing, calm, dimmed }: { bearing?: number; calm: boolean; dimmed: boolean }) {
  return <svg className="view-weather-compass" viewBox="-60 -60 120 120" aria-hidden="true" focusable="false" data-value={dimmed ? "dimmed" : "live"}>
    <circle r="50" className="view-weather-compass-ring" />
    {ticks.map(angle => <line key={angle} className={angle % 90 === 0 ? "view-weather-compass-major" : "view-weather-compass-minor"}
      x1="0" y1={-50} x2="0" y2={angle % 90 === 0 ? -42 : -46} transform={`rotate(${angle})`} />)}
    {(["N", "E", "S", "W"] as const).map((letter, index) => <text key={letter} className="view-weather-compass-letter"
      x={Math.sin(index * Math.PI / 2) * 32} y={-Math.cos(index * Math.PI / 2) * 32}>{letter}</text>)}
    {calm ? <circle r="4" className="view-weather-compass-calm" />
      : bearing !== undefined && <g className="view-weather-compass-arrow" transform={`rotate(${bearing + 180})`}>
        <line x1="0" y1="26" x2="0" y2="-20" /><path d="M0,-30 L7,-16 L-7,-16 Z" /><circle cy="26" r="3" />
      </g>}
  </svg>;
}

/** Wind speed with its Beaufort name, gusts, and where the wind comes from on a compass. */
export function BoundWind({ label, speed, gust, direction, max_gust }: Props) {
  const system = useUnitSystem() ?? "metric";
  const reads = useWeatherReads<Slot>([
    ["speed", { source: speed }], ["gust", gust && { source: gust }],
    ["direction", direction && { source: direction }], ["max", max_gust && { source: max_gust }],
  ]);
  const reading = reads.current("speed")!, status = cardStatus(reading);
  const metres = workingReading(reading, "speed", "m/s", system);
  const force = metres === undefined ? undefined : beaufort(metres);
  const directionReading = reads.current("direction");
  const bearing = shownData(directionReading)?.value;
  const from = bearing === undefined ? undefined : compassPoint(bearing);
  const calm = force === 0;
  const gustReading = reads.current("gust"), gustData = shownData(gustReading);
  const all = slotRows([["speed", "Wind speed", reading], ["gust", "Gusts", gustReading], ["direction", "Wind direction", directionReading], ["max", "Today's max gust", reads.current("max")]]);
  const displayed = rowStates(all);
  const maxRow = displayed.filter(row => row.key === "max");
  return <CardFrame className="view-weather view-wind" title={label ?? "Wind"} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)} sourceExtra={<>
      <p>Words follow the Beaufort scale for the current wind speed.</p>
      <p>Direction is where the wind comes from; the arrow points where it blows. There is no direction history, because averaging compass bearings gives wrong answers.</p>
    </>}>
    <div className="view-weather-wind-lead">
      {direction && <Compass bearing={bearing} calm={calm} dimmed={directionReading !== undefined && cardStatus(directionReading).value === "dimmed"} />}
      <div>
        <WeatherReading data={shownData(reading)} status={status} />
        {gustReading && <p className="view-weather-detail" data-value={cardStatus(gustReading).value}>Gusts {gustData ? formatReading(gustData) : "—"}</p>}
        {force !== undefined && <p className="view-weather-conditions" data-value={status.value}>{beaufortNames[force]}</p>}
        {from && !calm && <p className="view-weather-detail" data-value={derivedTreatment(reading, directionReading)}>From {from.short} · {Math.round(bearing!)}°<span className="sr-only">, from the {from.words}</span></p>}
      </div>
    </div>
    {maxRow.length > 0 && <div className="view-weather-rows"><ReadingRows rows={maxRow} /></div>}
    <SlotStatus name="Gusts" resource={gustReading} />
    <SlotStatus name="Wind direction" resource={directionReading} />
  </CardFrame>;
}
