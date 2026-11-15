import { afterEach, describe, expect, it, vi } from "vitest";

import { pairedHistoryResponse } from "../../../../.storybook/paired-fixtures";
import { historySeriesRequest } from "./history";
import type { HistorySeries } from "./history";

const source = {
  entity_id: "ent_019a3e6a-4111-7000-8000-000000000001",
  entity_type: "hearth.temperature/v1",
};
const to = new Date("2026-11-01T10:00:00Z");
const millis = {
  "1h": 3_600_000,
  "6h": 21_600_000,
  "24h": 86_400_000,
  "7d": 604_800_000,
} as const;
const duration = {
  "1h": "1m0s",
  "6h": "1m0s",
  "24h": "5m0s",
  "7d": "30m0s",
} as const;
const counts = { "1h": 60, "6h": 360, "24h": 288, "7d": 336 } as const;
const windows = ["1h", "6h", "24h", "7d"] as const;

interface SummaryBucketPayload {
  range: { from: string; to: string };
  metrics: { min: number | null; max: number | null; mean: number | null };
}
interface SummaryPayload {
  entity_id: string;
  quantity: { unit: string; label: string; fraction_digits: number };
  range: { from: string; to: string };
  bucket: { duration: string; alignment: "range_start" };
  buckets: SummaryBucketPayload[];
}

/**
 * A summary whose aggregates mirror the admitted range, resolution and bucket
 * edges; the patch overrides keep the exact wire shape.
 */
const summary = (
  window: keyof typeof millis,
  patch?: (payload: SummaryPayload) => void,
  at = to,
  unit = "Cel"
): unknown => {
  const span = millis[window];
  const seriesBuckets = counts[window];
  const step = span / seriesBuckets;
  const from = new Date(Math.ceil(at.getTime() / step) * step - span);
  const payload: SummaryPayload = {
    entity_id: source.entity_id,
    quantity: { unit: "Cel", label: "°C", fraction_digits: 1 },
    range: { from: from.toISOString(), to: at.toISOString() },
    bucket: { duration: duration[window], alignment: "range_start" },
    buckets: Array.from({ length: seriesBuckets }, (_, i) => {
      const start = new Date(from.getTime() + (i * span) / seriesBuckets);
      const end =
        i < seriesBuckets - 1
          ? new Date(from.getTime() + ((i + 1) * span) / seriesBuckets)
          : new Date(
              Math.min(start.getTime() + span / seriesBuckets, at.getTime())
            );
      return {
        range: { from: start.toISOString(), to: end.toISOString() },
        metrics: { min: 1, max: 2, mean: 1.5 },
      };
    }),
  };
  patch?.(payload);
  const canonicalUnit = unit === "Cel" ? "mCel" : unit;
  return pairedHistoryResponse(
    { ...payload, quantity: { unit, label: unit, fraction_digits: 1 } },
    {
      quantity: {
        unit: canonicalUnit,
        label: canonicalUnit,
        fraction_digits: 1,
      },
      buckets: payload.buckets.map((bucket) => ({
        range: bucket.range,
        metrics: Object.fromEntries(
          Object.entries(bucket.metrics).map(([key, value]) => [
            key,
            value === null ? null : Number(value) * (unit === "Cel" ? 1000 : 1),
          ])
        ),
      })),
    }
  );
};
const post = (payload: unknown): void => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(payload));
};
const requestBody = (call: Parameters<typeof fetch>) =>
  JSON.parse(String(call[1]?.body));

