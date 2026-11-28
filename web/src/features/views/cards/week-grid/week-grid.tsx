import type { z } from "zod";

import { weekStart } from "../../bindings/changes";
import type { HistorySpan } from "../../bindings/history";
import { useNumericSpans } from "../../bindings/registry";
import { quantityField } from "../../bindings/state-fields";
import type { Resolved, SeriesData } from "../../bindings/types";
import { DataCard } from "../data-card";
import { cardStatus } from "../status";
import {
  BLOCKS_PER_DAY,
  blockMiddleHour,
  dayMean,
  foldBlocks,
  peakBlock,
  shadeScale,
  weekMean,
} from "./blocks";
import type { GridBlock, GridDay } from "./blocks";
import type { weekGridSchema } from "./schema";

/** Seven local days in three-hour buckets: 56 reads at most. */
export const weekSpan: HistorySpan = {
  name: "week-3h",
  range: (to) => ({ from: weekStart(to), to }),
  bucket: "3h0m0s",
};

// Light text on the three dark shades, dark text on the two light ones.
const cellClass =
  "block rounded-xs bg-gray-3 text-gray-10 data-[shade='1']:bg-[color-mix(in_oklch,var(--gray-10)_22%,var(--gray-2))] data-[shade='2']:bg-[color-mix(in_oklch,var(--gray-10)_40%,var(--gray-2))] data-[shade='3']:bg-[color-mix(in_oklch,var(--gray-10)_70%,var(--gray-2))] data-[shade='3']:text-gray-0 data-[shade='4']:bg-gray-10 data-[shade='4']:text-gray-0 data-[future]:bg-transparent data-[future]:shadow-[inset_0_0_0_1px_var(--gray-3)] data-[unknown]:bg-transparent data-[unknown]:outline data-[unknown]:outline-dashed data-[unknown]:outline-gray-5 data-[unknown]:outline-offset-[-1px]";
const averageClass =
  "text-right whitespace-nowrap text-muted-foreground @max-[420px]:hidden";

const hourWord = (hour: number): string =>
  new Date(2026, 0, 1, hour).toLocaleTimeString([], { hour: "numeric" });

const GridCell = ({
  block,
  shade,
  digits,
}: {
  block: GridBlock;
  shade: (value: number) => number;
  digits: number;
}) => (
  <span
    data-slot="week-grid-cell"
    className={`${cellClass} h-7 text-center leading-7 font-medium`}
    data-shade={block.value === null ? undefined : shade(block.value)}
    data-future={(block.value === null && block.future) || undefined}
    data-unknown={(block.value === null && !block.future) || undefined}
  >
    {block.value !== null && (
      <>
        <span className="@max-[420px]:hidden">
          {block.value.toFixed(digits)}
        </span>
        <span className="@min-[421px]:hidden">{Math.round(block.value)}</span>
      </>
    )}
  </span>
);

const blockSignature = (
  block: GridBlock,
  shade: (value: number) => number,
  digits: number
): string => {
  if (block.value !== null) {
    return `${shade(block.value)}:${block.value.toFixed(digits)}`;
  }
  return block.future ? "future" : "none";
};

const dayName = (day: GridDay, index: number): string =>
  index === 6
    ? "Today"
    : new Date(day.start).toLocaleDateString([], { weekday: "short" });

const summaryOf = (
  days: GridDay[],
  label: string,
  digits: number,
  unit: string
): string[] => [
  ...days.map((day) => {
    const name = new Date(day.start).toLocaleDateString([], {
      weekday: "long",
    });
    const average = dayMean(day);
    const gaps = day.blocks.some(
      (block) => block.value === null && !block.future
    );
    return average === null
      ? `${name}: no history.`
      : `${name}: average ${average.toFixed(digits)}${unit}.${gaps ? " Some blocks have no history." : ""}`;
  }),
  `${label}.`,
];

