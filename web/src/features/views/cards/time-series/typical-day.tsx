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
import type { HistorySpan } from "../../bindings/history";
import { useNumericSpans } from "../../bindings/registry";
import type { NumericSource, Resolved, SeriesData } from "../../bindings/types";
import { DataCard } from "../data-card";
import { useMinuteClock } from "../observation";
import { cardStatus } from "../status";
import { niceTicks, timeLabel } from "./scale";
import {
  dayRows,
  departure,
  localMidnight,
  SLOT_MINUTES,
  todayPoints,
  usualRange,
} from "./typical";
import type { DayRow, Departure, TodayPoint, UsualSlot } from "./typical";

/** Today in five-minute buckets, from local midnight to now. */
const todaySpan: HistorySpan = {
  name: "today",
  range: (to) => ({ from: localMidnight(to), to }),
  bucket: "5m0s",
};
/** The 7 local days before today, in half-hour buckets. */
const usualSpan: HistorySpan = {
  name: "usual-7d",
  range: (to) => ({ from: localMidnight(to, 7), to: localMidnight(to) }),
  bucket: "30m0s",
};

const hourTicks = [0, 6, 12, 18].map((hour) => hour * 60);
const clock = (minute: number): string =>
  timeLabel(new Date(2026, 0, 1, 0, minute).getTime(), {
    weekday: false,
    short: true,
  });

const answerWords = (
  result: Departure,
  format: (value: number) => string
): { value: string; words: string } | undefined => {
  if (result.kind === "none") {
    return undefined;
  }
  if (result.kind === "within") {
    return {
      value: format(result.latest.value),
      words: `within the usual range for ${timeLabel(result.latest.time, { weekday: false })}`,
    };
  }
  return {
    value: `${format(result.gap)} ${result.above ? "above" : "below"}`,
    words: `the usual range since ${timeLabel(result.since, { weekday: false })}`,
  };
};

const summaryWords = (
  latest: TodayPoint | undefined,
  result: Departure,
  usual: (UsualSlot | null)[],
  format: (value: number) => string
): string[] => {
  if (!latest) {
    return ["Today: no samples yet."];
  }
  const slot = usual[Math.floor(latest.minute / SLOT_MINUTES)];
  const sentences = [
    `Today: latest ${format(latest.value)} at ${timeLabel(latest.time, { weekday: false })}.`,
  ];
  if (slot) {
    sentences.push(
      `Usual range at this time over the last 7 days: ${format(slot.low)} to ${format(slot.high)}.`
    );
  }
  if (result.kind === "within") {
    sentences.push("Today is within the usual range.");
  }
  if (result.kind === "outside") {
    sentences.push(
      `Today has been ${result.above ? "above" : "below"} the usual range since ${timeLabel(result.since, { weekday: false })}.`
    );
  }
  return sentences;
};

const TypicalTooltip = ({
  active,
  label,
  rows,
  usual,
  format,
}: {
  active?: boolean;
  label?: string | number;
  rows: DayRow[];
  usual: (UsualSlot | null)[];
  format: (value: number) => string;
}) => {
  const row = rows.find((item) => item.minute === Number(label));
  if (!active || !row) {
    return null;
  }
  const slot = usual[Math.floor(row.minute / SLOT_MINUTES)];
  return (
    <div className="border-gray-4 bg-gray-2 text-label text-foreground grid gap-0.5 rounded-md border px-2.5 py-2">
      <p className="text-muted-foreground">
        {timeLabel(new Date(2026, 0, 1, 0, row.minute).getTime(), {
          weekday: false,
        })}
      </p>
      <p>
        {row.today === null
          ? "Today: no samples"
          : `Today ${format(row.today)}`}
      </p>
      {slot && (
        <p>
          Usually {format(slot.mean)} ({format(slot.low)}–{format(slot.high)})
        </p>
      )}
    </div>
  );
};

const keyClass = "inline-flex items-center gap-2";

const TypicalKey = ({ comparable }: { comparable: boolean }) => (
  <ul className="text-label mb-2 flex list-none flex-wrap gap-x-4 gap-y-1">
    <li className={keyClass}>
      <span className="bg-chart-1 h-0.5 w-4 shrink-0" aria-hidden="true" />
      Today
    </li>
    {comparable && (
      <>
        <li className={keyClass}>
          <span
            className="h-2.5 w-4 shrink-0 rounded-xs bg-[color-mix(in_oklch,var(--chart-6)_32%,var(--gray-1))]"
            aria-hidden="true"
          />
          Usual range, last 7 days
        </li>
        <li className={keyClass}>
          <span className="bg-gray-6 h-px w-4 shrink-0" aria-hidden="true" />
          Usual average
        </li>
      </>
    )}
  </ul>
);