describe("shared history loader", () => {
  afterEach(() => vi.restoreAllMocks());

  it("posts the summary query for each window with the merged bucket plan", async () => {
    post(summary("6h"));
    await historySeriesRequest(source, "6h").load(
      new AbortController().signal,
      to
    );
    expect(globalThis.fetch).toHaveBeenCalledWith(
      `/v1/entities/${source.entity_id}/state/history`,
      {
        method: "POST",
        body: JSON.stringify({
          range: { from: "2026-11-01T04:00:00.000Z", to: to.toISOString() },
          select_field: "",
          result: {
            kind: "summary",
            metrics: {
              min: { operation: "min" },
              max: { operation: "max" },
              mean: { operation: "sample_mean" },
            },
            bucket: { duration: "1m0s", alignment: "range_start" },
          },
        }),
        headers: { "content-type": "application/json" },
        signal: expect.anything(),
      }
    );
  });

  it.each(windows)(
    "accepts %s and preserves the validated bucket layout",
    async (window) => {
      post(summary(window));
      const data = (await historySeriesRequest(source, window).load(
        new AbortController().signal,
        to
      )) as HistorySeries;
      expect(data.buckets).toHaveLength(counts[window]);
      expect(data.from).toBe(
        new Date(to.getTime() - millis[window]).toISOString()
      );
      expect(data.buckets[0].start).toBe(data.from);
      expect(data.buckets[0]).toMatchObject({ min: 1, max: 2, mean: 1.5 });
    }
  );

  it("normalizes root identity and separates field request keys", () => {
    expect(historySeriesRequest(source, "6h").key).toBe(
      historySeriesRequest({ ...source, field: "" }, "6h").key
    );
    const hue = { ...source, entity_type: "hearth.colorhs/v1", field: "/hue" };
    expect(historySeriesRequest(hue, "6h").key).not.toBe(
      historySeriesRequest({ ...hue, field: "/saturation" }, "6h").key
    );
  });

  it("posts each field's selector and null policy", async () => {
    const hue = { ...source, entity_type: "hearth.colorhs/v1", field: "/hue" };
    post(summary("6h", undefined, to, "deg"));
    await historySeriesRequest(hue, "6h").load(
      new AbortController().signal,
      to
    );
    const hueBody = requestBody(vi.mocked(fetch).mock.calls[0]);
    expect(hueBody.select_field).toBe("/hue");
    expect(hueBody.result.nulls).toBeUndefined();
    vi.mocked(fetch).mockResolvedValueOnce(
      Response.json(summary("6h", undefined, to, "1"))
    );
    await historySeriesRequest(
      { ...source, entity_type: "hearth.numericsetting/v1", field: "/value" },
      "6h"
    ).load(new AbortController().signal, to);
    const valueBody = requestBody(vi.mocked(fetch).mock.calls[1]);
    expect(valueBody.select_field).toBe("/value");
    expect(valueBody.result.nulls).toBe("gap");
  });

  it("loads both history projections with no query selector", async () => {
    post(summary("6h"));
    const series = (await historySeriesRequest(source, "6h").load(
      new AbortController().signal,
      to
    )) as HistorySeries;
    expect(vi.mocked(fetch).mock.calls[0][0]).toBe(
      `/v1/entities/${source.entity_id}/state/history`
    );
    expect(series.buckets[0].mean).toBe(1.5);
    expect(series).toMatchObject({
      canonical: {
        buckets: expect.arrayContaining([
          expect.objectContaining({ mean: 1500 }),
        ]),
      },
    });
  });

  it.each(windows)(
    "clips %s's final bucket and aligns starts at off-grid times",
    async (window) => {
      const at = new Date("2026-11-01T10:00:17.123Z");
      post(summary(window, undefined, at));
      const data = (await historySeriesRequest(source, window).load(
        new AbortController().signal,
        at
      )) as HistorySeries;
      expect(data.buckets).toHaveLength(counts[window]);
      const step = millis[window] / counts[window];
      expect(new Date(data.from).getTime() % step).toBe(0);
      const [last] = data.buckets.slice(-1);
      expect(last.end).toBe(at.toISOString());
      expect(
        new Date(last.end).getTime() - new Date(last.start).getTime()
      ).toBeLessThan(step);
    }
  );

  it.each(windows)(
    "posts %s's bucket duration and root selector",
    async (window) => {
      const at = new Date("2026-11-01T10:00:17.123Z");
      post(summary(window, undefined, at));
      await historySeriesRequest(source, window).load(
        new AbortController().signal,
        at
      );
      const { calls } = vi.mocked(fetch).mock;
      const [lastCall] = calls.slice(-1);
      const body = requestBody(lastCall);
      expect(body.select_field).toBe("");
      expect(body.result.bucket.duration).toBe(duration[window]);
    }
  );

  it("rejects a summary with the wrong bucket count and keeps empty buckets", async () => {
    post(
      summary("6h", (payload) => {
        payload.buckets = [];
      })
    );
    await expect(
      historySeriesRequest(source, "6h").load(new AbortController().signal, to)
    ).rejects.toThrow("resolution");

    post(
      summary("6h", (payload) => {
        payload.buckets[1].metrics = { min: null, max: null, mean: null };
      })
    );
    const data = (await historySeriesRequest(source, "6h").load(
      new AbortController().signal,
      to
    )) as HistorySeries;
    expect(data.buckets[1]).toMatchObject({ min: null, max: null, mean: null });
  });
});
