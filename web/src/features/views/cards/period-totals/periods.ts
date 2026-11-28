import type {
  PeriodTotal,
  PeriodTotalsData,
  WeekPatternData,
} from "../../bindings/types";

/** Seven local days of on-time, summed from the week's hourly seconds. A day with no known hour has no history. */
export const dayTotals = (week: WeekPatternData): PeriodTotalsData => ({
  from: week.from,
  to: week.to,
  periods: week.days.map((day) => {
    const known = day.hours.filter((cell) => cell.seconds !== null);
    const end = day.hours.at(-1)?.end ?? day.start;
    return {
      start: day.start,
      end,
      seconds: known.length
        ? known.reduce((sum, cell) => sum + (cell.seconds ?? 0), 0)
        : null,
      partial: end > week.to,
      gaps:
        known.length > 0 &&
        day.hours.some((cell) => cell.seconds === null && !cell.future),
    };
  }),
});

const withHistory = (
  periods: PeriodTotal[]
): (PeriodTotal & { seconds: number })[] =>
  periods.flatMap((period) =>
    period.seconds === null ? [] : [{ ...period, seconds: period.seconds }]
  );

export interface PeriodSummary {
  /** The period still being counted, when it has history. */
  current: { seconds: number } | null;
  /** The mean over finished periods with history; null when there are none. */
  average: number | null;
  averageCount: number;
  /** The finished or current period with the most on-time; null when none has history. */
  longest: PeriodTotal | null;
  /** The longest period's seconds, never less than 1 so a bar scale is defined. */
  peak: number;
}

export const summarize = (periods: PeriodTotal[]): PeriodSummary => {
  const known = withHistory(periods);
  const finished = known.filter((period) => !period.partial);
  const current = known.find((period) => period.partial);
  let longest: PeriodTotal | null = null;
  for (const period of known) {
    if (!longest || period.seconds > (longest.seconds ?? 0)) {
      longest = period;
    }
  }
  return {
    current: current ? { seconds: current.seconds } : null,
    average: finished.length
      ? finished.reduce((sum, period) => sum + period.seconds, 0) /
        finished.length
      : null,
    averageCount: finished.length,
    longest,
    peak: Math.max(1, ...known.map((period) => period.seconds)),
  };
};

/** A short duration for a chart label: 2h 10m, 45m, under 1m. */
export const compactDuration = (seconds: number): string => {
  if (seconds < 30) {
    return seconds === 0 ? "0m" : "<1m";
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m`;
  }
  const rest = minutes % 60;
  return `${Math.floor(minutes / 60)}h${rest ? ` ${rest}m` : ""}`;
};

/** A duration short enough to sit over a narrow bar: 3h30, 2h, 45m. */
export const barDuration = (seconds: number): string =>
  compactDuration(seconds).replace(/h (?<minutes>\d+)m/u, "h$<minutes>");
