import { describe, expect, it } from "vitest";
import type { SeriesData } from "../../bindings/types";
import { coverageStart, niceTicks, timeTicks } from "./scale";

describe("niceTicks", () => {
  it("rounds a narrow range to steps of 1, 2, or 5 × 10^n", () => {
    const { domain, ticks } = niceTicks(19.8, 21.6, 4);
    expect(domain).toEqual([19.5, 22]);
    expect(ticks).toEqual([19.5, 20, 20.5, 21, 21.5, 22]);
  });

  it("keeps integer ranges on whole steps", () => {
    expect(niceTicks(65, 75, 4).ticks).toEqual([65, 70, 75]);
    expect(niceTicks(1003, 1021, 4).ticks).toEqual([1000, 1005, 1010, 1015, 1020, 1025]);
  });

  it("gives a flat series a band without crossing zero", () => {
    expect(niceTicks(0, 0, 4).domain[0]).toBe(0);
    const flat = niceTicks(65, 65, 4);
    expect(flat.domain[0]).toBeLessThan(65);
    expect(flat.domain[1]).toBeGreaterThan(65);
  });
});

describe("timeTicks", () => {
  it("adds the weekday to the first tick of a new day when the window crosses midnight", () => {
    const to = new Date(2026, 10, 5, 18, 45).getTime();
    const ticks = timeTicks(to - 24 * 3_600_000, to);
    const midnight = ticks.findIndex(tick => new Date(tick.value).getHours() === 0);
    expect(midnight).toBeGreaterThan(0);
    expect(ticks[midnight].label).toMatch(/^\p{L}+ /u);
    expect(ticks[midnight + 1].label).not.toMatch(/^\p{L}+ /u);
    // The first tick is on the previous day, so it names that day too.
    expect(ticks[0].label).toMatch(/^\p{L}+ /u);
  });

  it("keeps a same-day window free of weekdays", () => {
    const to = new Date(2026, 10, 5, 18, 0).getTime();
    expect(timeTicks(to - 6 * 3_600_000, to).every(tick => !/^\p{L}+ /u.test(tick.label))).toBe(true);
  });

  it("uses at most about six ticks aligned to round local times", () => {
    const to = new Date(2026, 10, 5, 18, 47).getTime();
    const ticks = timeTicks(to - 3_600_000, to);
    expect(ticks.length).toBeLessThanOrEqual(6);
    expect(ticks.every(tick => new Date(tick.value).getMinutes() % 15 === 0)).toBe(true);
  });
});

describe("coverageStart", () => {
  const window = (firstSample: number): SeriesData => ({
    from: "2026-11-05T00:00:00Z", to: "2026-11-05T10:00:00Z", format: { unit_label: "", fraction_digits: 0 },
    buckets: Array.from({ length: 10 }, (_, i) => ({
      start: `2026-11-05T0${i}:00:00Z`, end: `2026-11-05T0${i}:59:59Z`,
      min: i >= firstSample ? 1 : null, max: i >= firstSample ? 1 : null, mean: i >= firstSample ? 1 : null,
    })),
  });

  it("names a history that begins well after the window starts", () => {
    expect(coverageStart([window(5)])).toBe("2026-11-05T05:00:00.000Z");
  });

  it("stays quiet when history covers the window, or there is none", () => {
    expect(coverageStart([window(1)])).toBeUndefined();
    expect(coverageStart([window(10)])).toBeUndefined();
  });

  it("uses the earliest series", () => {
    expect(coverageStart([window(8), window(2)])).toBe("2026-11-05T02:00:00.000Z");
  });
});
