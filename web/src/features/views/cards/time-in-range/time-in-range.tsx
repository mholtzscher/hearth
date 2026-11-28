import type { z } from "zod";

import { durationWords } from "../../bindings/changes";
import { useNumericReads } from "../../bindings/registry";
import type {
  NumericSource,
  Resolved,
  SeriesData,
  SeriesWindow,
} from "../../bindings/types";
import { DataCard } from "../data-card";
import { Answer, Plot } from "../ranking/parts";
import { cardStatus } from "../status";
import {
  affineFromPairs,
  bandNames,
  bucketPairs,
  countBands,
  focusWords,
  percent,
} from "./bands";
import type { Affine } from "./bands";
import type { timeInRangeSchema } from "./schema";

const windowWords: Record<SeriesWindow, string> = {
  "1h": "last hour",
  "6h": "last 6 hours",
  "24h": "last 24 hours",
  "7d": "last 7 days",
};
// Below, between, above; a single threshold uses the first and last.
const marks = ["bg-chart-4", "bg-chart-1", "bg-chart-5"];

/** The unit's household display of the thresholds, or the canonical numbers when no conversion is known. */
const shownThresholds = (
  thresholds: number[],
  canonical: SeriesData,
  display: SeriesData
): { values: string[]; unit: string } => {
  const same = canonical.unit !== undefined && canonical.unit === display.unit;
  const affine: Affine | undefined = same
    ? { scale: 1, offset: 0 }
    : affineFromPairs(bucketPairs(canonical.buckets, display.buckets));
  const digits = display.format.fraction_digits;
  const number = new Intl.NumberFormat(undefined, {
    maximumFractionDigits: affine ? digits : 6,
  });
  if (!affine) {
    return {
      values: thresholds.map((value) => number.format(value)),
      unit: canonical.unit ?? canonical.format.unit_label,
    };
  }
  return {
    values: thresholds.map((value) =>
      number.format(affine.scale * value + affine.offset)
    ),
    unit: display.format.unit_label,
  };
};

const bucketMillis = (data: SeriesData): number =>
  Math.max(
    ...data.buckets.map(
      (bucket) => Date.parse(bucket.end) - Date.parse(bucket.start)
    )
  );

interface BandRow {
  name: string;
  mark: string;
  count: number;
  hours: string;
  share: string;
}
interface Summary {
  rows: BandRow[];
  sampled: number;
  millis: number;
  shown: { values: string[]; unit: string };
}

const summarize = (
  thresholds: number[],
  canonical: SeriesData,
  display: SeriesData
): Summary => {
  const shown = shownThresholds(thresholds, canonical, display);
  const { counts, sampled } = countBands(
    canonical.buckets.map((bucket) => bucket.mean),
    thresholds
  );
  const names = bandNames(shown.values, shown.unit);
  const millis = bucketMillis(canonical);
  return {
    sampled,
    millis,
    shown,
    rows: counts.map((count, index) => ({
      name: names[index],
      mark: marks[counts.length === 2 && index === 1 ? 2 : index],
      count,
      hours: durationWords((count * millis) / 1000),
      share: sampled > 0 ? percent(count, sampled) : "—",
    })),
  };
};

