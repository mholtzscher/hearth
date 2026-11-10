import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { cardStatus } from "../status";

export function shownData(resource: Resolved<MetricData> | undefined): MetricData | undefined {
  return resource && cardStatus(resource).value !== "empty" ? resource.data : undefined;
}

/** The lowest and highest sampled values in a history window, or undefined without samples. */
export function extremes(series: SeriesData | undefined): { low: number; high: number } | undefined {
  const sampled = series?.buckets.filter(bucket => bucket.mean !== null) ?? [];
  if (sampled.length === 0) return undefined;
  return {
    low: Math.min(...sampled.map(bucket => bucket.min ?? bucket.mean!)),
    high: Math.max(...sampled.map(bucket => bucket.max ?? bucket.mean!)),
  };
}
