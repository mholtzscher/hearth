import { describe, expect, it } from "vitest";

import {
  affineFromPairs,
  bandNames,
  bucketPairs,
  countBands,
  percent,
} from "./bands";

describe(countBands, () => {
  it("counts bucket means in three bands, leaving out buckets without samples", () => {
    const { counts, sampled } = countBands(
      [19.9, 20, 21, 22, 22.1, null, null, 30],
      [20, 22]
    );
    expect(counts).toStrictEqual([1, 3, 2]);
    expect(sampled).toBe(6);
  });

  it("splits at one threshold, which starts the upper band", () => {
    expect(countBands([1, 2, 3, null], [2]).counts).toStrictEqual([1, 2]);
  });

  it("leaves every count zero without samples", () => {
    expect(countBands([null], [1, 2])).toStrictEqual({
      counts: [0, 0, 0],
      sampled: 0,
    });
  });
});

describe(percent, () => {
  it("rounds to a whole percent", () => {
    expect(percent(1, 3)).toBe("33%");
    expect(percent(2, 3)).toBe("67%");
  });
});

describe("unit conversion", () => {
  // Canonical milli-Celsius against °F, as the history read pairs them.
  const canonical = [
    { min: 19_000, max: 21_000, mean: 20_000 },
    { min: null, max: null, mean: null },
    { min: 21_500, max: 23_000, mean: 22_000 },
  ];
  const display = canonical.map((bucket) => ({
    min: bucket.min === null ? null : (bucket.min / 1000) * 1.8 + 32,
    max: bucket.max === null ? null : (bucket.max / 1000) * 1.8 + 32,
    mean: bucket.mean === null ? null : (bucket.mean / 1000) * 1.8 + 32,
  }));

  it("recovers the line from paired buckets", () => {
    const affine = affineFromPairs(bucketPairs(canonical, display));
    expect(affine?.scale).toBeCloseTo(0.0018, 8);
    expect((affine?.scale ?? 0) * 20_000 + (affine?.offset ?? 0)).toBeCloseTo(
      68,
      6
    );
  });

  it("cannot recover it from one value", () => {
    expect(affineFromPairs([[1, 2]])).toBeUndefined();
    expect(affineFromPairs([])).toBeUndefined();
  });
});

describe(bandNames, () => {
  it("names three ranges", () => {
    expect(bandNames(["20", "22"], "°C")).toStrictEqual([
      "Below 20 °C",
      "20 to 22 °C",
      "Above 22 °C",
    ]);
  });

  it("names two ranges", () => {
    expect(bandNames(["20"], "°C")).toStrictEqual([
      "Below 20 °C",
      "20 °C and above",
    ]);
  });
});
