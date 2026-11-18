import { useLayoutEffect, useRef, useState } from "react";
import type { z } from "zod";

import type {
  DailyPeriod,
  ForecastData,
  HourlyPeriod,
} from "../../bindings/forecast";
import { useForecast } from "../../bindings/forecast";
import type { Quantity } from "../../bindings/quantity";
import type { ResourceMetadata } from "../../bindings/types";
import { DataCard } from "../data-card";
import { useMinuteClock } from "../observation";
import { Reading } from "../reading";
import { cardStatus } from "../status";
import { conditionWords, ForecastIcon } from "./icons";
import type { forecastSchema } from "./schema";
import { forecastTypes } from "./schema";
import {
  chartLabelStep,
  localDate,
  upcomingDays,
  upcomingHours,
} from "./upcoming";

type Props = z.infer<typeof forecastSchema>;
const temperatureClass =
  "text-compact-reading tracking-[-.02em] text-right group-data-[value=dimmed]/forecast:text-gray-8";
const axisText = "fill-gray-8 text-caption";
// Periods start on the hour in UTC; half-hour and quarter-hour zones keep their minutes.
const hour = (date: Date) =>
  new Intl.DateTimeFormat(
    "en-US",
    date.getMinutes()
      ? { hour: "numeric", minute: "2-digit" }
      : { hour: "numeric" }
  ).format(date);
const weekday = (date: Date) =>
  new Intl.DateTimeFormat("en-US", { weekday: "short" }).format(date);
const dateValue = (date: string) => new Date(`${date}T12:00:00`);

const temperature = (
  value: number | undefined,
  quantity: Quantity | undefined,
  compact = false
): string => {
  if (value === undefined || !quantity) {
    return "—";
  }
  const label =
    compact && (quantity.unit === "Cel" || quantity.unit === "[degF]")
      ? "°"
      : quantity.label;
  return `${value.toFixed(quantity.fraction_digits)}${label}`;
};

const chance = (
  value: number | undefined,
  quantity: Quantity | undefined
): string | undefined =>
  value !== undefined && value >= 10 && quantity
    ? `${value.toFixed(quantity.fraction_digits)}${quantity.label}`
    : undefined;

const dailyLabel = (p: DailyPeriod, now: number): string => {
  const day = p.date === localDate(now) ? "Today" : weekday(dateValue(p.date));
  const details = [
    p.condition && conditionWords[p.condition],
    `low ${temperature(p.temperature_min, p.quantities.temperature_min)}`,
    `high ${temperature(p.temperature_max, p.quantities.temperature_max)}`,
    chance(
      p.precipitation_probability,
      p.quantities.precipitation_probability
    ) &&
      `${chance(p.precipitation_probability, p.quantities.precipitation_probability)} chance of precipitation`,
  ].filter(Boolean);
  return `${day}: ${details.join(", ")}`;
};

