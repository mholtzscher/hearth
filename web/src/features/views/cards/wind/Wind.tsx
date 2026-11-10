import type { z } from "zod";
import { CardFrame } from "../CardFrame";
import { cardStatus } from "../status";
import { listArrivals, oldestObservation, ReadingRows, rowSources, rowStates } from "../reading-list/ReadingRows";
import { shownData } from "../weather/readings";
import { slotRows, useWeatherReads, WeatherReading } from "../weather/WeatherParts";
import { windSchema } from "./schema";

type Props = z.infer<typeof windSchema>;
type Slot = "speed" | "gust" | "direction" | "max";

/** Wind readings supplied by the selected Entities. */
export function BoundWind({ label, speed, gust, direction, max_gust }: Props) {
  const reads = useWeatherReads<Slot>([
    ["speed", { source: speed }], ["gust", gust && { source: gust }],
    ["direction", direction && { source: direction }], ["max", max_gust && { source: max_gust }],
  ]);
  const reading = reads.current("speed")!, status = cardStatus(reading);
  const all = slotRows([["speed", "Wind speed", reading], ["gust", "Gusts", reads.current("gust")],
    ["direction", "Wind direction", reads.current("direction")], ["max", "Today's max gust", reads.current("max")]]);
  const displayed = rowStates(all), rows = displayed.filter(row => row.key !== "speed");
  return <CardFrame className="view-weather view-wind" title={label ?? "Wind"} status={status.entries}
    observedAt={oldestObservation(displayed)} observedLabel={all.length > 1 ? "Oldest reading" : "Observed"}
    arrivals={listArrivals(displayed)} sources={rowSources(all)}>
    <WeatherReading data={shownData(reading)} status={status} />
    {rows.length > 0 && <div className="view-weather-rows"><ReadingRows rows={rows} /></div>}
  </CardFrame>;
}
