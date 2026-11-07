import { z } from "zod";
import { ApiError, apiFetch } from "../../../api/client";
import type { NumericSource, SeriesWindow } from "./types";
import type { ReadRequest } from "../data/cache";
import { quantityField } from "./state-fields";

// One shared history read owns admission: the exact requested interval is
// captured here, and the response is validated before the cache accepts it.
// The chart resolution stays a client choice across the shared windows.
export const seriesBuckets = 288;
const windowMillis = { "1h": 3_600_000, "6h": 21_600_000, "24h": 86_400_000, "7d": 604_800_000 } as const;
const windowDuration: Record<SeriesWindow, string> = { "1h": "12.5s", "6h": "1m15s", "24h": "5m0s", "7d": "35m0s" };

export type HistorySeries = {
  from: string; to: string;
  buckets: Array<{ start: string; end: string; min: number | null; max: number | null; mean: number | null }>;
};
const timestamp = z.string().datetime({ offset: true });
export const historySeriesSchema = z.object({
  from: timestamp, to: timestamp,
  buckets: z.array(z.object({
    start: timestamp, end: timestamp, min: z.number().nullable(), max: z.number().nullable(), mean: z.number().nullable(),
  })),
});

const bucketRange = z.object({ from: timestamp, to: timestamp });
const summary = z.object({
  entity_id: z.string(),
  range: bucketRange,
  bucket: z.object({ duration: z.string(), alignment: z.literal("range_start") }),
  buckets: z.array(z.object({
    range: bucketRange,
    metrics: z.object({
      min: z.number().nullable(), max: z.number().nullable(), mean: z.number().nullable(),
    }),
  })),
});

/** Shown when Core refuses a window as more work than one query may do. */
export const tooMuchHistory = "Too many readings for this window. Choose a shorter one.";

/** A read error in the words a card shows; a refused window says how to recover. */
export function readErrorMessage(error: Error | undefined): string | undefined {
  const limited = error instanceof ApiError && error.status === 422 &&
    typeof error.detail === "object" && error.detail.code === "query_limit";
  return limited ? tooMuchHistory : error?.message;
}

function windowRange(window: SeriesWindow, to: Date) {
  return { from: new Date(to.getTime() - windowMillis[window]), to };
}

/** Aggregates are either all reported or all absent, and mean stays between them. */
function checkAggregates(metrics: { min: number | null; max: number | null; mean: number | null }) {
  const { min, max, mean } = metrics;
  const some = min !== null || max !== null || mean !== null;
  if (some !== (min !== null && max !== null && mean !== null) || (min !== null && (min > mean! || mean! > max!))) {
    throw new Error("Invalid series aggregates");
  }
}

/** Validates the summary envelope, resolution, interval and bucket boundaries. */
function readSummary(raw: unknown, source: NumericSource, range: { from: Date; to: Date }, window: SeriesWindow): HistorySeries {
  const data = summary.parse(raw);
  if (data.entity_id !== source.entity_id) throw new Error("Series Entity identity mismatch");
  if (data.bucket.duration !== windowDuration[window] || data.buckets.length !== seriesBuckets) throw new Error("Invalid series resolution");
  const sameMillisecond = (time: string, at: Date) => Math.abs(new Date(time).getTime() - at.getTime()) < 1;
  if (!sameMillisecond(data.range.from, range.from) || !sameMillisecond(data.range.to, range.to)) throw new Error("Invalid series interval");
  const step = windowMillis[window] / seriesBuckets;
  return {
    from: data.range.from, to: data.range.to,
    buckets: data.buckets.map((bucket, index) => {
      const start = new Date(range.from.getTime() + index * step);
      const end = index < data.buckets.length - 1
        ? new Date(range.from.getTime() + (index + 1) * step)
        : new Date(Math.min(start.getTime() + step, range.to.getTime()));
      if (!sameMillisecond(bucket.range.from, start) || !sameMillisecond(bucket.range.to, end)) throw new Error("Invalid series bucket boundaries");
      checkAggregates(bucket.metrics);
      return { start: bucket.range.from, end: bucket.range.to, min: bucket.metrics.min, max: bucket.metrics.max, mean: bucket.metrics.mean };
    }),
  };
}

export function historySeriesRequest(source: NumericSource, window: SeriesWindow): ReadRequest {
  const field = quantityField(source);
  if (!field) throw new Error("Unsupported quantity field");
  const path = source.field ?? "";
  const nulls = field.presence === "sometimes" ? "gap" : "reject";
  const selection = path === "" ? "sample.value" : `sample.value${path.replace(/\//g, "?.")}`;
  return {
    key: `entity-series:${source.entity_id}:${path}:${window}:${nulls}`, kind: "series",
    load: (signal: AbortSignal, to: Date) => {
      const range = windowRange(window, to);
      const body = JSON.stringify({
        range: { from: range.from.toISOString(), to: range.to.toISOString() },
        select_expr: selection,
        result: { kind: "summary", ...(nulls === "gap" ? { nulls } : {}), metrics: { min: { operation: "min" }, max: { operation: "max" }, mean: { operation: "sample_mean" } }, bucket: { duration: windowDuration[window], alignment: "range_start" } },
      });
      return apiFetch(`/v1/entities/${encodeURIComponent(source.entity_id)}/state/history`, { method: "POST", body, signal },
        text => readSummary(JSON.parse(text), source, range, window));
    },
  };
}
