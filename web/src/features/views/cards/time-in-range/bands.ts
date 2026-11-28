/** A positive-affine map between a canonical value and its household display value. */
export interface Affine {
  scale: number;
  offset: number;
}

/**
 * The band a bucket mean falls in, counted from the lowest. With two
 * thresholds the middle band includes both; with one, the upper band starts at it.
 */
export const bandIndex = (mean: number, thresholds: number[]): number => {
  if (thresholds.length === 1) {
    return mean < thresholds[0] ? 0 : 1;
  }
  if (mean < thresholds[0]) {
    return 0;
  }
  return mean > thresholds[1] ? 2 : 1;
};

/**
 * Buckets per band and the number with samples. A bucket without samples
 * belongs to no band.
 */
export const countBands = (
  means: (number | null)[],
  thresholds: number[]
): { counts: number[]; sampled: number } => {
  const counts = Array.from({ length: thresholds.length + 1 }, () => 0);
  let sampled = 0;
  for (const mean of means) {
    if (mean !== null) {
      counts[bandIndex(mean, thresholds)] += 1;
      sampled += 1;
    }
  }
  return { counts, sampled };
};

/** A share of the sampled buckets as a whole percent. */
export const percent = (count: number, sampled: number): string =>
  `${Math.round((count / sampled) * 100)}%`;

/**
 * The conversion shown by paired (canonical, display) values that were read
 * for the same bucket. Needs two distinct canonical values to fix the line.
 */
export const affineFromPairs = (
  pairs: [number, number][]
): Affine | undefined => {
  if (pairs.length === 0) {
    return undefined;
  }
  let [low] = pairs;
  let [high] = pairs;
  for (const pair of pairs) {
    low = pair[0] < low[0] ? pair : low;
    high = pair[0] > high[0] ? pair : high;
  }
  if (low[0] === high[0]) {
    return undefined;
  }
  const scale = (high[1] - low[1]) / (high[0] - low[0]);
  return scale > 0 ? { scale, offset: low[1] - scale * low[0] } : undefined;
};

/** The paired values of every bucket statistic that both reads delivered. */
export const bucketPairs = (
  canonical: { min: number | null; max: number | null; mean: number | null }[],
  display: { min: number | null; max: number | null; mean: number | null }[]
): [number, number][] =>
  canonical.flatMap((bucket, index) =>
    (["min", "max", "mean"] as const).flatMap((name): [number, number][] => {
      const c = bucket[name];
      const d = display[index]?.[name];
      return c === null || d === null || d === undefined ? [] : [[c, d]];
    })
  );

/** The band the card's answer is about, as the end of a sentence: the middle band, or the lower one. */
export const focusWords = (shown: string[], unit: string): string => {
  const suffix = unit ? ` ${unit}` : "";
  return shown.length === 1
    ? `below ${shown[0]}${suffix}`
    : `between ${shown[0]} and ${shown[1]}${suffix}`;
};

/** The names of the bands, using each threshold as shown. */
export const bandNames = (
  shown: string[],
  unit: string
): [string, string] | [string, string, string] => {
  const suffix = unit ? ` ${unit}` : "";
  if (shown.length === 1) {
    return [`Below ${shown[0]}${suffix}`, `${shown[0]}${suffix} and above`];
  }
  return [
    `Below ${shown[0]}${suffix}`,
    `${shown[0]} to ${shown[1]}${suffix}`,
    `Above ${shown[1]}${suffix}`,
  ];
};
