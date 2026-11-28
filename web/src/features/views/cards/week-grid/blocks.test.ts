import { afterEach, describe, expect, it, vi } from "vitest";

import { dayMean, foldBlocks, peakBlock, shadeScale, weekMean } from "./blocks";

const bucket = (start: string, mean: number | null) => ({
  start,
  end: new Date(Date.parse(start) + 10_800_000).toISOString(),
  min: mean,
  max: mean,
  mean,
});
const values = (days: ReturnType<typeof foldBlocks>, index: number) =>
  days[index].blocks.map((block) => block.value);

describe(foldBlocks, () => {
  afterEach(() => vi.unstubAllEnvs());
  const from = Date.parse("2026-11-03T00:00:00Z");
  const now = Date.parse("2026-11-09T10:30:00Z");
  const fold = () => {
    vi.stubEnv("TZ", "UTC");
    return foldBlocks(
      [
        bucket("2026-11-03T00:00:00Z", 10),
        bucket("2026-11-03T06:00:00Z", 14),
        bucket("2026-11-09T09:00:00Z", 20),
      ],
      from,
      now
    );
  };

  it("places each bucket by its local start", () => {
    const days = fold();
    expect(days).toHaveLength(7);
    expect(values(days, 0).slice(0, 3)).toStrictEqual([10, null, 14]);
    expect(days[6].blocks[3].value).toBe(20);
  });

  it("dashes past blocks without readings and outlines future ones", () => {
    const days = fold();
    expect(days[0].blocks[1]).toMatchObject({ value: null, future: false });
    expect(days[6].blocks[4]).toMatchObject({ value: null, future: true });
  });

  it("summarizes days, the week, and the peak block", () => {
    const days = fold();
    expect(dayMean(days[0])).toBe(12);
    expect(dayMean(days[1])).toBeNull();
    expect(weekMean(days)).toBeCloseTo(44 / 3);
    expect(peakBlock(days)).toStrictEqual({ block: 3, mean: 20 });
  });

  it("keeps a bucket with no mean empty", () => {
    vi.stubEnv("TZ", "UTC");
    const days = foldBlocks([bucket("2026-11-03T00:00:00Z", null)], from, now);
    expect(days[0].blocks[0]).toMatchObject({ value: null, future: false });
  });
});

describe("foldBlocks across a daylight-saving change", () => {
  afterEach(() => vi.unstubAllEnvs());

  it("places buckets by local time on the 25-hour autumn day", () => {
    vi.stubEnv("TZ", "America/New_York");
    // Sunday 1 Nov: 3 h buckets from midnight start at 00, 02 (repeated hour), 05, 08, ... 23 local.
    const starts = [
      "2026-11-01T00:00:00-04:00",
      "2026-11-01T02:00:00-05:00",
      "2026-11-01T05:00:00-05:00",
      "2026-11-01T08:00:00-05:00",
      "2026-11-01T11:00:00-05:00",
      "2026-11-01T14:00:00-05:00",
      "2026-11-01T17:00:00-05:00",
      "2026-11-01T20:00:00-05:00",
      "2026-11-01T23:00:00-05:00",
      "2026-11-02T02:00:00-05:00",
    ];
    const days = foldBlocks(
      starts.map((start, index) => bucket(start, index * 10)),
      new Date(2026, 10, 1).getTime(),
      Date.parse("2026-11-07T12:00:00-05:00")
    );
    // Buckets 0 and 1 share block 0; 23:00 lands in block 7, not on Monday.
    expect(values(days, 0)).toStrictEqual([5, 20, 30, 40, 50, 60, 70, 80]);
    expect(days[1].blocks[0].value).toBe(90);
    expect(days[1].blocks[1].value).toBeNull();
  });

  it("places buckets by local time on the 23-hour spring day", () => {
    vi.stubEnv("TZ", "America/New_York");
    // Sunday 8 Mar: the third hour after midnight starts at 04:00 local.
    const starts = [
      "2026-03-08T00:00:00-05:00",
      "2026-03-08T04:00:00-04:00",
      "2026-03-08T07:00:00-04:00",
      "2026-03-08T10:00:00-04:00",
      "2026-03-08T13:00:00-04:00",
      "2026-03-08T16:00:00-04:00",
      "2026-03-08T19:00:00-04:00",
      "2026-03-08T22:00:00-04:00",
      "2026-03-09T01:00:00-04:00",
    ];
    const days = foldBlocks(
      starts.map((start, index) => bucket(start, index * 10)),
      new Date(2026, 2, 8).getTime(),
      Date.parse("2026-03-14T12:00:00-04:00")
    );
    expect(values(days, 0)).toStrictEqual([0, 10, 20, 30, 40, 50, 60, 70]);
    expect(days[1].blocks[0].value).toBe(80);
  });
});

describe(shadeScale, () => {
  it("uses at most five shades from lightest to darkest", () => {
    const days = foldBlocks(
      [10, 12, 14, 16, 18, 20, 22, 24, 26, 28].map((mean, index) =>
        bucket(new Date(Date.UTC(2026, 10, 3, index * 3)).toISOString(), mean)
      ),
      Date.UTC(2026, 10, 3),
      Date.UTC(2026, 10, 9)
    );
    const shade = shadeScale(days);
    expect(shade(10)).toBe(0);
    expect(shade(28)).toBe(4);
    expect(shade(18)).toBeGreaterThan(shade(10));
  });
});
