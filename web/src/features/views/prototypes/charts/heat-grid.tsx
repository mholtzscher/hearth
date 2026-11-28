import { niceTicks } from "../../cards/time-series/scale";
import { livingRoomWeek, weekdayLabel } from "./fixtures";
import type { GridDay, GridHour } from "./fixtures";
import { Answer, ChartCard, Plot, source } from "./parts";

const known = livingRoomWeek.flatMap((day) =>
  day.hours.flatMap((cell) => (cell.value === null ? [] : [cell.value]))
);
const mean = (values: number[]): number =>
  values.reduce((sum, value) => sum + value, 0) / values.length;
const scale = niceTicks(Math.min(...known), Math.max(...known), 5);
const step = scale.ticks[1] - scale.ticks[0];
const levels = scale.ticks.length - 1;

/** Which of the scale's equal steps a value falls in, 0 for the lowest. */
const level = (value: number): number =>
  Math.min(levels - 1, Math.floor((value - scale.domain[0]) / step));
/** Five shades at most: a longer scale shares its darkest and lightest. */
const shade = (value: number): number =>
  Math.round((level(value) / Math.max(1, levels - 1)) * 4);

// Light text on the three dark shades, dark text on the two light ones.
const cellClass =
  "block rounded-xs bg-gray-3 text-gray-10 data-[shade='1']:bg-[color-mix(in_oklch,var(--gray-10)_22%,var(--gray-2))] data-[shade='2']:bg-[color-mix(in_oklch,var(--gray-10)_40%,var(--gray-2))] data-[shade='3']:bg-[color-mix(in_oklch,var(--gray-10)_70%,var(--gray-2))] data-[shade='3']:text-gray-0 data-[shade='4']:bg-gray-10 data-[shade='4']:text-gray-0 data-[future]:bg-transparent data-[future]:shadow-[inset_0_0_0_1px_var(--gray-3)] data-[unknown]:bg-transparent data-[unknown]:outline data-[unknown]:outline-dashed data-[unknown]:outline-gray-5 data-[unknown]:outline-offset-[-1px]";

const cellData = (cell: { value: number | null; future: boolean }) => ({
  "data-shade": cell.value === null ? undefined : shade(cell.value),
  "data-future": (cell.value === null && cell.future) || undefined,
  "data-unknown": (cell.value === null && !cell.future) || undefined,
});

const dayMean = (day: GridDay): number | null => {
  const values = day.hours.flatMap((cell) =>
    cell.value === null ? [] : [cell.value]
  );
  return values.length ? mean(values) : null;
};

const hourMeans = Array.from({ length: 24 }, (_, hour) =>
  mean(
    livingRoomWeek.flatMap((day) => {
      const { value } = day.hours[hour];
      return value === null ? [] : [value];
    })
  )
);
const warmestHour = hourMeans.indexOf(Math.max(...hourMeans));
const hourWord = (hour: number): string =>
  new Date(2026, 0, 1, hour).toLocaleTimeString([], { hour: "numeric" });

const summary = [
  ...livingRoomWeek.map((day) => {
    const name = new Date(day.start).toLocaleDateString([], {
      weekday: "long",
    });
    const average = dayMean(day);
    const gaps = day.hours.some((cell) => cell.value === null && !cell.future);
    return average === null
      ? `${name}: no history.`
      : `${name}: average ${average.toFixed(1)} °C.${gaps ? " Some hours have no history." : ""}`;
  }),
  `Warmest around ${hourWord(warmestHour)}, averaging ${hourMeans[warmestHour].toFixed(1)} °C.`,
];

const Legend = () => (
  <div className="text-label text-muted-foreground mt-3.5 flex flex-wrap items-center gap-x-3.5 gap-y-1.5">
    <span>Average °C per hour</span>
    <ol className="flex list-none flex-wrap gap-x-[9px] gap-y-[3px]">
      {[0, 1, 2, 3, 4].map((index) => {
        const members = scale.ticks
          .slice(0, -1)
          .filter((tick) => shade(tick + step / 2) === index);
        if (members.length === 0) {
          return null;
        }
        return (
          <li className="flex items-center gap-[5px]" key={index}>
            <span
              className={`${cellClass} aspect-square w-3`}
              data-shade={index}
              aria-hidden="true"
            />
            {members[0]}–{(members.at(-1) ?? members[0]) + step}
          </li>
        );
      })}
    </ol>
  </div>
);

const gridClass =
  "text-label grid grid-cols-[2.4rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-[3px] @max-[420px]:grid-cols-[2.2rem_minmax(0,1fr)]";
