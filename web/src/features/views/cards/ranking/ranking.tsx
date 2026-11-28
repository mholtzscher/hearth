import type { z } from "zod";

import { entityStateRequest, readEntity } from "../../bindings/entity";
import { useNumericReads } from "../../bindings/registry";
import type {
  EntitySource,
  MetricData,
  NumericFormat,
  Resolved,
  SeriesData,
  SeriesWindow,
} from "../../bindings/types";
import { useReads } from "../../data/provider";
import { DataCard, StatusList } from "../data-card";
import type { SourceItem } from "../data-card";
import { cardStatus, statusEntry } from "../status";
import type { StatusEntry } from "../status";
import { niceTicks } from "../time-series/scale";
import { Answer, Key, Plot } from "./parts";
import { bucketExtent, quantityKind, sortRows, usesRangePlot } from "./rank";
import type { RankedRow } from "./rank";
import type { rankingSchema } from "./schema";

export const rangeWords: Record<SeriesWindow, string> = {
  "1h": "last hour",
  "6h": "last 6 hours",
  "24h": "last 24 hours",
  "7d": "last 7 days",
};

const rowClass =
  "grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)_4rem] items-center gap-x-3 @max-[420px]:grid-cols-[minmax(0,1fr)_auto]";
const trackClass = "relative @max-[420px]:order-3 @max-[420px]:col-span-2";
const valueClass = "text-right data-[dimmed]:text-gray-8 whitespace-nowrap";
const incompatibleUnits = "Readings use incompatible units.";

export interface RankingInput {
  label?: string;
  current: Resolved<MetricData | SeriesData>;
  /** The row's history over `range`, when one is set. */
  history?: Resolved<MetricData | SeriesData>;
  kind?: string;
}

interface ShownRow extends RankedRow {
  key: string;
  status: StatusEntry[];
  summary: string;
}

const currentOf = (
  resource: Resolved<MetricData | SeriesData>
): MetricData | undefined =>
  resource.data && "value" in resource.data ? resource.data : undefined;

const seriesOf = (
  resource: Resolved<MetricData | SeriesData> | undefined
): SeriesData | undefined =>
  resource?.data && "buckets" in resource.data ? resource.data : undefined;

const formatter = (digits: number) =>
  new Intl.NumberFormat(undefined, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });

/** Status words for a row: its reading's, then any the history read adds. */
const rowStatus = (input: RankingInput): StatusEntry[] => {
  const { entries } = cardStatus(input.current);
  const extra = input.history
    ? cardStatus(input.history).entries.filter(
        (entry) => !entries.some((known) => known.key === entry.key)
      )
    : [];
  return [...entries, ...extra];
};

const shownRows = (
  inputs: RankingInput[],
  range: SeriesWindow | undefined,
  format: Intl.NumberFormat,
  unit: string
): ShownRow[] =>
  inputs.map((input, index) => {
    const current = currentOf(input.current);
    const extent = bucketExtent(seriesOf(input.history)?.buckets ?? []);
    const label =
      input.label ?? input.current.metadata?.label ?? `Row ${index + 1}`;
    const dimmed = cardStatus(input.current).value === "dimmed";
    const value = current?.value ?? null;
    const reading =
      value === null ? "no reading" : `${format.format(value)} ${unit}`;
    const span =
      range && extent
        ? `, ${format.format(extent.low)} to ${format.format(extent.high)} over the ${rangeWords[range]}`
        : "";
    return {
      key: `${index}`,
      label,
      value,
      dimmed,
      ...extent,
      status: rowStatus(input),
      summary: `${label}: ${reading}${span}${dimmed ? ", unavailable, last reading" : ""}.`,
    };
  });

const Name = ({ row }: { row: ShownRow }) => (
  <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
    <span className="[overflow-wrap:anywhere]">{row.label}</span>
    <StatusList entries={row.status} separators={false} />
  </span>
);

const Value = ({
  row,
  format,
}: {
  row: ShownRow;
  format: Intl.NumberFormat;
}) => (
  <span className={valueClass} data-dimmed={row.dimmed || undefined}>
    {row.value === null ? "—" : format.format(row.value)}
  </span>
);

