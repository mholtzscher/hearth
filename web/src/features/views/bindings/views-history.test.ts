import { afterEach, describe, expect, it, vi } from "vitest";
import { historySeriesRequest, type HistorySeries } from "./history";

const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.temperature/v1" };
const to = new Date("2026-11-01T10:00:00Z");
const millis = { "1h": 3_600_000, "6h": 21_600_000, "24h": 86_400_000, "7d": 604_800_000 } as const;
const duration = { "1h": "1m0s", "6h": "1m0s", "24h": "5m0s", "7d": "30m0s" } as const;
const counts = { "1h": 60, "6h": 360, "24h": 288, "7d": 336 } as const;

/**
 * A summary whose aggregates mirror the admitted range, resolution and bucket
 * edges; the patch overrides keep the exact wire shape.
 */
function summary(window: keyof typeof millis, patch?: (payload: {
  entity_id: string; range: { from: string; to: string };
  bucket: { duration: string; alignment: "range_start" };
  buckets: Array<{ range: { from: string; to: string }; metrics: { min: number | null; max: number | null; mean: number | null } }>;
}) => void, at = to): unknown {
  const span = millis[window];
  const seriesBuckets = counts[window];
  const step = span / seriesBuckets;
  const from = new Date(Math.ceil(at.getTime() / step) * step - span);
  const payload: any = {
    entity_id: source.entity_id,
    range: { from: from.toISOString(), to: at.toISOString() },
    bucket: { duration: duration[window], alignment: "range_start" },
    buckets: Array.from({ length: seriesBuckets }, (_, i) => {
      const start = new Date(from.getTime() + i * span / seriesBuckets);
      const end = i < seriesBuckets - 1
        ? new Date(from.getTime() + (i + 1) * span / seriesBuckets)
        : new Date(Math.min(start.getTime() + span / seriesBuckets, at.getTime()));
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
        select_field: "",
        result: { kind: "summary", metrics: { min: { operation: "min" }, max: { operation: "max" }, mean: { operation: "sample_mean" } }, bucket: { duration: "1m0s", alignment: "range_start" } },
      }), headers: { "content-type": "application/json" }, signal: expect.anything() },
    );
  });
  it("accepts every window and preserves the validated bucket layout", async () => {
    for (const window of ["1h", "6h", "24h", "7d"] as const) {
      post(summary(window));
      const data = await historySeriesRequest(source, window).load(new AbortController().signal, to) as HistorySeries;
      expect(data.buckets).toHaveLength(counts[window]);
      expect(data.from).toBe(new Date(to.getTime() - millis[window]).toISOString());
      expect(data.buckets[0].start).toBe(data.from);
      expect(data.buckets[0]).toMatchObject({ min: 1, max: 2, mean: 1.5 });
    }
  });
  it("normalizes root identity and separates field requests and null policies", async () => {
    expect(historySeriesRequest(source, "6h").key).toBe(historySeriesRequest({ ...source, field: "" }, "6h").key);
    const hue = { ...source, entity_type: "hearth.colorhs/v1", field: "/hue" };
    expect(historySeriesRequest(hue, "6h").key).not.toBe(historySeriesRequest({ ...hue, field: "/saturation" }, "6h").key);
    post(summary("6h"));
    await historySeriesRequest(hue, "6h").load(new AbortController().signal, to);
    let body = JSON.parse(vi.mocked(fetch).mock.calls[0][1]!.body as string);
    expect(body.select_field).toBe("/hue");
    expect(body.result.nulls).toBeUndefined();
    await historySeriesRequest({ ...source, entity_type: "hearth.numericsetting/v1", field: "/value" }, "6h").load(new AbortController().signal, to);
    body = JSON.parse(vi.mocked(fetch).mock.calls[1][1]!.body as string);
    expect(body.select_field).toBe("/value");
    expect(body.result.nulls).toBe("gap");
  });

  it("clips the final bucket and aligns starts for every window at off-grid times", async () => {
    const at = new Date("2026-11-01T10:00:17.123Z");
    for (const window of ["1h", "6h", "24h", "7d"] as const) {
      post(summary(window, undefined, at));
      const data = await historySeriesRequest(source, window).load(new AbortController().signal, at) as HistorySeries;
      expect(data.buckets).toHaveLength(counts[window]);
      const step = millis[window] / counts[window];
      expect(new Date(data.from).getTime() % step).toBe(0);
      const last = data.buckets[data.buckets.length - 1];
      expect(last.end).toBe(at.toISOString());
      expect(new Date(last.end).getTime() - new Date(last.start).getTime()).toBeLessThan(step);
      const calls = vi.mocked(fetch).mock.calls;
      const body = JSON.parse(calls[calls.length - 1][1]!.body as string);
      expect(body.select_field).toBe("");
      expect(body.result.bucket.duration).toBe(duration[window]);
    }
  });

  it("rejects a summary with the wrong bucket count and keeps empty buckets", async () => {
    post(summary("6h", payload => { payload.buckets = []; }));
    await expect(historySeriesRequest(source, "6h").load(new AbortController().signal, to)).rejects.toThrow("resolution");

    post(summary("6h", payload => { payload.buckets[1].metrics = { min: null, max: null, mean: null }; }));
    const data = await historySeriesRequest(source, "6h").load(new AbortController().signal, to) as HistorySeries;
    expect(data.buckets[1]).toMatchObject({ min: null, max: null, mean: null });
  });
});