const Grid = ({
  days,
  digits,
  shade,
}: {
  days: GridDay[];
  digits: number;
  shade: (value: number) => number;
}) => (
  <div
    data-slot="week-grid"
    aria-hidden="true"
    className="text-label grid grid-cols-[2.4rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-[3px] @max-[420px]:grid-cols-[2.2rem_minmax(0,1fr)]"
  >
    <span />
    <div className="text-caption text-muted-foreground grid grid-cols-8 gap-0.5">
      {Array.from({ length: BLOCKS_PER_DAY }, (_, block) => block * 3).map(
        (hour) => (
          <span key={hour}>
            {hourWord(hour).replace(/\s?[AP]M/u, "")}
            <span className="@max-[420px]:hidden">
              {hour % 12 === 0 ? ` ${hour < 12 ? "AM" : "PM"}` : ""}
            </span>
          </span>
        )
      )}
    </div>
    <span className={averageClass}>Avg</span>
    {days.map((day, index) => {
      const average = dayMean(day);
      return (
        <div className="contents" key={day.start}>
          <span className="text-muted-foreground">{dayName(day, index)}</span>
          <div className="text-caption grid grid-cols-8 gap-0.5">
            {day.blocks.map((block) => (
              <GridCell
                key={block.start}
                block={block}
                shade={shade}
                digits={digits}
              />
            ))}
          </div>
          <span className={averageClass}>
            {average === null ? "—" : average.toFixed(digits)}
          </span>
        </div>
      );
    })}
  </div>
);

export const WeekGrid = ({
  label,
  entityId,
  resource,
  highWord,
}: {
  label: string;
  entityId: string;
  resource: Resolved<SeriesData>;
  /** "warmest" for temperatures, "highest" otherwise. */
  highWord: string;
}) => {
  const status = cardStatus(resource);
  const { data, metadata } = resource;
  const view = data && {
    days: foldBlocks(data.buckets, Date.parse(data.from), Date.parse(data.to)),
    digits: data.format.fraction_digits,
    unit: data.format.unit_label,
  };
  const average = view ? weekMean(view.days) : null;
  const peak = view ? peakBlock(view.days) : null;
  const unitText = view?.unit ? ` ${view.unit}` : "";
  const sentence =
    peak === null
      ? "No history in the last 7 days"
      : `average this week · ${highWord} around ${hourWord(blockMiddleHour(peak.block))}`;
  const shade = view ? shadeScale(view.days) : () => 0;
  const signature =
    view &&
    JSON.stringify(
      view.days.map((day) =>
        day.blocks.map((block) => blockSignature(block, shade, view.digits))
      )
    );
  return (
    <DataCard
      className="@container"
      title={label}
      unit="Last 7 days"
      status={status.entries}
      sources={[
        {
          entityId,
          label: metadata?.label ?? label,
          enabled: metadata?.enabled,
          availability: metadata?.availability,
        },
      ]}
      arrivals={
        data && signature
          ? [
              {
                key: entityId,
                updatedAt: new Date(data.to).toISOString(),
                shown: signature,
                eligible: status.glows,
              },
            ]
          : []
      }
      sourceExtra={
        <>
          <p>
            Each cell averages the readings in three local hours. A dashed cell
            has no readings; an outlined cell is still to come. The current
            block shows its average so far.
          </p>
          {data && (
            <p>
              {new Date(data.from).toLocaleString()} to{" "}
              {new Date(data.to).toLocaleString()}
            </p>
          )}
        </>
      }
    >
      {view ? (
        <>
          <p className="text-label text-muted-foreground mb-4 flex flex-wrap items-baseline gap-x-2">
            <span className="text-compact-reading text-foreground">
              {average === null
                ? "—"
                : `${average.toFixed(view.digits)}${unitText}`}
            </span>
            <span>{sentence}</span>
          </p>
          <section aria-label={`${label}, last 7 days`}>
            <ul className="sr-only">
              {summaryOf(
                view.days,
                peak === null
                  ? "No history in the last 7 days"
                  : `${highWord[0].toUpperCase()}${highWord.slice(1)} around ${hourWord(blockMiddleHour(peak.block))}, averaging ${peak.mean.toFixed(view.digits)}${unitText}`,
                view.digits,
                unitText
              ).map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
            <Grid days={view.days} digits={view.digits} shade={shade} />
          </section>
        </>
      ) : (
        <p className="text-label text-muted-foreground py-6">
          Historical readings will appear here.
        </p>
      )}
    </DataCard>
  );
};

export const BoundWeekGrid = ({
  source,
  label,
}: z.infer<typeof weekGridSchema>) => {
  const [resource] = useNumericSpans([{ source, span: weekSpan }]);
  const temperature =
    quantityField(source)?.quantity.quantity_kind === "temperature";
  return (
    <WeekGrid
      label={label ?? resource.metadata?.label ?? "Reading"}
      entityId={source.entity_id}
      resource={resource}
      highWord={temperature ? "warmest" : "highest"}
    />
  );
};
