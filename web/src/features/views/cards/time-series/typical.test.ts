import { describe, expect, it } from "vitest";

import {
  dayRows,
  departure,
  localMidnight,
  SLOTS,
  todayPoints,
  usualRange,
} from "./typical";

const bucketAt = (time: Date, mean: number | null) => ({
  start: time.toISOString(),
  end: time.toISOString(),
  min: mean,
  max: mean,
  mean,
});
/** Local wall-clock time on a day in a week with no daylight-saving change. */
const at = (day: number, hour: number, minute = 0) =>
  new Date(2026, 5, day, hour, minute);

/** Today's five-minute samples from 9 AM on the 8th. */
const today = (values: number[]) =>
  todayPoints(
    values.map((value, index) => bucketAt(at(8, 9, index * 5), value))
  );

describe(usualRange, () => {
  it("folds each day's bucket into its half hour of the local day", () => {
    const usual = usualRange([
      bucketAt(at(1, 6, 30), 10),
      bucketAt(at(2, 6, 30), 14),
      bucketAt(at(3, 6, 30), 12),
    ]);
    expect(usual).toHaveLength(SLOTS);
    expect(usual[13]).toStrictEqual({ low: 10, high: 14, mean: 12 });
  });

  it("has no usual range for a slot seen on fewer than two days", () => {
    const usual = usualRange([
      bucketAt(at(1, 6), 10),
      bucketAt(at(2, 6), null),
    ]);
    expect(usual[12]).toBeNull();
  });

  it("keeps a bucket in its local slot across a daylight-saving change", () => {
    // Buckets aligned to a midnight before the change start an hour off the
    // index a fixed 30-minute grid would give them; the local time still wins.
    const before = new Date(2026, 2, 7, 15);
    const after = new Date(2026, 2, 9, 15);
    const usual = usualRange([bucketAt(before, 8), bucketAt(after, 12)]);
    expect(usual[30]).toStrictEqual({ low: 8, high: 12, mean: 10 });
  });
});

describe(departure, () => {
  const usual = usualRange(
    [1, 2].flatMap((day) =>
      Array.from({ length: SLOTS }, (_, slot) =>
        bucketAt(at(day, 0, slot * 30), 10 + day)
      )
    )
  );

  it("says within when the latest value is inside the range", () => {
    expect(departure(today([11.5]), usual).kind).toBe("within");
  });

  it("names the start of the run that is still outside now", () => {
    const result = departure(today([15, 11.5, 13, 14]), usual);
    expect(result).toMatchObject({
      kind: "outside",
      above: true,
      gap: 2,
      since: at(8, 9, 10).getTime(),
    });
  });

  it("reports below with the distance under the low edge", () => {
    expect(departure(today([9]), usual)).toMatchObject({
      kind: "outside",
      above: false,
      gap: 2,
    });
  });

  it("has nothing to say without samples or a usual range", () => {
    expect(departure([], usual).kind).toBe("none");
    expect(departure(today([12]), []).kind).toBe("none");
  });
});

describe(dayRows, () => {
  it("spans the whole day and carries the band to midnight", () => {
    const usual = usualRange(
      [1, 2].map((day) => bucketAt(at(day, 23, 30), 10 + day))
    );
    const rows = dayRows(todayPoints([bucketAt(at(8, 0, 5), 7)]), usual);
    expect(rows).toHaveLength(289);
    expect(rows[1]).toMatchObject({ minute: 5, today: 7, usual: null });
    expect(rows.at(-1)).toMatchObject({ minute: 1440, usual: [11, 12] });
  });
});

describe(localMidnight, () => {
  it("returns the start of a local day some days back", () => {
    expect(localMidnight(at(8, 15, 40), 7)).toStrictEqual(at(1, 0));
  });
});
