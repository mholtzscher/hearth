import { afterEach, describe, expect, it, vi } from "vitest";
import { feedEntries, readTimeline, readWeekPattern, timelineRequest, weekPatternRequest, weekStart } from "./changes";

const source = { entity_id: "ent_019a3e6a-4111-7000-8000-000000000001", entity_type: "hearth.power/v1" };
const to = new Date("2026-11-08T14:00:00Z");
const iso = (n: number) => new Date(n).toISOString();
const from = to.getTime() - 3_600_000;
function payload(intervals: unknown[], extra = {}) {
  return { kind: "timeline", entity_id: source.entity_id, select_field: "", range: { from: iso(from), to: to.toISOString() }, time_axis: "observed_at", coverage: { basis: "retained_changes", seeded: false }, intervals, ...extra };
}
function span(kind: "value" | "unknown", a: number, b: number, value?: unknown) {
  return { kind, from: iso(from + a * 600_000), to: iso(from + b * 600_000), ...(kind === "value" ? { value } : {}) };
}
afterEach(() => vi.restoreAllMocks());

describe("State changes", () => {
  it("requests the exact range and field and rejects identity, range and coverage mismatches", async () => {
    const raw = payload([span("value", 0, 6, true)]);
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(raw)));
    const request = timelineRequest(source, "1h");
    expect(request.kind).toBe("series");
    await request.load(new AbortController().signal, to);
    expect(JSON.parse(fetch.mock.calls[0][1]!.body as string)).toEqual({ range: raw.range, select_field: "", result: { kind: "timeline" } });
    for (const bad of [payload(raw.intervals, { entity_id: "other" }), payload(raw.intervals, { range: { from: iso(from + 1), to: to.toISOString() } }), payload([span("value", 1, 6, true)]), payload([span("value", 0, 2, true), span("value", 3, 6, false)])]) {
      fetch.mockResolvedValueOnce(new Response(JSON.stringify(bad)));
      await expect(request.load(new AbortController().signal, to)).rejects.toThrow("Invalid timeline");
    }
    const active = { ...source, entity_type: "hearth.colorhs/v1", field: "/active" };
    fetch.mockResolvedValueOnce(new Response(JSON.stringify(payload(raw.intervals, { select_field: "/active" }))));
    await timelineRequest(active, "1h").load(new AbortController().signal, to);
    expect(JSON.parse(fetch.mock.calls[fetch.mock.calls.length - 1][1]!.body as string).select_field).toBe("/active");
  });

  it("counts known transitions from null but not unknown and retains the open interval", () => {
    const data = readTimeline(payload([span("unknown", 0, 1), span("value", 1, 2, true), span("value", 2, 3, null), span("value", 3, 4, true), span("value", 4, 5, false), span("value", 5, 6, true)]), source, "1h", { true: "Open", false: "Closed" });
    expect(data.intervals.map(item => item.kind)).toEqual(["unknown", "value", "absent", "value", "value", "value"]);
    expect(data.totals).toEqual([{ text: "Open", truthy: true, seconds: 1800, entries: 2 }, { text: "Closed", truthy: false, seconds: 600, entries: 1 }]);
    expect(feedEntries([{ label: "Door", data }], to.getTime())[0]?.text).toContain("so far");
  });

  it("totals three text values and folds more than three entries in a day-part", () => {
    const textSource = { ...source, entity_type: "hearth.enumsetting/v1" };
    const data = readTimeline(payload([span("value", 0, 1, "Eco"), span("value", 1, 2, "Heat"), span("value", 2, 3, "Cool"), span("value", 3, 4, "Heat"), span("value", 4, 5, "Cool"), span("value", 5, 6, "Eco")]), textSource, "1h");
    expect(data.totals.map(total => [total.text, total.seconds, total.entries])).toEqual([["Eco", 1200, 1], ["Heat", 1200, 2], ["Cool", 1200, 2]]);
    const feed = feedEntries([{ label: "Mode", data }], to.getTime());
    expect(feed).toHaveLength(1);
    expect(feed[0].text).toMatch(/^5 times since /);
    expect(feed[0].pulses).toHaveLength(5);
    // Text changes are instants, so their pulses are ticks rather than held spans.
    expect(feed[0].pulses?.every(pulse => pulse.from === pulse.to)).toBe(true);
  });

  it("labels and orders local day-parts before folding each row independently", () => {
    const now = new Date(2026, 10, 8, 18).getTime();
    const morning = new Date(2026, 10, 8, 8).getTime();
    const yesterday = new Date(2026, 10, 7, 18).getTime();
    const data = { from: yesterday, to: now, window: "24h" as const, totals: [], intervals: [yesterday, morning, morning + 60_000, morning + 120_000, morning + 180_000].map(at => ({ kind: "value" as const, from: at, to: at + 30_000, text: "Open", truthy: true })) };
    const feed = feedEntries([{ label: "Door", data }], now);
    expect(feed.map(item => item.group)).toEqual(["This morning", "Yesterday evening"]);
    expect(feed[0].text).toMatch(/^4 times since /);
    expect(feed[1].text).toBe("open for 1 min");
  });

  it("requests hourly summary buckets with local midnight and rejects a malformed range", async () => {
    const start = weekStart(to);
    const raw = { kind: "summary", entity_id: source.entity_id, select_field: "", range: { from: start.toISOString(), to: to.toISOString() }, time_axis: "observed_at", coverage: { basis: "retained_changes", seeded: true }, bucket: { duration: "1h0m0s", alignment: "range_start" }, buckets: Array.from({ length: Math.ceil((to.getTime() - start.getTime()) / 3_600_000) }, (_, i) => ({ range: { from: iso(start.getTime() + i * 3_600_000), to: iso(Math.min(start.getTime() + (i + 1) * 3_600_000, to.getTime())) }, seconds_true: 0, known_seconds: Math.min(3600, (to.getTime() - start.getTime() - i * 3_600_000) / 1000), transitions_into_true: 0 })) };
    const fetch = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(raw)));
    const request = weekPatternRequest(source, start);
    await request.load(new AbortController().signal, to);
    expect(JSON.parse(fetch.mock.calls[0][1]!.body as string)).toEqual({ range: raw.range, select_field: "", result: { kind: "summary", bucket: { duration: "1h", alignment: "range_start" } } });
    fetch.mockResolvedValueOnce(new Response(JSON.stringify({ ...raw, buckets: raw.buckets.slice(1) })));
    await expect(request.load(new AbortController().signal, to)).rejects.toThrow("Invalid week pattern");
  });

  it("maps repeated and skipped DST hours by local bucket start", () => {
    const old = process.env.TZ; process.env.TZ = "America/New_York";
    try {
      const now = new Date("2026-11-02T12:00:00-05:00"), start = weekStart(now);
      const bucket = (start: string, seconds: number) => ({ range: { from: start, to: iso(Date.parse(start) + 3_600_000) }, seconds_true: seconds, known_seconds: 3600, transitions_into_true: 1 });
      const raw = { kind: "summary", entity_id: source.entity_id, select_field: "", range: { from: start.toISOString(), to: now.toISOString() }, time_axis: "observed_at", coverage: { basis: "retained_changes", seeded: true }, bucket: { duration: "1h0m0s", alignment: "range_start" }, buckets: [bucket("2026-11-01T01:00:00-04:00", 100), bucket("2026-11-01T01:00:00-05:00", 200)] };
      const week = readWeekPattern(raw, start, now);
      const sunday = week.days.find(day => new Date(day.start).getDate() === 1)!;
      expect(sunday.hours[1]).toMatchObject({ seconds: 300, entries: 2 });
      expect(sunday.hours[2].seconds).toBeNull();
      const springNow = new Date("2026-03-09T12:00:00-04:00"), springStart = weekStart(springNow);
      const spring = readWeekPattern({ ...raw, range: { from: springStart.toISOString(), to: springNow.toISOString() }, buckets: [bucket("2026-03-08T01:00:00-05:00", 100), bucket("2026-03-08T03:00:00-04:00", 200)] }, springStart, springNow);
      const springSunday = spring.days.find(day => new Date(day.start).getDate() === 8)!;
      expect(springSunday.hours[1].seconds).toBe(100);
      expect(springSunday.hours[2].seconds).toBeNull();
      expect(springSunday.hours[3].seconds).toBe(200);
    } finally { if (old === undefined) delete process.env.TZ; else process.env.TZ = old; }
  });
});
