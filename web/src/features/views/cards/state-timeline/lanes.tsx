import type { CSSProperties } from "react";

import { cn } from "../../../../lib/utils";
import { durationWords } from "../../bindings/changes";
import type { Resolved, TextData, TimelineData } from "../../bindings/types";
import { StatusList } from "../data-card";
import { cardStatus } from "../status";
import { timeLabel, timeTicks } from "../time-series/scale";

export interface TimelineRow {
  key: string;
  label: string;
  entityId: string;
  trueWord?: string;
  resource: Resolved<{ current?: TextData; timeline?: TimelineData }>;
}

export const totalsText = (
  data?: TimelineData,
  trueWord = "active"
): string => {
  if (!data) {
    return "No history";
  }
  const boolean = data.totals.some((total) => total.truthy !== null);
  const totals = boolean
    ? data.totals.filter((total) => total.truthy)
    : data.totals;
  if (!totals.length) {
    return boolean ? `Never ${trueWord.toLowerCase()}` : "No recorded values";
  }
  // Counts belong to yes/no fields ("Open 43 min · 7×"); text values show time per value.
  return totals
    .map(
      (total) =>
        `${total.text} ${durationWords(total.seconds)}${boolean && total.entries ? ` · ${total.entries}×` : ""}`
    )
    .join(" · ");
};

export const sinceText = (row: TimelineRow): string | undefined => {
  const timeline = row.resource.data?.timeline;
  const current = row.resource.data?.current;
  const last = timeline?.intervals.at(-1);
  // A value held since before the window has no change time inside it to name.
  return last?.kind === "value" &&
    timeline &&
    last.from > timeline.from &&
    current?.text === last.text
    ? `since ${timeLabel(last.from, { weekday: false })}`
    : undefined;
};

export const Axis = ({
  data,
  className,
}: {
  data?: TimelineData;
  className?: string;
}) => {
  if (!data) {
    return null;
  }
  return (
    <div
      data-slot="timeline-axis"
      className={cn(
        "text-muted-foreground relative h-5 text-[13px] tabular-nums",
        className
      )}
      aria-hidden="true"
    >
      {timeTicks(data.from, data.to)
        .filter(
          (tick) =>
            tick.value > data.from &&
            tick.value < data.to - 0.12 * (data.to - data.from)
        )
        .map((tick) => (
          <span
            className="absolute -translate-x-1/2 whitespace-nowrap"
            key={tick.value}
            style={{
              left: `${(100 * (tick.value - data.from)) / (data.to - data.from)}%`,
            }}
          >
            {tick.label}
          </span>
        ))}
      <span className="absolute right-0 whitespace-nowrap">Now</span>
    </div>
  );
};

const trackClass =
  "group/track relative h-[18px] min-w-0 before:absolute before:inset-x-0 before:top-2 before:h-0.5 before:bg-gray-4 before:content-[''] data-[dimmed]:before:h-0 data-[dimmed]:before:border-t-2 data-[dimmed]:before:border-dashed data-[dimmed]:before:border-gray-5 data-[dimmed]:before:bg-transparent data-[compact]:col-span-full data-[compact]:mt-2 data-[compact]:h-1.5 data-[compact]:rounded-[3px] data-[compact]:bg-gray-3 data-[compact]:before:hidden data-[compact]:data-[dimmed]:border-t-2 data-[compact]:data-[dimmed]:border-dashed data-[compact]:data-[dimmed]:border-gray-5 data-[compact]:data-[dimmed]:bg-transparent";

