import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { ChartContainer } from "../../../../components/chart";
import { niceTicks, timeLabel } from "../../cards/time-series/scale";
import { clockLabel, dayStart, nowSlot, outdoorDay } from "./fixtures";
import type { ComparisonSlot } from "./fixtures";
import { Answer, axis, ChartCard, Key, Plot, source, Tip } from "./parts";

const hour = 3_600_000;
const from = dayStart(0);
const to = from + 24 * hour;
const xTicks = [0, 6, 12, 18].map((h) => from + h * hour);
const latest = outdoorDay[nowSlot];
const latestToday = latest.today ?? 0;

const degrees = (value: number): string => `${value.toFixed(1)} °C`;

const DayTip = ({
  active,
  label,
  rows,
}: {
  active?: boolean;
  label?: string | number;
  rows: (slot: ComparisonSlot) => string[];
}) => {
  const slot = outdoorDay.find((item) => item.time === Number(label));
  return active && slot ? (
    <Tip title={clockLabel(slot.time)} rows={rows(slot)} />
  ) : null;
};

const DayPlot = ({
  values,
  tooltip,
  children,
}: {
  values: number[];
  tooltip: (slot: ComparisonSlot) => string[];
  children: React.ReactNode;
}) => {
  const y = niceTicks(Math.min(...values), Math.max(...values), 4);
  return (
    <ChartContainer className="h-[210px] w-full min-w-0">
      <ComposedChart
        data={outdoorDay}
        margin={{ top: 8, right: 16, left: 0, bottom: 0 }}
        accessibilityLayer={false}
      >
        <CartesianGrid vertical={false} stroke="var(--gray-3)" />
        <XAxis
          {...axis}
          dataKey="time"
          type="number"
          domain={[from, to]}
          ticks={xTicks}
          tickFormatter={(value) =>
            timeLabel(Number(value), { weekday: false, short: true })
          }
          tickMargin={8}
        />
        <YAxis
          {...axis}
          domain={y.domain}
          ticks={y.ticks}
          width={40}
          axisLine={false}
          tickMargin={6}
        />
        <Tooltip content={<DayTip rows={tooltip} />} />
        {children}
      </ComposedChart>
    </ChartContainer>
  );
};

const todayLine = (
  <Line
    type="linear"
    dataKey="today"
    stroke="var(--chart-1)"
    strokeWidth={2}
    dot={false}
    connectNulls={false}
    isAnimationActive={false}
  />
);

/** Today over yesterday: two lines on one time-of-day axis. */
const Yesterday = () => {
  const difference = latestToday - latest.yesterday;
  return (
    <>
      <Answer
        value={`${Math.abs(difference).toFixed(1)} °C ${difference >= 0 ? "warmer" : "cooler"}`}
      >
        than yesterday at {clockLabel(latest.time)}
      </Answer>
      <Key
        className="mb-2"
        items={[
          { label: "Today", mark: "bg-chart-1 h-0.5 w-4" },
          { label: "Yesterday", mark: "bg-chart-6 h-px w-4" },
        ]}
      />
      <Plot
        label="Outdoor temperature, today over yesterday"
        summary={[
          `Today: latest ${degrees(latestToday)} at ${clockLabel(latest.time)}.`,
          `Yesterday at the same time: ${degrees(latest.yesterday)}.`,
        ]}
      >
        <DayPlot
          values={outdoorDay.flatMap((slot) =>
            slot.today === null
              ? [slot.yesterday]
              : [slot.today, slot.yesterday]
          )}
          tooltip={(slot) => [
            slot.today === null
              ? "Today: not yet"
              : `Today ${degrees(slot.today)}`,
            `Yesterday ${degrees(slot.yesterday)}`,
          ]}
        >
          <Line
            type="linear"
            dataKey="yesterday"
            stroke="var(--chart-6)"
            strokeWidth={1.25}
            dot={false}
            isAnimationActive={false}
          />
          {todayLine}
        </DayPlot>
      </Plot>
    </>
  );
};

