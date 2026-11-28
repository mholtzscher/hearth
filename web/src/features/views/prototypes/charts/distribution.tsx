import { niceTicks } from "../../cards/time-series/scale";
import { livingRoomWeek } from "./fixtures";
import { Answer, ChartCard, Plot, source } from "./parts";

// One known hour of the week counts as one hour spent at its average.
const hours = livingRoomWeek.flatMap((day) =>
  day.hours.flatMap((cell) => (cell.value === null ? [] : [cell.value]))
);
const known = hours.length;

const binWidth = 0.5;
const low = Math.floor(Math.min(...hours) / binWidth) * binWidth;
const binCount = Math.floor((Math.max(...hours) - low) / binWidth) + 1;
const bins = Array.from({ length: binCount }, (_, index) => {
  const from = low + index * binWidth;
  return {
    from,
    to: from + binWidth,
    count: hours.filter((value) => value >= from && value < from + binWidth)
      .length,
  };
});
const [mode] = bins.toSorted((a, b) => b.count - a.count);
const yScale = niceTicks(0, mode.count, 3);
const [, top] = yScale.domain;

/** Histogram: hours spent in each half-degree, to show the shape of the week. */
const Histogram = () => (
  <>
    <Answer value={`${mode.from}–${mode.to} °C`}>
      most often · {mode.count} of {known} hours
    </Answer>
    <Plot
      label="Hours at each living room temperature"
      summary={bins.map(
        (bin) => `${bin.from} to ${bin.to} °C: ${bin.count} hours.`
      )}
      className="text-caption text-muted-foreground grid grid-cols-[2rem_minmax(0,1fr)] gap-x-2"
    >
      <div className="relative h-44">
        {yScale.ticks.map((tick) => (
          <span
            className="absolute right-0 translate-y-1/2 leading-none"
            key={tick}
            style={{ bottom: `${(tick / top) * 100}%` }}
          >
            {tick} h
          </span>
        ))}
      </div>
      <div className="border-gray-4 relative h-44 border-b">
        {yScale.ticks.slice(1).map((tick) => (
          <span
            className="bg-gray-3 absolute inset-x-0 h-px"
            key={tick}
            style={{ bottom: `${(tick / top) * 100}%` }}
          />
        ))}
        <div
          className="relative grid h-full items-end gap-x-0.5"
          style={{ gridTemplateColumns: `repeat(${binCount}, minmax(0, 1fr))` }}
        >
          {bins.map((bin) => (
            <span
              className="bg-chart-1 rounded-t-xs"
              key={bin.from}
              style={{ height: `${(bin.count / top) * 100}%` }}
            />
          ))}
        </div>
      </div>
      <span />
      <div className="relative mt-2 h-5">
        <span className="absolute right-0">°C</span>
        {bins.map((bin, index) =>
          Number.isInteger(bin.from) ? (
            <span
              className="absolute -translate-x-1/2"
              key={bin.from}
              style={{ left: `${(index / binCount) * 100}%` }}
            >
              {bin.from}
            </span>
          ) : null
        )}
      </div>
    </Plot>
  </>
);

const bands = [
  {
    name: "Below 20 °C",
    mark: "bg-chart-4",
    test: (value: number) => value < 20,
  },
  {
    name: "20 to 22 °C",
    mark: "bg-chart-1",
    test: (value: number) => value >= 20 && value <= 22,
  },
  {
    name: "Above 22 °C",
    mark: "bg-chart-5",
    test: (value: number) => value > 22,
  },
].map((band) => {
  const count = hours.filter(band.test).length;
  return { ...band, count, share: count / known };
});
const [, inRange] = bands;
const percent = (share: number): string => `${Math.round(share * 100)}%`;

/** Bands: the week split into three named ranges, as one bar and a row per range. */
const Bands = () => (
  <>
    <Answer value={percent(inRange.share)}>
      of the last 7 days between 20 and 22 °C
    </Answer>
    <Plot
      label="Share of time in each living room temperature range"
      summary={bands.map(
        (band) => `${band.name}: ${band.count} hours, ${percent(band.share)}.`
      )}
      className="text-label"
    >
      <div className="flex h-3.5 gap-0.5">
        {bands.map((band) => (
          <span
            className={`${band.mark} min-w-0.5 rounded-xs`}
            key={band.name}
            style={{ flexGrow: band.count, flexBasis: 0 }}
          />
        ))}
      </div>
      <div className="mt-3 grid grid-cols-[auto_minmax(0,1fr)_auto_3rem] items-center gap-x-2.5 gap-y-1.5">
        {bands.map((band) => (
          <div className="contents" key={band.name}>
            <span className={`${band.mark} size-2.5 rounded-xs`} />
            <span>{band.name}</span>
            <span className="text-muted-foreground text-right">
              {band.count} h
            </span>
            <span className="text-right">{percent(band.share)}</span>
          </div>
        ))}
      </div>
    </Plot>
  </>
);

/**
 * Prototype. How long a reading spent at each value, to answer "how much of
 * the week was it in range?". Time order is dropped on purpose.
 */
export const Distribution = ({
  take,
}: {
  /** `histogram` shows the shape in half-degree bins; `bands` splits time into three named ranges. */
  take: "histogram" | "bands";
}) => (
  <ChartCard
    title="Living room temperature"
    unit="Last 7 days"
    sources={[source(380, "Living room temperature")]}
    method={
      take === "histogram"
        ? "Each bar counts the local hours whose average fell in that half-degree. Hours without samples are left out."
        : "Each range counts the local hours whose average fell inside it. Hours without samples are left out of the total."
    }
  >
    {take === "histogram" ? <Histogram /> : <Bands />}
  </ChartCard>
);