/** Bars from zero: honest for quantities where zero means none, such as power. */
const BarRows = ({
  rows,
  format,
}: {
  rows: ShownRow[];
  format: Intl.NumberFormat;
}) => {
  const peak = Math.max(0, ...rows.map((row) => row.value ?? 0));
  return (
    <>
      {rows.map((row) => (
        <div className={rowClass} key={row.key}>
          <Name row={row} />
          <span className={`${trackClass} h-2.5`}>
            {row.value !== null && (
              <span
                className="bg-chart-1 data-[dimmed]:border-gray-8 data-[dimmed]:bg-card block h-full min-w-px rounded-xs data-[dimmed]:border"
                data-dimmed={row.dimmed || undefined}
                style={{
                  width: peak > 0 ? `${(row.value / peak) * 100}%` : "0%",
                }}
              />
            )}
          </span>
          <Value row={row} format={format} />
        </div>
      ))}
    </>
  );
};

/** Range plot: a dot on a shared scale, for readings where a bar from zero would mislead. */
const RangeRows = ({
  rows,
  format,
  shared,
}: {
  rows: ShownRow[];
  format: Intl.NumberFormat;
  shared: ReturnType<typeof niceTicks>;
}) => {
  const { domain, ticks } = shared;
  const x = (value: number) =>
    `${((value - domain[0]) / (domain[1] - domain[0])) * 100}%`;
  const tickFormat = new Intl.NumberFormat();
  return (
    <>
      {rows.map((row) => (
        <div className={rowClass} key={row.key}>
          <Name row={row} />
          <span className={`${trackClass} h-8`}>
            {ticks.map((tick) => (
              <span
                className="bg-gray-3 absolute inset-y-0 w-px"
                key={tick}
                style={{ left: x(tick) }}
              />
            ))}
            {row.low !== undefined && row.high !== undefined && (
              <span
                className="bg-gray-5 absolute top-1/2 h-1 -translate-y-1/2 rounded-full"
                style={{
                  left: x(row.low),
                  right: `calc(100% - ${x(row.high)})`,
                }}
              />
            )}
            {row.value !== null && (
              <span
                className="bg-chart-1 ring-card data-[dimmed]:border-gray-8 data-[dimmed]:bg-card absolute top-1/2 size-2.5 -translate-1/2 rounded-full ring-2 data-[dimmed]:border-2"
                data-dimmed={row.dimmed || undefined}
                style={{ left: x(row.value) }}
              />
            )}
          </span>
          <Value row={row} format={format} />
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
              {tickFormat.format(tick)}
            </span>
          ))}
        </span>
        <span />
      </div>
    </>
  );
};

/** The answer in words: the highest live reading and the lowest. */
const answerOf = (
  rows: ShownRow[],
  format: Intl.NumberFormat,
  unit: string
): { value: string; words: string } | undefined => {
  const live = rows.filter((row) => row.value !== null && !row.dimmed);
  const [top] = live;
  const bottom = live.at(-1);
  if (!top || top.value === null) {
    return undefined;
  }
  const value = `${format.format(top.value)} ${unit}`;
  if (live.length < 2 || !bottom || bottom.value === null) {
    return { value, words: `${top.label} is highest` };
  }
  return {
    value,
    words: `${top.label} is highest · ${bottom.label} is lowest at ${format.format(bottom.value)} ${unit}`,
  };
};

const unitsOf = (
  inputs: RankingInput[]
): { format?: NumericFormat; mixed: boolean } => {
  const formats = inputs.flatMap((input) => {
    const current = currentOf(input.current);
    return current ? [current.format] : [];
  });
  const [format] = formats;
  return {
    format,
    mixed: formats.some((entry) => entry.unit_label !== format?.unit_label),
  };
};

const sourcesOf = (inputs: RankingInput[]): SourceItem[] => {
  const sources = new Map<string, SourceItem>();
  for (const input of inputs) {
    const { metadata } = input.current;
    if (metadata) {
      sources.set(`${metadata.id}:${metadata.label}`, {
        entityId: metadata.id,
        label: metadata.label,
        availability: metadata.availability,
        enabled: metadata.enabled,
      });
    }
  }
  return [...sources.values()];
};