/** 1 above the usual range, -1 below it, 0 inside it or not yet sampled. */
const outside = (slot: ComparisonSlot): number =>
  slot.today === null
    ? 0
    : Math.sign(
        Math.max(0, slot.today - slot.usual[1]) -
          Math.max(0, slot.usual[0] - slot.today)
      );

/** The first slot of the run, ending now, in which today sits outside the usual range. */
const departure = (): { since: number; above: boolean } | undefined => {
  const side = outside(latest);
  if (side === 0) {
    return undefined;
  }
  let index = nowSlot;
  while (index > 0 && outside(outdoorDay[index - 1]) === side) {
    index -= 1;
  }
  return { since: outdoorDay[index].time, above: side > 0 };
};

/** Today against typical: the last 7 days' range at each time of day, with today on top. */
const Typical = () => {
  const run = departure();
  const gap = run?.above
    ? latestToday - latest.usual[1]
    : latest.usual[0] - latestToday;
  return (
    <>
      {run ? (
        <Answer value={`${gap.toFixed(1)} °C ${run.above ? "above" : "below"}`}>
          the usual range since {clockLabel(run.since)}
        </Answer>
      ) : (
        <Answer value={degrees(latestToday)}>
          within the usual range for {clockLabel(latest.time)}
        </Answer>
      )}
      <Key
        className="mb-2"
        items={[
          { label: "Today", mark: "bg-chart-1 h-0.5 w-4" },
          {
            label: "Usual range, last 7 days",
            mark: "h-2.5 w-4 rounded-xs bg-[color-mix(in_oklch,var(--chart-6)_32%,var(--gray-1))]",
          },
          { label: "Usual average", mark: "bg-gray-6 h-px w-4" },
        ]}
      />
      <Plot
        label="Outdoor temperature, today against the last 7 days"
        summary={[
          `Today: latest ${degrees(latestToday)} at ${clockLabel(latest.time)}.`,
          `Usual range at this time over the last 7 days: ${degrees(latest.usual[0])} to ${degrees(latest.usual[1])}.`,
          run
            ? `Today has been ${run.above ? "above" : "below"} the usual range since ${clockLabel(run.since)}.`
            : "Today is within the usual range.",
        ]}
      >
        <DayPlot
          values={outdoorDay.flatMap((slot) => [
            ...slot.usual,
            ...(slot.today === null ? [] : [slot.today]),
          ])}
          tooltip={(slot) => [
            slot.today === null
              ? "Today: not yet"
              : `Today ${degrees(slot.today)}`,
            `Usually ${degrees(slot.typical)} (${slot.usual[0].toFixed(1)}–${slot.usual[1].toFixed(1)})`,
          ]}
        >
          <Area
            type="linear"
            dataKey="usual"
            stroke="none"
            fill="var(--chart-6)"
            fillOpacity={0.3}
            isAnimationActive={false}
          />
          <Line
            type="linear"
            dataKey="typical"
            stroke="var(--gray-6)"
            strokeWidth={1}
            dot={false}
            isAnimationActive={false}
          />
          {todayLine}
        </DayPlot>
      </Plot>
    </>
  );
};

/**
 * Prototype. Today drawn against an earlier period on one time-of-day axis, to
 * answer "is today unusual?". The axis spans the whole day; today's line ends
 * at the latest reading.
 */
export const PeriodComparison = ({
  take,
}: {
  /** `yesterday` overlays one earlier day; `typical` shows the last 7 days' range. */
  take: "yesterday" | "typical";
}) => (
  <ChartCard
    title="Outdoor temperature"
    unit="°C"
    sources={[source(101, "Outdoor temperature")]}
    method={
      take === "yesterday"
        ? "Each point averages the samples in a 15-minute interval. Yesterday's line is the same Entity one day earlier, at the same local time."
        : "Each point averages the samples in a 15-minute interval. The band spans the lowest and highest of the last 7 days' averages for that time of day; the thin line is their mean."
    }
  >
    {take === "yesterday" ? <Yesterday /> : <Typical />}
  </ChartCard>
);
