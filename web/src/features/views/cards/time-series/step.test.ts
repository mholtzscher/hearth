import { describe, expect, it } from "vitest";

import { stepValues } from "./step";

const bucket = (min: number | null, max = min, mean = min) => ({
  start: "",
  end: "",
  min,
  max,
  mean: min === null || max === null ? null : mean,
});

describe(stepValues, () => {
  it("holds a flat value and marks nothing changed", () => {
    expect(stepValues([bucket(18.5), bucket(18.5)])).toStrictEqual([
      { value: 18.5, changed: false },
      { value: 18.5, changed: false },
    ]);
  });

  it("places a change at the start of the bucket that contains it", () => {
    // The middle bucket saw both values; its mean lies between them.
    const steps = stepValues([
      bucket(18.5),
      bucket(18.5, 21, 19.6),
      bucket(21),
    ]);
    expect(steps.map((step) => step.value)).toStrictEqual([18.5, 21, 21]);
    expect(steps.map((step) => step.changed)).toStrictEqual([
      false,
      true,
      false,
    ]);
  });

  it("follows a change downward", () => {
    const steps = stepValues([bucket(21), bucket(17.5, 21, 20), bucket(17.5)]);
    expect(steps.map((step) => step.value)).toStrictEqual([21, 17.5, 17.5]);
  });

  it("marks a change that falls on a bucket edge", () => {
    const steps = stepValues([bucket(20), bucket(22)]);
    expect(steps[1]).toStrictEqual({ value: 22, changed: true });
  });

  it("keeps a gap and does not call the value after it a change", () => {
    const steps = stepValues([bucket(20), bucket(null), bucket(22)]);
    expect(steps).toStrictEqual([
      { value: 20, changed: false },
      { value: null, changed: false },
      { value: 22, changed: false },
    ]);
  });

  it("uses the next flat bucket when the history opens mid-change", () => {
    const steps = stepValues([bucket(18, 20, 19), bucket(20)]);
    expect(steps[0].value).toBe(20);
  });
});
