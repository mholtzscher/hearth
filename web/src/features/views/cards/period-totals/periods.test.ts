import { afterEach, describe, expect, it, vi } from "vitest";

import { readHourTotals } from "../../bindings/changes";
import type { WeekPatternData } from "../../bindings/types";
import { barDuration, compactDuration, dayTotals, summarize } from "./periods";

const hour = 3_600_000;
const day = (start: number, seconds: (number | null)[], future = false) => ({
  start,
  hours: Array.from({ length: 24 }, (_, index) => ({
    start: start + index * hour,
    end: start + (index + 1) * hour,
    seconds: seconds[index] ?? null,
    entries: 0,
    future: future && index > 10,
  })),
});
const bucket = (from: number, known: number, seconds: number) => ({
  range: {
    from: new Date(from).toISOString(),
    to: new Date(from + hour).toISOString(),
  },
  seconds_true: seconds,
  transitions_into_true: 0,
  known_seconds: known,
});
const raw = (buckets: unknown[], from: number, to: number) => ({
  kind: "summary",
  entity_id: "e",
  select_field: "",
  range: { from: new Date(from).toISOString(), to: new Date(to).toISOString() },
  time_axis: "observed_at",
  coverage: { basis: "retained_changes", seeded: true },
  bucket: { duration: "1h0m0s", alignment: "range_start" },
  buckets,
});

const read = () => {
  const start = Date.UTC(2026, 10, 9, 11);
  const now = Date.UTC(2026, 10, 10, 10, 20);
  return readHourTotals(
    raw(
      [
        bucket(start, 3600, 600),
        bucket(start + hour, 0, 0),
        bucket(start + 23 * hour, 1200, 300),
      ],
      start,
      now
    ),
    new Date(start),
    new Date(now)
  );
};

describe(dayTotals, () => {
  const start = Date.UTC(2026, 10, 3);
  const full = Array.from({ length: 24 }, () => 600);
  const week: WeekPatternData = {
    from: start,
    to: start + 6 * 24 * hour + 11 * hour,
    days: [
      day(start, full),
      day(start + 24 * hour, []),
      day(start + 48 * hour, [300, 300, null, 60]),
      day(start + 72 * hour, full),
      day(start + 96 * hour, full),
      day(start + 120 * hour, full),
      day(start + 144 * hour, [3600, 1800], true),
    ],
  };

  it("sums known hours, keeps an empty day without history, and marks today partial", () => {
    const { periods } = dayTotals(week);
    expect(periods.map((period) => period.seconds)).toStrictEqual([
      14_400,
      null,
      660,
      14_400,
      14_400,
      14_400,
      5400,
    ]);
    expect(periods.map((period) => period.partial)).toStrictEqual([
      false,
      false,
      false,
      false,
      false,
      false,
      true,
    ]);
    expect(periods.map((period) => period.gaps)).toStrictEqual([
      false,
      false,
      true,
      false,
      false,
      false,
      true,
    ]);
  });

  it("averages finished days with history and leaves out today and empty days", () => {
    const summary = summarize(dayTotals(week).periods);
    expect(summary.current).toStrictEqual({ seconds: 5400 });
    expect(summary.averageCount).toBe(5);
    expect(summary.average).toBe((14_400 * 4 + 660) / 5);
    expect(summary.longest?.seconds).toBe(14_400);
    expect(summary.peak).toBe(14_400);
  });
});

describe(summarize, () => {
  it("has no average, longest, or current period without history", () => {
    const empty = summarize([
      { start: 0, end: hour, seconds: null, partial: true, gaps: false },
    ]);
    expect(empty).toMatchObject({
      current: null,
      average: null,
      longest: null,
      peak: 1,
    });
  });
});

describe(readHourTotals, () => {
  afterEach(() => vi.unstubAllEnvs());

  it("makes 24 periods and places buckets by elapsed hour", () => {
    const data = read();
    expect(data.periods).toHaveLength(24);
    expect(data.periods[0]).toMatchObject({ seconds: 600, gaps: false });
    expect(data.periods[1].seconds).toBeNull();
    expect(data.periods[2].seconds).toBeNull();
  });

  it("marks only the current hour partial", () => {
    const { periods } = read();
    expect(periods[23]).toMatchObject({ seconds: 300, partial: true });
    expect(periods.filter((period) => period.partial)).toHaveLength(1);
  });

  it("keeps 24 one-hour periods through a clock change", () => {
    vi.stubEnv("TZ", "America/New_York");
    // 23 h before the hour at 12:00 EST on Sunday 1 Nov spans the repeated 01:00.
    const now = new Date("2026-11-01T12:00:00-05:00");
    const start = new Date(now.getTime() - 23 * hour);
    const data = readHourTotals(
      raw(
        [bucket(start.getTime() + 2 * hour, 3600, 60)],
        start.getTime(),
        now.getTime()
      ),
      start,
      now
    );
    expect(
      data.periods.map((period) => period.end - period.start)
    ).toStrictEqual(Array.from({ length: 24 }, () => hour));
    expect(data.periods[2].seconds).toBe(60);
  });
});

describe(compactDuration, () => {
  it("reads as hours and minutes", () => {
    expect(compactDuration(0)).toBe("0m");
    expect(compactDuration(20)).toBe("<1m");
    expect(compactDuration(45 * 60)).toBe("45m");
    expect(compactDuration(2 * 3600)).toBe("2h");
    expect(compactDuration(7800)).toBe("2h 10m");
  });
});

describe(barDuration, () => {
  it("drops the minute unit after hours", () => {
    expect(barDuration(12_600)).toBe("3h30");
    expect(barDuration(7200)).toBe("2h");
    expect(barDuration(2700)).toBe("45m");
  });
});
