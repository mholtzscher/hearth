import { z } from "zod";

import { ApiError, apiFetch } from "../../../api/client";
import type { ReadRequest } from "../data/cache";
import { quantitySchema } from "./quantity";
import type { Quantity } from "./quantity";
import { quantityField } from "./state-fields";
import type { NumericSource, SeriesWindow } from "./types";

// One shared history read owns admission: the window's requested interval is
// computed here, and the response's bucket count is checked before the cache
// accepts it. The chart resolution stays a client choice across the shared windows.
const windows = {
  "1h": { bucketMillis: 60_000, bucket: "1m0s", count: 60 },
  "6h": { bucketMillis: 60_000, bucket: "1m0s", count: 360 },
  "24h": { bucketMillis: 300_000, bucket: "5m0s", count: 288 },
  "7d": { bucketMillis: 1_800_000, bucket: "30m0s", count: 336 },
} as const;

export interface HistorySeries {
  from: string;
  to: string;
  quantity: Quantity;
  buckets: {
    start: string;
    end: string;
    min: number | null;
    max: number | null;
    mean: number | null;
  }[];
  /** HTTP reads populate the same aggregation in stored units alongside display. */
  canonical?: HistorySeries;
}
const timestamp = z.string().datetime({ offset: true });
const bucketRange = z.object({ from: timestamp, to: timestamp });
const metrics = z.object({
  min: z.number().nullable(),
  max: z.number().nullable(),
  mean: z.number().nullable(),
});
const pairedSummary = z.object({
  range: bucketRange,
  buckets: z.array(
    z.object({
      range: bucketRange,
      canonical: z.object({ unit: z.string(), metrics }),
      display: quantitySchema.extend({ metrics }),
    })
  ),
});

/** Shown when Core refuses a window as more work than one query may do. */
export const tooMuchHistory =
  "Too many readings for this window. Choose a shorter one.";

/** A read error in the words a card shows; a refused window says how to recover. */
export const readErrorMessage = (
  error: Error | undefined
): string | undefined => {
  const limited =
    error instanceof ApiError &&
    error.status === 422 &&
    typeof error.detail === "object" &&
    error.detail.code === "query_limit";
  return limited ? tooMuchHistory : error?.message;
};

const windowRange = (window: SeriesWindow, to: Date) => {
  const { bucketMillis, count } = windows[window];
  // Ceil keeps exactly count buckets even when to lands on the bucket grid.
  const from =
    Math.ceil(to.getTime() / bucketMillis) * bucketMillis -
    count * bucketMillis;
  return { from: new Date(from), to };
};

/** The most buckets one history query returns (Core's HistoryMaxBuckets). */
const MAX_BUCKETS = 1000;

const readPairedSummary = (
  raw: unknown,
  validCount: (count: number) => boolean,
  canonicalUnit?: string
): HistorySeries => {
  const data = pairedSummary.parse(raw);
  if (!validCount(data.buckets.length)) {
    throw new Error("Invalid series resolution");
  }
  const [first] = data.buckets;
  if (canonicalUnit && first.canonical.unit !== canonicalUnit) {
    throw new Error("Canonical history unit mismatch");
  }
  for (const bucket of data.buckets) {
    if (
      bucket.canonical.unit !== first.canonical.unit ||
      bucket.display.unit !== first.display.unit ||
      bucket.display.label !== first.display.label ||
      bucket.display.fraction_digits !== first.display.fraction_digits
    ) {
      throw new Error("History bucket units differ");
    }
    for (const name of ["min", "max", "mean"] as const) {
      if (
        (bucket.canonical.metrics[name] === null) !==
        (bucket.display.metrics[name] === null)
      ) {
        throw new Error("Paired history gaps differ");
      }
    }
  }
  const interval = { from: data.range.from, to: data.range.to };
  return {
    ...interval,
    quantity: first.display,
    buckets: data.buckets.map((bucket) => ({
      start: bucket.range.from,
      end: bucket.range.to,
      ...bucket.display.metrics,
    })),
    canonical: {
      ...interval,
      quantity: {
        unit: first.canonical.unit,
        label: first.canonical.unit,
        fraction_digits: 1,
      },
      buckets: data.buckets.map((bucket) => ({
        start: bucket.range.from,
        end: bucket.range.to,
        ...bucket.canonical.metrics,
      })),
    },
  };
};

/**
 * A history read over a card-chosen range: fixed-length buckets aligned to the
 * range's start, so a range that starts at local midnight keeps local clock times.
 */
export interface HistorySpan {
  /** Names the span in the cache key; the range's start is added to it. */
  name: string;
  /** The range to read, ending no later than `to`. */
  range: (to: Date) => { from: Date; to: Date };
  /** A Go duration, such as "5m" or "3h". */
  bucket: string;
}

const summaryRequest = (
  source: NumericSource,
  key: string,
  canonicalUnit: string | undefined,
  plan: (to: Date) => {
    from: Date;
    to: Date;
    bucket: string;
    validCount: (count: number) => boolean;
  }
): ReadRequest => {
  const field = quantityField(source);
  if (!field) {
    throw new Error("Unsupported quantity field");
  }
  const expectedUnit =
    canonicalUnit ??
    (typeof field.quantity.unit === "string" ? field.quantity.unit : undefined);
  const path = source.field ?? "";
  const nulls = field.presence === "sometimes" ? "gap" : "reject";
  return {
    key: `entity-series:${source.entity_id}:${path}:${key}:${nulls}:paired`,
    kind: "series",
    load: (signal: AbortSignal, to: Date) => {
      const { from, to: until, bucket, validCount } = plan(to);
      const body = JSON.stringify({
        range: { from: from.toISOString(), to: until.toISOString() },
        select_field: path,
        result: {
          kind: "summary",
          ...(nulls === "gap" ? { nulls } : {}),
          metrics: {
            min: { operation: "min" },
            max: { operation: "max" },
            mean: { operation: "sample_mean" },
          },
          bucket: { duration: bucket, alignment: "range_start" },
        },
      });
      return apiFetch(
        `/v1/entities/${encodeURIComponent(source.entity_id)}/state/history`,
        { method: "POST", body, signal },
        (text) => readPairedSummary(JSON.parse(text), validCount, expectedUnit)
      );
    },
  };
};

export const historySeriesRequest = (
  source: NumericSource,
  window: SeriesWindow,
  canonicalUnit?: string
): ReadRequest =>
  summaryRequest(source, window, canonicalUnit, (to) => ({
    ...windowRange(window, to),
    bucket: windows[window].bucket,
    validCount: (count) => count === windows[window].count,
  }));

/** The span's start is part of the key, so a new local day is a new read. */
export const historySpanRequest = (
  source: NumericSource,
  span: HistorySpan,
  canonicalUnit?: string
): ReadRequest =>
  summaryRequest(
    source,
    `${span.name}:${span.range(new Date()).from.toISOString()}`,
    canonicalUnit,
    (to) => ({
      ...span.range(to),
      bucket: span.bucket,
      validCount: (count) => count >= 1 && count <= MAX_BUCKETS,
    })
  );
