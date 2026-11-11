import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { cardStatus } from "../status";

/** How a derived value shows: dimmed when any reading it uses is dimmed, like those readings. */
export function derivedTreatment(...resources: Array<Resolved<MetricData> | undefined>): "live" | "dimmed" {
  return resources.some(resource => resource && cardStatus(resource).value === "dimmed") ? "dimmed" : "live";
}

/** The reading a card shows under the state table: undefined when its value is empty. */
export function shownData(resource: Resolved<MetricData> | undefined): MetricData | undefined {
  return resource && cardStatus(resource).value !== "empty" ? resource.data : undefined;
}

/**
 * The canonical value of a shown reading, for formulas with fixed working
 * units. Undefined unless the card shows the reading and the canonical read
 * describes the same observation, so words never describe a different value.
 */
export function canonicalValue(shown: Resolved<MetricData> | undefined, canonical: Resolved<MetricData> | undefined): number | undefined {
  const data = shownData(shown), working = canonical?.data;
  return data && working && working.observed_at === data.observed_at ? working.value : undefined;
}

export function formatReading(data: MetricData): string {
  return `${data.value.toFixed(data.format.fraction_digits)}${data.format.unit_label ? ` ${data.format.unit_label}` : ""}`;
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

const THREE_HOURS = 3 * 60 * 60_000, TOLERANCE = 10 * 60_000;

/**
 * The latest sampled mean, within ten minutes of the current time, and the mean
 * nearest three hours before it, within ten minutes, with the start of each
 * bucket. Undefined when either is missing: a tendency is never guessed, and an
 * old change is never shown as current.
 */
export function threeHourChange(series: SeriesData | undefined, now: number): { then: number; now: number; thenStart: string; nowStart: string } | undefined {
  const sampled = series?.buckets.filter(bucket => bucket.mean !== null) ?? [];
  const latest = sampled[sampled.length - 1];
  if (!latest || now - Date.parse(latest.end) > TOLERANCE) return undefined;
  const target = Date.parse(latest.start) - THREE_HOURS;
  let best: { distance: number; mean: number; start: string } | undefined;
  for (const bucket of sampled) {
    const distance = Math.abs(Date.parse(bucket.start) - target);
    if (distance <= TOLERANCE && (!best || distance < best.distance)) best = { distance, mean: bucket.mean!, start: bucket.start };
  }
  return best && { then: best.mean, now: latest.mean!, thenStart: best.start, nowStart: latest.start };
}