const methodWords = (
  rangePlot: boolean,
  range: SeriesWindow | undefined
): string => {
  if (!rangePlot) {
    return "Each bar is the Entity's current reading, drawn from zero and sorted highest first. An unavailable Entity shows its last reading.";
  }
  const bar = range
    ? ` The bar behind it spans the minimum and maximum over the ${rangeWords[range]}.`
    : "";
  return `Each dot is the Entity's current reading on a shared scale, sorted highest first.${bar} An unavailable Entity shows its last reading.`;
};

const plotLabel = (title: string, rangePlot: boolean): string =>
  `${title}, ${rangePlot ? "dot on a shared scale" : "bars from zero"}, highest first`;

export const Ranking = ({
  title,
  range,
  rows: inputs,
}: {
  title: string;
  range?: SeriesWindow;
  rows: RankingInput[];
}) => {
  const { format: unitFormat, mixed } = unitsOf(inputs);
  const unit = unitFormat?.unit_label ?? "";
  const format = formatter(unitFormat?.fraction_digits ?? 0);
  const rows = sortRows(shownRows(inputs, range, format, unit));
  const rangePlot = usesRangePlot(
    rows,
    inputs.map((input) => input.kind),
    range
  );
  const values = rows.flatMap((row) => [
    ...(row.value === null ? [] : [row.value]),
    ...(row.low === undefined ? [] : [row.low]),
    ...(row.high === undefined ? [] : [row.high]),
  ]);
  const shared = niceTicks(Math.min(...values), Math.max(...values), 6);
  const answer = answerOf(rows, format, unit);
  const plotted = rangePlot && values.length > 0;
  const arrivals = inputs.flatMap((input, index) => {
    const current = currentOf(input.current);
    return current
      ? [
          {
            key: `${index}:${input.current.metadata?.id ?? ""}`,
            updatedAt: current.observed_at,
            shown: `${current.value} ${current.format.unit_label}`,
            eligible: cardStatus(input.current).glows,
          },
        ]
      : [];
  });
  return (
    <DataCard
      className="@container"
      title={title}
      unit={mixed ? undefined : unit || undefined}
      status={mixed ? [statusEntry("error", incompatibleUnits)] : []}
      arrivals={mixed ? [] : arrivals}
      sources={sourcesOf(inputs)}
      sourceExtra={<p>{methodWords(rangePlot, range)}</p>}
    >
      {mixed ? null : (
        <>
          {answer && <Answer value={answer.value}>{answer.words}</Answer>}
          {plotted && (
            <Key
              className="mb-3"
              items={[
                { label: "Now", mark: "bg-chart-1 size-2.5 rounded-full" },
                ...(range
                  ? [
                      {
                        label: `${rangeWords[range][0].toUpperCase()}${rangeWords[range].slice(1)}`,
                        mark: "bg-gray-5 h-1 w-5 rounded-full",
                      },
                    ]
                  : []),
              ]}
            />
          )}
          <Plot
            label={plotLabel(title, rangePlot)}
            summary={rows.map((row) => row.summary)}
            className={`text-label grid ${rangePlot ? "" : "gap-y-2.5"}`}
          >
            {plotted ? (
              <RangeRows rows={rows} format={format} shared={shared} />
            ) : (
              <BarRows rows={rows} format={format} />
            )}
          </Plot>
        </>
      )}
    </DataCard>
  );
};

const supportOf = (raw: unknown, source: EntitySource): unknown => {
  if (raw === undefined) {
    return null;
  }
  try {
    return readEntity(raw, source).support;
  } catch {
    return null;
  }
};

/** Each row's Entity support, for a quantity kind a numeric sensor declares itself. */
const useSupports = (sources: EntitySource[]): unknown[] => {
  const snapshots = useReads(
    sources.map((source) => entityStateRequest(source))
  );
  return sources.map((source, index) =>
    supportOf(snapshots[index]?.data, source)
  );
};

export const BoundRanking = ({
  title,
  rows,
  range,
}: z.infer<typeof rankingSchema>) => {
  const sources = rows.map((row) => row.source);
  const current = useNumericReads(sources.map((source) => ({ source })));
  const history = useNumericReads(
    range ? sources.map((source) => ({ source, window: range })) : []
  );
  const supports = useSupports(sources);
  return (
    <Ranking
      title={title}
      range={range}
      rows={rows.map((row, index) => ({
        label: row.label,
        current: current[index],
        history: range ? history[index] : undefined,
        kind: quantityKind(row.source, supports[index]),
      }))}
    />
  );
};
