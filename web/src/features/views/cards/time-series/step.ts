import type { SeriesData } from "../../bindings/types";

/**
 * A held field keeps its last value until the next change, so its line is a
 * step. Each bucket gets the value held at its end. A bucket whose minimum and
 * maximum differ contains a change: the step is placed at that bucket's start,
 * because the buckets do not say when inside it the value changed.
 */
export const stepValues = (
  buckets: SeriesData["buckets"]
): { value: number | null; changed: boolean }[] => {
  const steps: { value: number | null; changed: boolean }[] = [];
  let previous: number | null = null;
  for (const [index, bucket] of buckets.entries()) {
    const { min, max, mean } = bucket;
    if (min === null || max === null || mean === null) {
      steps.push({ value: null, changed: false });
      previous = null;
      continue;
    }
    let value = mean;
    if (min !== max) {
      const next = buckets[index + 1];
      if (previous === min) {
        value = max;
      } else if (previous === max) {
        value = min;
      } else if (next && next.min !== null && next.min === next.max) {
        value = next.min;
      }
    }
    steps.push({ value, changed: previous !== null && value !== previous });
    previous = value;
  }
  return steps;
};