const Bands = ({
  title,
  window,
  summary,
  dimmed,
}: {
  title: string;
  window: SeriesWindow;
  summary: Summary;
  dimmed: boolean;
}) => {
  const { rows, sampled, shown } = summary;
  const known = sampled > 0;
  return (
    <>
      {known ? (
        <Answer value={(rows.length === 3 ? rows[1] : rows[0]).share}>
          of the {windowWords[window]} {focusWords(shown.values, shown.unit)}
        </Answer>
      ) : (
        <p className="text-label text-muted-foreground mb-4">
          No history in this window.
        </p>
      )}
      <Plot
        label={`${title}, share of time in each range`}
        summary={rows.map((row) =>
          known
            ? `${row.name}: ${row.hours}, ${row.share}.`
            : `${row.name}: no history.`
        )}
        className="text-label"
      >
        {known && (
          <div className="flex h-3.5 gap-0.5">
            {rows
              .filter((row) => row.count > 0)
              .map((row) => (
                <span
                  className={`${row.mark} ${dimmed ? "opacity-60" : ""} min-w-0.5 rounded-xs`}
                  key={row.name}
                  style={{ flexGrow: row.count, flexBasis: 0 }}
                />
              ))}
          </div>
        )}
        <div className="mt-3 grid grid-cols-[auto_minmax(0,1fr)_auto_3rem] items-center gap-x-2.5 gap-y-1.5">
          {rows.map((row) => (
            <div className="contents" key={row.name}>
              <span
                className={`${row.mark} ${dimmed ? "opacity-60" : ""} size-2.5 rounded-xs`}
              />
              <span>{row.name}</span>
              <span className="text-muted-foreground text-right">
                {known ? row.hours : "—"}
              </span>
              <span className="text-right">{row.share}</span>
            </div>
          ))}
        </div>
      </Plot>
    </>
  );
};

export const TimeInRange = ({
  label,
  entityId,
  window,
  thresholds,
  canonical,
  display,
}: {
  label?: string;
  entityId: string;
  window: SeriesWindow;
  thresholds: number[];
  /** The history in the field's canonical unit, which the thresholds are written in. */
  canonical: Resolved<SeriesData>;
  /** The same history in household units, for naming the ranges. */
  display: Resolved<SeriesData>;
}) => {
  const ready = canonical.data && display.data;
  const status = cardStatus(
    ready ? canonical : { ...canonical, data: undefined }
  );
  const title = label ?? canonical.metadata?.label ?? "Reading";
  const { metadata } = canonical;
  const summary =
    canonical.data && display.data
      ? summarize(thresholds, canonical.data, display.data)
      : undefined;
  return (
    <DataCard
      className="@container"
      title={title}
      unit={`${windowWords[window][0].toUpperCase()}${windowWords[window].slice(1)}`}
      status={status.entries}
      arrivals={
        canonical.data && summary
          ? [
              {
                key: entityId,
                updatedAt: canonical.data.to,
                shown: summary.rows.map((row) => row.count).join("/"),
                eligible: status.glows,
              },
            ]
          : []
      }
      sources={
        metadata
          ? [
              {
                entityId,
                label: metadata.label,
                enabled: metadata.enabled,
                availability: metadata.availability,
              },
            ]
          : []
      }
      sourceExtra={
        canonical.data &&
        summary && (
          <>
            <p>
              Each range counts the {durationWords(summary.millis / 1000)}{" "}
              intervals whose average fell inside it. Intervals without samples
              are left out of the total, and hours are intervals times their
              length.
            </p>
            <p>
              {new Date(canonical.data.from).toLocaleString()} to{" "}
              {new Date(canonical.data.to).toLocaleString()}
            </p>
          </>
        )
      }
    >
      {summary ? (
        <Bands
          title={title}
          window={window}
          summary={summary}
          dimmed={status.value === "dimmed"}
        />
      ) : (
        <p className="text-label text-muted-foreground py-6">
          Historical readings will appear here.
        </p>
      )}
    </DataCard>
  );
};

export const BoundTimeInRange = ({
  source,
  label,
  window,
  thresholds,
}: z.infer<typeof timeInRangeSchema> & { source: NumericSource }) => {
  const [canonical] = useNumericReads([{ source, window, units: "canonical" }]);
  const [display] = useNumericReads([{ source, window }]);
  const series = (resource: typeof canonical): Resolved<SeriesData> => ({
    ...resource,
    data:
      resource.data && "buckets" in resource.data ? resource.data : undefined,
  });
  return (
    <TimeInRange
      label={label}
      entityId={source.entity_id}
      window={window}
      thresholds={thresholds}
      canonical={series(canonical)}
      display={series(display)}
    />
  );
};
