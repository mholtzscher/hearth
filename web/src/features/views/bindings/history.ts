import { z } from "zod";
import { ApiError, apiFetch } from "../../../api/client";
import type { NumericSource, SeriesWindow } from "./types";
import type { ReadRequest } from "../data/cache";
import { quantityField } from "./state-fields";
import { quantitySchema, type Quantity } from "./quantity";

// One shared history read owns admission: the window's requested interval is
// computed here, and the response's bucket count is checked before the cache
// accepts it. The chart resolution stays a client choice across the shared windows.
const windows = {
  "1h": { bucketMillis: 60_000, bucket: "1m0s", count: 60 },
  "6h": { bucketMillis: 60_000, bucket: "1m0s", count: 360 },
  "24h": { bucketMillis: 300_000, bucket: "5m0s", count: 288 },
  "7d": { bucketMillis: 1_800_000, bucket: "30m0s", count: 336 },
} as const;

export type HistorySeries = {
  from: string; to: string;
  quantity: Quantity;
  buckets: Array<{ start: string; end: string; min: number | null; max: number | null; mean: number | null }>;
};
const timestamp = z.string().datetime({ offset: true });
const bucketRange = z.object({ from: timestamp, to: timestamp });
const summary = z.object({
  quantity: quantitySchema,
  range: bucketRange,
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
  const { bucketMillis, count } = windows[window];
  // Ceil keeps exactly count buckets even when to lands on the bucket grid.
  const from = Math.ceil(to.getTime() / bucketMillis) * bucketMillis - count * bucketMillis;
  return { from: new Date(from), to };
}

function readSummary(raw: unknown, window: SeriesWindow): HistorySeries {
  const data = summary.parse(raw);
  if (data.buckets.length !== windows[window].count) throw new Error("Invalid series resolution");
  return {
    from: data.range.from, to: data.range.to, quantity: data.quantity,
    buckets: data.buckets.map(({ range, metrics }) => ({ start: range.from, end: range.to, ...metrics })),
  };
}

export function historySeriesRequest(source: NumericSource, window: SeriesWindow, units: "household" | "canonical" = "household"): ReadRequest {
  const field = quantityField(source);
  if (!field) throw new Error("Unsupported quantity field");
  const path = source.field ?? "";
  const nulls = field.presence === "sometimes" ? "gap" : "reject";
  return {
    key: `entity-series:${source.entity_id}:${path}:${window}:${nulls}:${units}`, kind: "series",
    load: (signal: AbortSignal, to: Date) => {
      const range = windowRange(window, to);
      const body = JSON.stringify({
        range: { from: range.from.toISOString(), to: range.to.toISOString() },
        select_field: path,
        result: { kind: "summary", ...(nulls === "gap" ? { nulls } : {}), metrics: { min: { operation: "min" }, max: { operation: "max" }, mean: { operation: "sample_mean" } }, bucket: { duration: windows[window].bucket, alignment: "range_start" } },
      });
      return apiFetch(`/v1/entities/${encodeURIComponent(source.entity_id)}/state/history${units === "canonical" ? "?units=canonical" : ""}`, { method: "POST", body, signal },
        text => readSummary(JSON.parse(text), window));
    },
  };
}