export const Track = ({
  data,
  color,
  dimmed,
  compact = false,
  className,
}: {
  data?: TimelineData;
  color: string;
  dimmed: boolean;
  compact?: boolean;
  className?: string;
}) => {
  if (!data) {
    return (
      <div
        data-slot="timeline-track"
        className={cn(trackClass, className)}
        data-dimmed={dimmed || undefined}
        aria-hidden="true"
      />
    );
  }
  const ranked = [...data.totals]
    .filter((total) => total.truthy === null)
    .toSorted((a, b) => b.seconds - a.seconds);
  const ranks = new Map(ranked.map((total, index) => [total.text, index]));
  return (
    <div
      data-slot="timeline-track"
      data-compact={compact || undefined}
      className={cn(trackClass, className)}
      data-dimmed={dimmed || undefined}
      style={{ "--timeline-color": color } as CSSProperties}
      aria-hidden="true"
    >
      {data.intervals.map((interval, index) => {
        if (
          interval.kind === "absent" ||
          (interval.kind === "value" && interval.truthy === false)
        ) {
          return null;
        }
        const rank =
          interval.kind === "value" && interval.truthy === null
            ? (ranks.get(interval.text) ?? 0)
            : 0;
        const text = interval.kind === "unknown" ? "No history" : interval.text;
        return (
          <span
            key={index}
            data-slot="timeline-segment"
            className="data-[unknown]:border-gray-5 data-[unknown]:bg-card absolute inset-y-0 min-w-0.5 overflow-hidden rounded-[2px] bg-(--timeline-color) group-data-[dimmed]/track:opacity-60 data-[rank='1']:bg-[color-mix(in_oklch,var(--timeline-color)_32%,var(--gray-1))] data-[rank='2']:bg-[color-mix(in_oklch,var(--timeline-color)_16%,var(--gray-1))] data-[unknown]:border data-[unknown]:border-dashed"
            data-unknown={interval.kind === "unknown" || undefined}
            data-rank={rank > 1 ? 2 : rank}
            style={{
              left: `${(100 * (interval.from - data.from)) / (data.to - data.from)}%`,
              width: `${(100 * (interval.to - interval.from)) / (data.to - data.from)}%`,
            }}
          >
            {!compact &&
              (interval.kind === "unknown" || interval.truthy === null) && (
                <span
                  className={`text-label block overflow-hidden px-[5px] whitespace-nowrap ${interval.kind === "unknown" ? "text-gray-7 leading-4 font-normal" : `leading-[18px] font-medium ${rank > 0 ? "text-foreground" : "text-background"}`}`}
                >
                  {text}
                </span>
              )}
          </span>
        );
      })}
    </div>
  );
};

export const rowSummary = (row: TimelineRow, windowWords: string): string => {
  const current = row.resource.data?.current?.text ?? "no current value";
  return `${row.label}: ${current}${sinceText(row) ? ` ${sinceText(row)}` : ""}; ${totalsText(row.resource.data?.timeline, row.trueWord)} in the last ${windowWords}.`;
};

export const TimelineLanes = ({
  rows,
  windowWords,
}: {
  rows: TimelineRow[];
  windowWords: string;
}) => {
  const first = rows.find((row) => row.resource.data?.timeline)?.resource.data
    ?.timeline;
  return (
    <section
      className="grid grid-cols-[minmax(0,8.5rem)_minmax(0,1fr)_auto] items-center gap-x-3.5 gap-y-3 @max-[600px]:grid-cols-[minmax(0,1fr)] @max-[600px]:gap-3.5"
      aria-label="State timeline"
    >
      <ul className="sr-only">
        {rows.map((row) => (
          <li key={row.key}>{rowSummary(row, windowWords)}</li>
        ))}
      </ul>
      {rows.map((row, index) => {
        const status = cardStatus(row.resource);
        return (
          <div
            className="contents @max-[600px]:grid @max-[600px]:grid-cols-[minmax(0,1fr)_auto] @max-[600px]:items-baseline @max-[600px]:gap-x-3 @max-[600px]:gap-y-1.5 @max-[420px]:grid-cols-[minmax(0,1fr)] @max-[420px]:gap-0.5"
            key={row.key}
          >
            <div className="grid min-w-0 leading-[1.35] @max-[600px]:flex @max-[600px]:flex-wrap @max-[600px]:items-baseline @max-[600px]:gap-x-2 @max-[600px]:gap-y-0">
              <span className="overflow-hidden text-ellipsis whitespace-nowrap">
                {row.label}
              </span>
              <span
                className="text-muted-foreground data-[value=empty]:text-gray-6 data-[value=dimmed]:text-gray-8"
                data-value={status.value}
              >
                {row.resource.data?.current?.text ?? "—"}
              </span>
              <StatusList
                className="grid @max-[600px]:flex"
                separators={false}
                entries={status.entries}
              />
            </div>
            <Track
              className="@max-[600px]:col-span-full @max-[420px]:mt-1.5"
              data={row.resource.data?.timeline}
              color={`var(--chart-${(index % 8) + 1})`}
              dimmed={status.value === "dimmed"}
            />
            <div className="text-muted-foreground text-right tabular-nums @max-[600px]:col-start-2 @max-[600px]:row-start-1 @max-[420px]:col-start-1 @max-[420px]:row-start-2 @max-[420px]:text-left">
              {totalsText(row.resource.data?.timeline, row.trueWord)}
            </div>
          </div>
        );
      })}
      <div className="@max-[600px]:hidden" />
      <Axis data={first} />
      <div className="@max-[600px]:hidden" />
    </section>
  );
};