const averageClass =
  "text-right tabular-nums whitespace-nowrap @max-[420px]:hidden";

/** Hours: one shaded cell per hour, read through a five-step key. */
const Hours = () => (
  <>
    <Plot
      label="Living room temperature by day and hour"
      summary={summary}
      className={gridClass}
    >
      <span />
      <div className="text-muted-foreground grid grid-cols-4">
        <span>12 AM</span>
        <span>6 AM</span>
        <span>12 PM</span>
        <span>6 PM</span>
      </div>
      <span className={`${averageClass} text-muted-foreground`}>Avg</span>
      {livingRoomWeek.map((day) => {
        const average = dayMean(day);
        return (
          <div className="contents" key={day.start}>
            <span className="text-muted-foreground">
              {weekdayLabel(day.start)}
            </span>
            <div className="grid grid-cols-[repeat(24,minmax(0,1fr))] gap-0.5 @max-[420px]:gap-px">
              {day.hours.map((cell, hour) => (
                <span
                  key={hour}
                  className={`${cellClass} aspect-[1/1.15]`}
                  {...cellData(cell)}
                />
              ))}
            </div>
            <span className={averageClass}>
              {average === null ? "—" : average.toFixed(1)}
            </span>
          </div>
        );
      })}
    </Plot>
    <Legend />
  </>
);

/** Three-hour blocks of one day; a block with no known hour keeps its reason. */
const blocks = (day: GridDay): GridHour[] =>
  Array.from({ length: 8 }, (_, block) => {
    const hours = day.hours.slice(block * 3, block * 3 + 3);
    const values = hours.flatMap((cell) =>
      cell.value === null ? [] : [cell.value]
    );
    return {
      value: values.length ? mean(values) : null,
      future: hours.every((cell) => cell.future),
    };
  });

/** Blocks: three-hour cells large enough to print the value, so no key is needed. */
const Blocks = () => (
  <Plot
    label="Living room temperature by day and three-hour block"
    summary={summary}
    className={gridClass}
  >
    <span />
    <div className="text-caption text-muted-foreground grid grid-cols-8 gap-0.5">
      {[0, 3, 6, 9, 12, 15, 18, 21].map((hour) => (
        <span key={hour}>
          {hourWord(hour).replace(/\s?[AP]M/u, "")}
          <span className="@max-[420px]:hidden">
            {hour % 12 === 0 ? ` ${hour < 12 ? "AM" : "PM"}` : ""}
          </span>
        </span>
      ))}
    </div>
    <span className={`${averageClass} text-muted-foreground`}>Avg</span>
    {livingRoomWeek.map((day) => {
      const average = dayMean(day);
      return (
        <div className="contents" key={day.start}>
          <span className="text-muted-foreground">
            {weekdayLabel(day.start)}
          </span>
          <div className="text-caption grid grid-cols-8 gap-0.5">
            {blocks(day).map((cell, block) => (
              <span
                key={block}
                className={`${cellClass} h-7 text-center leading-7 font-medium tabular-nums`}
                {...cellData(cell)}
              >
                {cell.value !== null && (
                  <>
                    <span className="@max-[420px]:hidden">
                      {cell.value.toFixed(1)}
                    </span>
                    <span className="@min-[421px]:hidden">
                      {Math.round(cell.value)}
                    </span>
                  </>
                )}
              </span>
            ))}
          </div>
          <span className={averageClass}>
            {average === null ? "—" : average.toFixed(1)}
          </span>
        </div>
      );
    })}
  </Plot>
);

/**
 * Prototype. A numeric reading by day and time of day, to answer "when is it
 * usually warm?". The numeric sibling of WeekPattern, which shades only how
 * long a yes/no field was on.
 */
export const HeatGrid = ({
  take,
}: {
  /** `hours` shades one cell per hour; `blocks` prints the value in three-hour cells. */
  take: "hours" | "blocks";
}) => (
  <ChartCard
    title="Living room temperature"
    unit="Last 7 days"
    sources={[source(380, "Living room temperature")]}
    method={
      take === "hours"
        ? "Each cell averages the samples in one local hour. A dashed cell has no samples; an outlined cell is still to come."
        : "Each cell averages the samples in three local hours. A dashed cell has no samples; an outlined cell is still to come."
    }
  >
    <Answer value={`${mean(known).toFixed(1)} °C`}>
      average this week · warmest around {hourWord(warmestHour)}
    </Answer>
    {take === "hours" ? <Hours /> : <Blocks />}
  </ChartCard>
);
