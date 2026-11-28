import {
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceLine,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { ChartContainer } from "../../../../components/chart";
import {
  niceTicks,
  sameDay,
  timeLabel,
  timeTicks,
} from "../../cards/time-series/scale";
import { clockLabel, NOW, targetHolds, thermostatDay } from "./fixtures";
import type { StepPoint } from "./fixtures";
import { Answer, axis, ChartCard, Key, Plot, source, Tip } from "./parts";

const { from, to, points } = thermostatDay;
const xTicks = timeTicks(from, to);
const xLabels = new Map(xTicks.map((tick) => [tick.value, tick.label]));
const y = niceTicks(
  Math.min(...points.flatMap((point) => [point.target, point.room])),
  Math.max(...points.flatMap((point) => [point.target, point.room])),
  4
);
const current = targetHolds.at(-1) ?? targetHolds[0];
const changeCount = targetHolds.length - 1;
const latestRoom = points.at(-1)?.room ?? 0;

const when = (ms: number): string =>
  timeLabel(ms, { weekday: !sameDay(ms, NOW) });
const summary = [
  `Target: ${current.value} °C since ${when(current.from)}, ${changeCount} changes over the last 24 hours.`,
  ...targetHolds
    .slice(1)
    .map((hold) => `${when(hold.from)}: target set to ${hold.value} °C.`),
  `Room: latest ${latestRoom.toFixed(1)} °C.`,
];

const StepTip = ({
  active,
  label,
}: {
  active?: boolean;
  label?: string | number;
}) => {
  const point = points.find((item) => item.time === Number(label));
  return active && point ? (
    <Tip
      title={when(point.time)}
      rows={[
        `Target ${point.target} °C`,
        `Room avg ${point.room.toFixed(1)} °C`,
      ]}
    />
  ) : null;
};

/** A dot only where the target changed; every other bucket draws nothing. */
const ChangeDot = ({
  cx,
  cy,
  payload,
}: {
  cx?: number;
  cy?: number;
  payload?: StepPoint;
}) =>
  payload?.changed ? (
    <circle
      cx={cx}
      cy={cy}
      r={3}
      fill="var(--chart-1)"
      stroke="var(--card)"
      strokeWidth={1.5}
    />
  ) : null;

const StepPlot = ({ children }: { children: React.ReactNode }) => (
  <ChartContainer className="h-[210px] w-full min-w-0">
    <ComposedChart
      data={points}
      margin={{ top: 16, right: 16, left: 0, bottom: 0 }}
      accessibilityLayer={false}
    >
      <CartesianGrid vertical={false} stroke="var(--gray-3)" />
      <XAxis
        {...axis}
        dataKey="time"
        type="number"
        domain={[from, to]}
        ticks={xTicks.map((tick) => tick.value)}
        tickFormatter={(value) => xLabels.get(Number(value)) ?? ""}
        tickMargin={8}
        interval="preserveStart"
        minTickGap={12}
      />
      <YAxis
        {...axis}
        domain={y.domain}
        ticks={y.ticks}
        width={40}
        axisLine={false}
        tickMargin={6}
      />
      <Tooltip content={<StepTip />} />
      {children}
    </ComposedChart>
  </ChartContainer>
);

/** Risers: one connected step line, with a dot where the target changed. */
const Risers = () => (
  <>
    <Key
      className="mb-2"
      items={[
        { label: "Target", mark: "bg-chart-1 h-0.5 w-4" },
        { label: "Room", mark: "bg-chart-2 h-0.5 w-4" },
      ]}
    />
    <Plot
      label="Thermostat target as a step line, with room temperature"
      summary={summary}
    >
      <StepPlot>
        <Line
          type="linear"
          dataKey="room"
          stroke="var(--chart-2)"
          strokeWidth={2}
          dot={false}
          isAnimationActive={false}
        />
        <Line
          type="stepAfter"
          dataKey="target"
          stroke="var(--chart-1)"
          strokeWidth={2}
          isAnimationActive={false}
          dot={<ChangeDot />}
        />
      </StepPlot>
    </Plot>
  </>
);

/** Holds: each held value is its own labelled bar, with nothing drawn across a jump. */
const Holds = () => (
  <>
    <Key
      className="mb-2"
      items={[
        { label: "Target, held", mark: "bg-chart-1 h-1 w-4" },
        { label: "Room", mark: "bg-gray-7 h-px w-4" },
      ]}
    />
    <Plot
      label="Thermostat target as held values, with room temperature"
      summary={summary}
    >
      <StepPlot>
        <Line
          type="linear"
          dataKey="room"
          stroke="var(--gray-7)"
          strokeWidth={1.25}
          dot={false}
          isAnimationActive={false}
        />
        {targetHolds.map((hold) => (
          <ReferenceLine
            key={hold.from}
            segment={[
              { x: hold.from, y: hold.value },
              { x: hold.to, y: hold.value },
            ]}
            stroke="var(--chart-1)"
            strokeWidth={4}
            label={{
              value: String(hold.value),
              position: "top",
              fill: "var(--gray-10)",
              fontSize: 12,
              offset: 6,
            }}
          />
        ))}
      </StepPlot>
    </Plot>
  </>
);

/**
 * Prototype. A value that holds and then jumps, such as a thermostat target,
 * brightness, or fan level. A sloped line between two samples would claim the
 * value passed through everything in between.
 */
export const SteppedLine = ({
  take,
}: {
  /** `risers` draws one connected step line; `holds` draws each held value alone, labelled. */
  take: "risers" | "holds";
}) => (
  <ChartCard
    title="Hallway thermostat"
    unit="°C"
    sources={[
      source(360, "Hallway thermostat target"),
      source(361, "Hallway temperature"),
    ]}
    method="The target holds its last reported value until the next change. The room line averages the samples in a 5-minute interval."
  >
    <Answer value={`${current.value} °C`}>
      target since {clockLabel(current.from)} · {changeCount} changes in the
      last 24 hours
    </Answer>
    {take === "risers" ? <Risers /> : <Holds />}
  </ChartCard>
);
