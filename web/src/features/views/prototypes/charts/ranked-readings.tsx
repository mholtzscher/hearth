import { niceTicks } from "../../cards/time-series/scale";
import { StatusDot } from "../../controls/status-dot";
import { plugPower, roomTemperatures } from "./fixtures";
import type { RankedReading } from "./fixtures";
import { Answer, ChartCard, Key, Plot, source } from "./parts";

const rowClass =
  "grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)_3.25rem] items-center gap-x-3 @max-[420px]:grid-cols-[minmax(0,1fr)_auto]";
const trackClass = "relative @max-[420px]:order-3 @max-[420px]:col-span-2";
const valueClass = "text-right data-[dimmed]:text-gray-8 whitespace-nowrap";

const Name = ({ reading }: { reading: RankedReading }) => (
  <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
    <span className="[overflow-wrap:anywhere]">{reading.label}</span>
    {reading.unavailable && (
      <span className="text-warning inline-flex items-baseline gap-1.5">
        <StatusDot tone="warn" />
        Unavailable
      </span>
    )}
  </span>
);

const number = new Intl.NumberFormat();

/** Bars from zero: honest for quantities where zero means none, such as power. */
const Bars = () => {
  const rows = plugPower.toSorted((a, b) => b.value - a.value);
  const peak = rows[0].value;
  const total = rows.reduce((sum, row) => sum + row.value, 0);
  return (
    <>
      <Answer value={`${number.format(peak)} W`}>
        {rows[0].label} draws the most of {number.format(total)} W
      </Answer>
      <Plot
        label="Power by plug, highest first"
        summary={rows.map(
          (row) =>
            `${row.label}: ${number.format(row.value)} W${row.unavailable ? ", unavailable, last reading" : ""}.`
        )}
        className="text-label grid gap-y-2.5"
      >
        {rows.map((row) => (
          <div className={rowClass} key={row.label}>
            <Name reading={row} />
            <span className={`${trackClass} h-2.5`}>
              <span
                className="bg-chart-1 data-[dimmed]:border-gray-6 data-[dimmed]:bg-card block h-full min-w-px rounded-xs data-[dimmed]:border"
                data-dimmed={row.unavailable || undefined}
                style={{ width: `${(row.value / peak) * 100}%` }}
              />
            </span>
            <span
              className={valueClass}
              data-dimmed={row.unavailable || undefined}
            >
              {number.format(row.value)}
            </span>
          </div>
        ))}
      </Plot>
    </>
  );
};

/** Range plot: a dot on a shared scale, for readings where a bar from zero would mislead. */
const Range = () => {
  const rows = roomTemperatures.toSorted((a, b) => b.value - a.value);
  const lows = rows.map((row) => row.low ?? row.value);
  const highs = rows.map((row) => row.high ?? row.value);
  const { domain, ticks } = niceTicks(Math.min(...lows), Math.max(...highs), 6);
  const x = (value: number) =>
    `${((value - domain[0]) / (domain[1] - domain[0])) * 100}%`;
  const [warmest] = rows;
  const coolest = rows.at(-1) ?? warmest;
  return (
    <>
      <Answer value={`${warmest.value.toFixed(1)} °C`}>
        {warmest.label} is warmest · {coolest.label} is coolest at{" "}
        {coolest.value.toFixed(1)} °C
      </Answer>
      <Key
        className="mb-3"
        items={[
          { label: "Now", mark: "bg-chart-1 size-2.5 rounded-full" },
          { label: "Last 24 hours", mark: "bg-gray-5 h-1 w-5 rounded-full" },
        ]}
      />
      <Plot
        label="Temperature by room, warmest first"
        summary={rows.map(
          (row) =>
            `${row.label}: ${row.value.toFixed(1)} °C, ${row.low?.toFixed(1)} to ${row.high?.toFixed(1)} over the last 24 hours.`
        )}
        className="text-label grid"
      >
        {rows.map((row) => (
          <div className={rowClass} key={row.label}>
            <Name reading={row} />
            <span className={`${trackClass} h-8`}>
              {ticks.map((tick) => (
                <span
                  className="bg-gray-3 absolute inset-y-0 w-px"
                  key={tick}
                  style={{ left: x(tick) }}
                />
              ))}
              <span
                className="bg-gray-5 absolute top-1/2 h-1 -translate-y-1/2 rounded-full"
                style={{
                  left: x(row.low ?? row.value),
                  right: `calc(100% - ${x(row.high ?? row.value)})`,
                }}
              />
              <span
                className="bg-chart-1 ring-card absolute top-1/2 size-2.5 -translate-1/2 rounded-full ring-2"
                style={{ left: x(row.value) }}
              />
            </span>
            <span className={valueClass}>{row.value.toFixed(1)}</span>
          </div>
        ))}
        <div className={`${rowClass} text-caption text-muted-foreground mt-1`}>
          <span />
          <span className={`${trackClass} h-5`}>
            {ticks.map((tick) => (
              <span
                className="absolute -translate-x-1/2"
                key={tick}
                style={{ left: x(tick) }}
              >
                {tick}
              </span>
            ))}
          </span>
          <span />
        </div>
      </Plot>
    </>
  );
};

/**
 * Prototype. The current reading of several Entities, sorted, to answer
 * "which one is highest?" without reading every number.
 */
export const RankedReadings = ({
  take,
}: {
  /** `bars` grow from zero; `range` places a dot and the day's range on a shared scale. */
  take: "bars" | "range";
}) =>
  take === "bars" ? (
    <ChartCard
      title="Plug power"
      unit="W"
      sources={plugPower.map((row, index) => source(320 + index, row.label))}
      method="Each bar is the Entity's current power, drawn from zero and sorted highest first. An unavailable Entity shows its last reading."
    >
      <Bars />
    </ChartCard>
  ) : (
    <ChartCard
      title="Room temperatures"
      unit="°C"
      sources={roomTemperatures.map((row, index) =>
        source(340 + index, row.label)
      )}
      method="Each dot is the Entity's current temperature. The bar behind it spans the minimum and maximum over the last 24 hours."
    >
      <Range />
    </ChartCard>
  );
