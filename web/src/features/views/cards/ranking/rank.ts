import { pointer, quantityField } from "../../bindings/state-fields";
import type { EntitySource } from "../../bindings/types";

/** One ranked reading: its last value, if any, and its range over the window. */
export interface RankedRow {
  label: string;
  value: number | null;
  /** An unavailable or disabled Entity keeps its last value, drawn hollow. */
  dimmed: boolean;
  low?: number;
  high?: number;
}

/** Quantities where zero is not "none", so a bar from zero would mislead. */
const offsetKinds = new Set([
  "temperature",
  "atmospheric_pressure",
  "color_temperature",
  "color_hue",
  "chromaticity",
]);

/**
 * The field's quantity kind: named by the catalog, or by the Entity's support
 * when the catalog refers to it (a numeric sensor declares its own kind).
 */
export const quantityKind = (
  source: EntitySource,
  support: unknown
): string | undefined => {
  const kind = quantityField(source)?.quantity.quantity_kind;
  if (typeof kind === "string" || kind === undefined) {
    return kind;
  }
  try {
    const declared = pointer(support, kind.path);
    return typeof declared === "string" ? declared : undefined;
  } catch {
    return undefined;
  }
};

/**
 * The mark rule: the range plot when a window is set, when the quantity has no
 * meaningful zero, or when a reading is negative; bars from zero otherwise.
 */
export const usesRangePlot = (
  rows: RankedRow[],
  kinds: (string | undefined)[],
  range: string | undefined
): boolean =>
  range !== undefined ||
  kinds.some((kind) => kind !== undefined && offsetKinds.has(kind)) ||
  rows.some((row) => row.value !== null && row.value < 0);

/** Highest first; rows with no value last, keeping their authored order. */
export const sortRows = <T extends RankedRow>(rows: T[]): T[] =>
  rows.toSorted((a, b) => {
    if (a.value === null || b.value === null) {
      return Number(a.value === null) - Number(b.value === null);
    }
    return b.value - a.value;
  });

/** The minimum and maximum over a history's buckets, or undefined when none have samples. */
export const bucketExtent = (
  buckets: { min: number | null; max: number | null }[]
): { low: number; high: number } | undefined => {
  const lows = buckets.flatMap((bucket) =>
    bucket.min === null ? [] : [bucket.min]
  );
  const highs = buckets.flatMap((bucket) =>
    bucket.max === null ? [] : [bucket.max]
  );
  return lows.length && highs.length
    ? { low: Math.min(...lows), high: Math.max(...highs) }
    : undefined;
};
