import { dailyEnergy, weekdayLabel } from "./fixtures";
import type { PeriodTotal } from "./fixtures";
import { Answer, ChartCard, Plot, source } from "./parts";

const complete = dailyEnergy.flatMap((day) =>
  day.total === null || day.partial ? [] : [day.total]
);
const average =
  complete.reduce((sum, value) => sum + value, 0) / complete.length;
const peak = Math.max(...complete);
const today = dailyEnergy.at(-1)?.total ?? 0;

const sentence = (day: PeriodTotal): string => {
  const name = new Date(day.start).toLocaleDateString([], { weekday: "long" });
  if (day.total === null) {
    return `${name}: no history.`;
  }
  return `${name}: ${day.total.toFixed(1)} kWh${day.partial ? " so far" : ""}.`;
};
const summary = [
  ...dailyEnergy.map(sentence),
  `Daily average ${average.toFixed(1)} kWh over ${complete.length} complete days.`,
];

// A partial period is tinted and outlined: it is a total still being counted.
const barClass =
  "bg-chart-1 data-[partial]:border data-[partial]:border-chart-1 data-[partial]:bg-[color-mix(in_oklch,var(--chart-1)_28%,var(--gray-1))]";

/** Columns: one bar per day, each labelled with its total, so the plot needs no value axis. */
const Columns = () => (
  <Plot label="Energy used per day, bars" summary={summary}>
    <div className="relative pe-10">
      <div className="border-gray-4 grid h-44 grid-cols-7 items-end gap-x-2 border-b">
        {dailyEnergy.map((day) => (
          <div
            className="flex h-full flex-col items-center justify-end gap-1"
            key={day.start}
          >
            {day.total === null ? (
              <span className="text-caption text-gray-6 pb-1">—</span>
            ) : (
              <>
                <span className="text-caption text-foreground bg-card relative z-10 px-0.5 leading-tight">
                  {day.total.toFixed(1)}
                </span>
                <span
                  className={`${barClass} w-full max-w-8 rounded-t-xs`}
                  data-partial={day.partial || undefined}
                  style={{ height: `${(day.total / peak) * 84}%` }}
                />
              </>
            )}
          </div>
        ))}
      </div>
      {/* The average's label sits in a gutter, so it never covers a bar's own label. */}
      <span
        className="border-gray-6 absolute inset-x-0 border-t"
        style={{ bottom: `${(average / peak) * 84}%` }}
      >
        <span className="text-caption text-muted-foreground bg-card absolute top-0 right-0 w-8 -translate-y-1/2 text-right leading-tight">
          avg
          <br />
          {average.toFixed(1)}
        </span>
      </span>
    </div>
    <div className="text-caption text-muted-foreground mt-2 grid grid-cols-7 gap-x-2 pe-10 text-center">
      {dailyEnergy.map((day) => (
        <span key={day.start}>
          {weekdayLabel(day.start)}
          {day.partial && <span className="block">so far</span>}
          {day.total === null && <span className="block">No history</span>}
        </span>
      ))}
    </div>
  </Plot>
);

/** Ledger: one row per day, read like a table, with the average as a rule down the rows. */
const Ledger = () => (
  <Plot
    label="Energy used per day, rows"
    summary={summary}
    className="text-label grid grid-cols-[2.75rem_minmax(0,1fr)_auto] items-center gap-x-3"
  >
    <span />
    <span className="text-caption text-muted-foreground relative h-5">
      <span
        className="absolute -translate-x-1/2 whitespace-nowrap"
        style={{ left: `${(average / peak) * 100}%` }}
      >
        avg {average.toFixed(1)}
      </span>
    </span>
    <span />
    {dailyEnergy.map((day) => (
      <div className="contents" key={day.start}>
        <span className="text-muted-foreground">{weekdayLabel(day.start)}</span>
        <span className="relative flex h-7 items-center">
          <span
            className="bg-gray-5 absolute inset-y-0 w-px"
            style={{ left: `${(average / peak) * 100}%` }}
          />
          {day.total === null ? (
            <span className="text-muted-foreground bg-card relative pe-2">
              No history
            </span>
          ) : (
            <span
              className={`${barClass} relative h-2.5 rounded-xs`}
              data-partial={day.partial || undefined}
              style={{ width: `${(day.total / peak) * 100}%` }}
            />
          )}
        </span>
        <span className="text-right whitespace-nowrap">
          {day.partial && (
            <span className="text-muted-foreground">so far </span>
          )}
          {day.total === null ? (
            <span className="text-gray-6">—</span>
          ) : (
            day.total.toFixed(1)
          )}
        </span>
      </div>
    ))}
  </Plot>
);

/**
 * Prototype. A total per period: energy used, rain fallen, hours a heater ran.
 * A line would imply the total moved smoothly between days; bars do not.
 */
export const PeriodBars = ({
  take,
}: {
  /** `columns` labels each bar and drops the value axis; `ledger` reads as rows. */
  take: "columns" | "ledger";
}) => (
  <ChartCard
    title="Energy used"
    unit="kWh per day"
    sources={[source(301, "Whole-home energy")]}
    method="Each bar is the meter's increase between local midnights. Today counts up to the latest reading. A day without readings at both ends has no total."
  >
    <Answer value={`${today.toFixed(1)} kWh`}>
      today so far · daily average {average.toFixed(1)} kWh
    </Answer>
    {take === "columns" ? <Columns /> : <Ledger />}
  </ChartCard>
);