const Daily = ({ periods, now }: { periods: DailyPeriod[]; now: number }) => {
  const values = periods.flatMap((p) =>
    [p.temperature_min, p.temperature_max].filter(
      (v): v is number => v !== undefined
    )
  );
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  return (
    <div className="@container">
      <ul className="m-0 grid list-none p-0">
        {periods.map((p) => {
          const probability = chance(
            p.precipitation_probability,
            p.quantities.precipitation_probability
          );
          const barStyle =
            p.temperature_min === undefined || p.temperature_max === undefined
              ? undefined
              : {
                  left: `${((p.temperature_min - min) / range) * 100}%`,
                  right: `${100 - ((p.temperature_max - min) / range) * 100}%`,
                };
          return (
            <li
              key={p.date}
              className="border-border grid min-h-11 grid-cols-[4.2em_28px_3.2em_2.6em_1fr_2.6em] items-center gap-2.5 border-t tabular-nums first:border-t-0 @max-[300px]:grid-cols-[3.4em_24px_2.8em_2.4em_1fr_2.4em] @max-[300px]:gap-1.5"
              aria-label={dailyLabel(p, now)}
            >
              <span
                className="text-foreground group-data-[value=dimmed]/forecast:text-gray-8"
                aria-hidden="true"
              >
                {p.date === localDate(now)
                  ? "Today"
                  : weekday(dateValue(p.date))}
              </span>
              <ForecastIcon condition={p.condition} />
              <span
                className="text-label text-info group-data-[value=dimmed]/forecast:text-gray-8"
                aria-hidden="true"
              >
                {probability}
              </span>
              <span
                data-slot="forecast-temp"
                data-low
                className={`${temperatureClass} text-muted-foreground`}
                aria-hidden="true"
              >
                {temperature(
                  p.temperature_min,
                  p.quantities.temperature_min,
                  true
                )}
              </span>
              <span
                data-slot="forecast-range"
                className="bg-gray-3 relative h-1 rounded-xs"
                aria-hidden="true"
              >
                {barStyle && (
                  <span
                    className="bg-gray-9 group-data-[value=dimmed]/forecast:bg-gray-8 absolute inset-y-0 rounded-xs"
                    style={barStyle}
                  />
                )}
              </span>
              <span
                data-slot="forecast-temp"
                className={temperatureClass}
                aria-hidden="true"
              >
                {temperature(
                  p.temperature_max,
                  p.quantities.temperature_max,
                  true
                )}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
};

const hourlyLabel = (p: HourlyPeriod, index: number): string =>
  [
    index === 0 ? "Now" : hour(new Date(p.starts_at)),
    p.condition && conditionWords[p.condition],
    temperature(p.temperature, p.quantities.temperature),
    chance(
      p.precipitation_probability,
      p.quantities.precipitation_probability
    ) &&
      `${chance(p.precipitation_probability, p.quantities.precipitation_probability)} chance of precipitation`,
  ]
    .filter(Boolean)
    .join(", ");

/** The plot's measured width, so the chart is drawn at its rendered size rather than scaled with it. */
const useMeasuredWidth = () => {
  const ref = useRef<HTMLElement>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element || typeof ResizeObserver === "undefined") {
      return;
    }
    const observer = new ResizeObserver(() => setWidth(element.clientWidth));
    setWidth(element.clientWidth);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return [ref, width] as const;
};

/**
 * The line path for the drawn temperatures: each run starts a new subpath, so a
 * gap in the forecast never draws across it.
 */
const forecastPath = (
  values: (number | undefined)[],
  x: (index: number) => number,
  y: (value: number) => number
): string => {
  const commands: string[] = [];
  let drawing = false;
  for (const [index, value] of values.entries()) {
    if (value === undefined) {
      drawing = false;
      continue;
    }
    commands.push(
      `${drawing ? "L" : "M"}${x(index).toFixed(1)},${y(value).toFixed(1)}`
    );
    drawing = true;
  }
  return commands.join("");
};

const Hourly = ({
  periods,
  count,
}: {
  periods: HourlyPeriod[];
  count: number;
}) => {
  const [plotRef, measured] = useMeasuredWidth();
  const values = periods.map((p) => p.temperature);
  const known = values.filter((v): v is number => v !== undefined);
  const low = Math.min(...known);
  const high = Math.max(...known);
  let peak: HourlyPeriod | undefined;
  for (const p of periods) {
    if (
      p.precipitation_probability !== undefined &&
      (peak?.precipitation_probability === undefined ||
        p.precipitation_probability > peak.precipitation_probability)
    ) {
      peak = p;
    }
  }
  const lowest = periods.find((p) => p.temperature === low);
  const highest = periods.find((p) => p.temperature === high);
  const unit = periods.find((p) => p.temperature !== undefined)?.quantities
    .temperature.label;
  // The summary follows the drawn bars: chance below 10% is not shown, but it is not unavailable either.
  let precipitation = "precipitation chance unavailable";
  if (peak) {
    const peakChance = chance(
      peak.precipitation_probability,
      peak.quantities.precipitation_probability
    );
    precipitation = peakChance
      ? `precipitation chance peaks at ${peakChance} at ${hour(new Date(peak.starts_at))}`
      : "precipitation chance below 10%";
  }
  const summary = `Next ${periods.length} hours: ${known.length ? `low ${temperature(low, lowest?.quantities.temperature)}, high ${temperature(high, highest?.quantities.temperature)}` : "temperature unavailable"}; ${precipitation}.`;
  // Hours keep at least 22px each and scroll below that; wider cards spread the hours while text and icons keep their size.
  const width = Math.max(264, periods.length * 22, Math.floor(measured));
  const step = chartLabelStep(count);
  const x = (i: number) =>
    14 + i * ((width - 28) / Math.max(1, periods.length - 1));
  // Icons occupy y 8–32; the line starts at 58 so a label 8px above the highest point clears them.
  const y = (v: number) => 58 + (1 - (v - low) / (high - low || 1)) * 70;
  const path = forecastPath(values, x, y);
  return (
    <>
      <div
        data-slot="forecast-legend"
        className="text-label text-muted-foreground mb-1.5 flex flex-wrap gap-4"
        aria-hidden="true"
      >
        <span className="before:bg-gray-10 before:mr-1.5 before:inline-block before:h-0.5 before:w-2.5 before:align-middle">
          Temperature{unit && ` ${unit}`}
        </span>
        <span className="before:bg-info before:mr-1.5 before:inline-block before:size-2 before:align-middle before:opacity-55">
          Chance of precipitation
        </span>
      </div>
      <figure
        ref={plotRef}
        className="overflow-x-auto rounded-md"
        aria-label={summary}
      >
        <svg
          data-slot="forecast-chart"
          className="block overflow-visible tabular-nums"
          width={width}
          height={216}
          viewBox={`0 0 ${width} 216`}
          aria-hidden="true"
        >
          <line className="stroke-gray-3" x1="0" x2={width} y1="176" y2="176" />
          {periods.map((p, i) => {
            const date = new Date(p.starts_at);
            const probability = p.precipitation_probability;
            const value = values[i];
            // A new local day starts between this period and the previous one; place the rule at
            // local midnight by time, which in half-hour zones falls between two hourly points.
            const previous =
              i > 0 ? Date.parse(periods[i - 1].starts_at) : undefined;
            const midnight =
              previous !== undefined &&
              localDate(date.getTime()) !== localDate(previous)
                ? x(i - 1) +
                  ((new Date(
                    date.getFullYear(),
                    date.getMonth(),
                    date.getDate()
                  ).getTime() -
                    previous) /
                    (date.getTime() - previous)) *
                    (x(i) - x(i - 1))
                : undefined;
            return (
              <g key={p.starts_at}>
                {midnight !== undefined && (
                  <>
                    <line
                      data-slot="forecast-midnight"
                      className="stroke-gray-5 [stroke-dasharray:2_3]"
                      x1={midnight}
                      x2={midnight}
                      y1="10"
                      y2="176"
                    />
                    <text
                      className={axisText}
                      x={midnight}
                      y="212"
                      textAnchor="middle"
                    >
                      {weekday(date)}
                    </text>
                  </>
                )}
                {probability !== undefined && probability >= 10 && (
                  <rect
                    data-slot="forecast-bar"
                    className="fill-info group-data-[value=dimmed]/forecast:fill-gray-8 opacity-55"
                    x={x(i) - 7}
                    y={176 - (26 * probability) / 100}
                    width="14"
                    height={(26 * probability) / 100}
                    rx="2"
                  />
                )}
                {i % step === 0 && (
                  <>
                    <ForecastIcon condition={p.condition} x={x(i) - 12} y={8} />
                    {value !== undefined && (
                      <text
                        data-slot="forecast-temp-label"
                        className="fill-gray-10 text-label group-data-[value=dimmed]/forecast:fill-gray-8"
                        x={x(i)}
                        y={y(value) - 8}
                        textAnchor="middle"
                      >
                        {temperature(
                          p.temperature,
                          p.quantities.temperature,
                          true
                        )}
                      </text>
                    )}
                    <text
                      className={axisText}
                      x={x(i)}
                      y="192"
                      textAnchor="middle"
                    >
                      {i === 0 ? "Now" : hour(date)}
                    </text>
                  </>
                )}
              </g>
            );
          })}
          <path
            data-slot="forecast-line"
            className="stroke-gray-10 group-data-[value=dimmed]/forecast:stroke-gray-8 fill-none stroke-2"
            d={path}
          />
        </svg>
      </figure>
    </>
  );
};

const forecastCount = (
  periods: number | undefined,
  source: Props["source"]
): number => periods ?? (source.entity_type === forecastTypes.hourly ? 24 : 8);

const forecastTitle = (
  label: string | undefined,
  count: number,
  source: Props["source"]
): string =>
  label ??
  `Next ${count} ${source.entity_type === forecastTypes.hourly ? "hours" : "days"}`;

const forecastHours = (
  data: ForecastData | undefined,
  now: number,
  count: number
): HourlyPeriod[] =>
  data?.kind === "hourly" ? upcomingHours(data.periods, now, count) : [];

const forecastDays = (
  data: ForecastData | undefined,
  now: number,
  count: number
): DailyPeriod[] =>
  data?.kind === "daily" ? upcomingDays(data.periods, now, count) : [];

// Words merge day and night variants, so the afterglow also compares each drawn condition icon.
const shownForecast = (
  data: ForecastData | undefined,
  hours: HourlyPeriod[],
  days: DailyPeriod[],
  now: number
): (string | undefined)[][] =>
  data?.kind === "hourly"
    ? hours.map((p, i) => [hourlyLabel(p, i), p.condition])
    : days.map((p) => [dailyLabel(p, now), p.condition]);

const partialForecast = (
  data: ForecastData | undefined,
  days: DailyPeriod[],
  now: number,
  metadata: ResourceMetadata | undefined
): boolean =>
  data?.kind === "daily" &&
  days.length > 0 &&
  data.periods.some((p) => p.date < localDate(now)) &&
  metadata?.availability === "unavailable";

const ForecastBody = ({
  data,
  hours,
  days,
  count,
  now,
  partial,
}: {
  data: ForecastData | undefined;
  hours: HourlyPeriod[];
  days: DailyPeriod[];
  count: number;
  now: number;
  partial: boolean;
}) => {
  if (!data) {
    return (
      <p aria-hidden="true">
        <Reading as="span" value="empty">
          —
        </Reading>
      </p>
    );
  }
  if (data.kind === "hourly") {
    return hours.length ? (
      <Hourly periods={hours} count={count} />
    ) : (
      <p>Every hour in the last forecast has passed.</p>
    );
  }
  if (days.length === 0) {
    return <p>Every day in the last forecast has passed.</p>;
  }
  return (
    <>
      {partial && (
        <p className="text-muted-foreground mb-2">
          Showing the remaining days of a forecast from{" "}
          {weekday(new Date(data.observed_at))}.
        </p>
      )}
      <Daily periods={days} now={now} />
    </>
  );
};

export const BoundForecast = ({ label, source, periods }: Props) => {
  const resource = useForecast(source);
  const status = cardStatus(resource);
  const now = useMinuteClock();
  const count = forecastCount(periods, source);
  const data = status.value === "empty" ? undefined : resource.data;
  const hours = forecastHours(data, now, count);
  const days = forecastDays(data, now, count);
  const shown = shownForecast(data, hours, days, now);
  const title = forecastTitle(label, count, source);
  const { metadata } = resource;
  const partial = partialForecast(data, days, now, metadata);
  return (
    <DataCard
      title={title}
      status={status.entries}
      observedAt={data?.observed_at}
      arrival={
        data
          ? {
              updatedAt: data.observed_at,
              shown: JSON.stringify(shown),
              eligible: status.glows,
            }
          : undefined
      }
      sources={
        metadata
          ? [
              {
                entityId: metadata.id,
                label: metadata.label,
                availability: metadata.availability,
                enabled: metadata.enabled,
              },
            ]
          : []
      }
      sourceExtra={
        <p>
          Modeled forecast from the bound Entity&apos;s Adapter; periods that
          have ended are hidden.
        </p>
      }
    >
      <div
        data-slot="forecast-body"
        data-value={status.value}
        className="group/forecast"
      >
        <ForecastBody
          data={data}
          hours={hours}
          days={days}
          count={count}
          now={now}
          partial={partial}
        />
      </div>
    </DataCard>
  );
};
