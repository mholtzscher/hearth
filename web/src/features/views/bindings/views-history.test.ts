import { afterEach, describe, expect, it, vi } from "vitest";
import { historySeriesRequest, seriesBuckets, type HistorySeries } from "./history";

const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.temperature/v1" };
const to = new Date("2026-11-01T10:00:00Z");
const millis = { "1h": 3_600_000, "6h": 21_600_000, "24h": 86_400_000, "7d": 604_800_000 } as const;
const duration = { "1h": "12.5s", "6h": "1m15s", "24h": "5m0s", "7d": "35m0s" } as const;

/**
 * A summary whose aggregates mirror the admitted range, resolution and bucket
 * edges; the patch overrides keep the exact wire shape.
 */
function summary(window: keyof typeof millis, patch?: (payload: {
  entity_id: string; range: { from: string; to: string };
  bucket: { duration: string; alignment: "range_start" };
  buckets: Array<{ range: { from: string; to: string }; metrics: { min: number | null; max: number | null; mean: number | null } }>;
}) => void): unknown {
  const span = millis[window];
  const from = new Date(to.getTime() - span);
  const payload: any = {
    entity_id: source.entity_id,
    range: { from: from.toISOString(), to: to.toISOString() },
    bucket: { duration: duration[window], alignment: "range_start" },
    buckets: Array.from({ length: seriesBuckets }, (_, i) => {
      const start = new Date(from.getTime() + i * span / seriesBuckets);
      const end = i < seriesBuckets - 1
        ? new Date(from.getTime() + (i + 1) * span / seriesBuckets)
        : new Date(Math.min(start.getTime() + span / seriesBuckets, to.getTime()));
      return { range: { from: start.toISOString(), to: end.toISOString() }, metrics: { min: 1, max: 2, mean: 1.5 } };
    }),
  };
  patch?.(payload);
  return payload;
}

afterEach(() => vi.restoreAllMocks());

function post(payload: unknown): void {
  vi.spyOn(globalThis, "fetch").mockImplementation(async () => new Response(JSON.stringify(payload), { status: 200 }));
}

describe("shared history loader", () => {
  it("posts the summary query for each window with the merged bucket plan", async () => {
    post(summary("6h"));
    await historySeriesRequest(source, "6h").load(new AbortController().signal, to);
    expect(globalThis.fetch).toHaveBeenCalledWith(
      `/v1/entities/${source.entity_id}/state/history`,
      { method: "POST", body: JSON.stringify({
        range: { from: "2026-11-01T04:00:00.000Z", to: to.toISOString() },
        result: { kind: "summary", metrics: { min: { operation: "min" }, max: { operation: "max" }, mean: { operation: "sample_mean" } }, bucket: { duration: "1m15s", alignment: "range_start" } },
      }), headers: { "content-type": "application/json" }, signal: expect.anything() },
    );
  });
  it("accepts every window and preserves the validated bucket layout", async () => {
    for (const window of ["1h", "6h", "24h", "7d"] as const) {
      post(summary(window));
      const data = await historySeriesRequest(source, window).load(new AbortController().signal, to) as HistorySeries;
      expect(data.buckets).toHaveLength(seriesBuckets);
      expect(data.from).toBe(new Date(to.getTime() - millis[window]).toISOString());
      expect(data.buckets[0].start).toBe(data.from);
      expect(data.buckets[0]).toMatchObject({ min: 1, max: 2, mean: 1.5 });
    }
  });

  it("rejects envelopes that disagree with the requested read", async () => {
    post(summary("6h", payload => { payload.entity_id = "ent_019a3e6a-4111-7000-8000-000000000002"; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("identity");
    post(summary("6h", payload => { payload.bucket.duration = "5m"; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("resolution");
    post(summary("6h", payload => { payload.buckets = []; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("resolution");
    post(summary("6h", payload => { payload.range.from = "2026-10-31T18:00:00Z"; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("interval");
  });

  it("rejects shifted bucket edges and incoherent aggregates", async () => {
    post(summary("6h", payload => { payload.buckets[3].range.from = "2026-01-01T00:00:00Z"; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("boundaries");

    post(summary("6h", payload => { payload.buckets[2].metrics = { min: null, max: 2, mean: 1.5 }; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("aggregates");

    post(summary("6h", payload => { payload.buckets[1].metrics = { min: 5, max: 2, mean: 3 }; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("aggregates");

    post(summary("6h", payload => { payload.buckets[1].metrics = { min: null, max: null, mean: null }; }));
    const data = await historySeriesRequest(source, "6h").load(new AbortController().signal, to) as HistorySeries;
    expect(data.buckets[1]).toMatchObject({ min: null, max: null, mean: null });
  });
});