const TypicalPlot = ({
  rows,
  usual,
  digits,
  dimmed,
  format,
}: {
  rows: DayRow[];
  usual: (UsualSlot | null)[];
  digits: number;
  dimmed: boolean;
  format: (value: number) => string;
}) => {
  const values = rows.flatMap((row) => [
    ...(row.today === null ? [] : [row.today]),
    ...(row.usual ?? []),
  ]);
  const y = values.length
    ? niceTicks(Math.min(...values), Math.max(...values), 4)
    : undefined;
  return (
    <ChartContainer className="h-[230px] w-full min-w-0" aria-hidden="true">
      <ComposedChart
        data={rows}
        margin={{ top: 8, right: 16, left: 0, bottom: 0 }}
        accessibilityLayer={false}
      >
        <CartesianGrid vertical={false} stroke="var(--gray-3)" />
        <XAxis
          dataKey="minute"
          type="number"
          domain={[0, 24 * 60]}
          ticks={hourTicks}
          tickFormatter={(value) => clock(Number(value))}
          stroke="var(--gray-8)"
          tickLine={false}
          tickMargin={8}
        />
        <YAxis
          domain={y?.domain ?? ["auto", "auto"]}
          ticks={y?.ticks}
          tickFormatter={(value) => Number(value).toFixed(digits)}
          width={48}
          stroke="var(--gray-8)"
          tickLine={false}
          axisLine={false}
          tickMargin={6}
        />
        <Tooltip
          content={<TypicalTooltip rows={rows} usual={usual} format={format} />}
        />
        <Area
          type="linear"
          dataKey="usual"
          stroke="none"
          fill="var(--chart-6)"
          fillOpacity={0.3}
          connectNulls
          isAnimationActive={false}
        />
        <Line
          type="linear"
          dataKey="typical"
          stroke="var(--gray-6)"
          strokeWidth={1}
          dot={false}
          connectNulls
          isAnimationActive={false}
        />
        <Line
          type="linear"
          dataKey="today"
          stroke="var(--chart-1)"
          strokeWidth={2}
          strokeDasharray={dimmed ? "4 4" : undefined}
          strokeOpacity={dimmed ? 0.6 : 1}
          dot={false}
          connectNulls={false}
          isAnimationActive={false}
        />
      </ComposedChart>
    </ChartContainer>
  );
};

const formatter =
  (data: SeriesData | undefined) =>
  (value: number): string => {
    const unit = data?.format.unit_label;
    return `${value.toFixed(data?.format.fraction_digits ?? 1)}${unit ? ` ${unit}` : ""}`;
  };

/**
 * Today from local midnight against the last 7 days' range for each time of
 * day, to answer "is today unusual?". The axis spans the whole day; today's
 * line ends at its latest sample.
 */
export const TypicalDay = ({
  title,
  label,
  entityId,
  today,
  usual: usualResource,
}: {
  title: string;
  label?: string;
  entityId: string;
  today: Resolved<SeriesData>;
  usual: Resolved<SeriesData>;
}) => {
  const now = useMinuteClock();
  const status = cardStatus(today);
  const { data } = today;
  const format = formatter(data);
  const points = data ? todayPoints(data.buckets) : [];
  const usual = usualResource.data
    ? usualRange(usualResource.data.buckets)
    : [];
  const comparable = usual.some((slot) => slot !== null);
  const result = departure(points, usual);
  const answer = answerWords(result, format);
  const latest = points.at(-1);
  return (
    <DataCard
      title={title}
      unit={data?.format.unit_label}
      status={status.entries}
      arrivals={
        latest
          ? [
              {
                key: entityId,
                updatedAt: new Date(latest.time).toISOString(),
                shown: format(latest.value),
                eligible: status.glows,
              },
            ]
          : []
      }
      sources={[
        {
          entityId,
          label: today.metadata?.label ?? label ?? title,
          availability: today.metadata?.availability,
          enabled: today.metadata?.enabled,
        },
      ]}
      sourceExtra={
        <>
          <p>
            Today&apos;s line averages the samples in each 5-minute interval
            since local midnight. The band spans the lowest and highest of the
            last 7 days&apos; half-hour averages for that time of day; the thin
            line is their mean. Gaps mean no samples.
          </p>
          <p>{localMidnight(now).toLocaleDateString()}</p>
        </>
      }
    >
      {answer && (
        <p className="text-label text-muted-foreground mb-4 flex flex-wrap items-baseline gap-x-2">
          <span className="text-compact-reading text-foreground">
            {answer.value}
          </span>
          <span>{answer.words}</span>
        </p>
      )}
      <TypicalKey comparable={comparable} />
      {data && !comparable && usualResource.status !== "loading" && (
        <p className="text-label text-muted-foreground mb-2">
          Not enough history to compare.
        </p>
      )}
      {data ? (
        <section
          aria-label={`${title}, today against the last 7 days' range for each time of day`}
        >
          <ul className="sr-only">
            {summaryWords(latest, result, usual, format).map((sentence) => (
              <li key={sentence}>{sentence}</li>
            ))}
            {status.value === "dimmed" && <li>Unavailable.</li>}
          </ul>
          <TypicalPlot
            rows={dayRows(points, usual)}
            usual={usual}
            digits={data.format.fraction_digits}
            dimmed={status.value === "dimmed"}
            format={format}
          />
        </section>
      ) : (
        <p className="text-label text-muted-foreground py-6">
          Historical readings will appear here.
        </p>
      )}
    </DataCard>
  );
};

export const BoundTypicalDay = ({
  title,
  series,
}: {
  title: string;
  series: { source: NumericSource; label?: string };
}) => {
  const { entity_id, entity_type, field } = series.source;
  const source = { entity_id, entity_type, field };
  const [today, usual] = useNumericSpans([
    { source, span: todaySpan },
    { source, span: usualSpan },
  ]);
  return (
    <TypicalDay
      title={title}
      label={series.label}
      entityId={entity_id}
      today={today}
      usual={usual}
    />
  );
};
