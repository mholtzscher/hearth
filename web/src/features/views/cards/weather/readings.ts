import type { MetricData, Resolved, SeriesData } from "../../bindings/types";
import { canonicalValue, convertUnit, present, type UnitSystem, type ValueType } from "../../../units/units";
import { cardStatus } from "../status";

/** Canonical units of the weather Entity types, from the generated State field catalog. */
export const canonicalUnits = {
  temperature: { unit: "mCel", type: "integer" }, humidity: { unit: "%", type: "number" },
  speed: { unit: "m/s", type: "number" }, pressure: { unit: "hPa", type: "number" },
  rainRate: { unit: "mm/h", type: "number" }, rainDepth: { unit: "mm", type: "number" },
  direction: { unit: "deg", type: "number" },
} as const satisfies Record<string, { unit: string; type: ValueType }>;
export type WeatherQuantity = keyof typeof canonicalUnits;

/** How a derived value shows: dimmed when any reading it uses is dimmed, like those readings. */
export function derivedTreatment(...resources: Array<Resolved<MetricData> | undefined>): "live" | "dimmed" {
  return resources.some(resource => resource && cardStatus(resource).value === "dimmed") ? "dimmed" : "live";
}

/** The reading a card shows under the state table: undefined when its value is empty. */
export function shownData(resource: Resolved<MetricData> | undefined): MetricData | undefined {
  return resource && cardStatus(resource).value !== "empty" ? resource.data : undefined;
}

/** A displayed value converted back to a formula's working unit, such as Cel from °F. */
export function working(displayed: number, quantity: WeatherQuantity, target: string, system: UnitSystem): number | undefined {
  const { unit, type } = canonicalUnits[quantity];
  return convertUnit(canonicalValue(displayed, unit, type, system), unit, target) ?? undefined;
}

/** A shown reading in a formula's working unit, or undefined when the card shows no value. */
export function workingReading(resource: Resolved<MetricData> | undefined, quantity: WeatherQuantity, target: string, system: UnitSystem): number | undefined {
  const data = shownData(resource);
  return data === undefined ? undefined : working(data.value, quantity, target, system);
}

/** A derived temperature in the household unit, such as "43.5 °F". */
export function formatTemperature(celsius: number, system: UnitSystem): string {
  const shown = present(celsius, "Cel", "number", system);
  return `${shown.value.toFixed(shown.fraction_digits)} ${shown.label}`;
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
 * The latest sampled mean, within ten minutes of the window's end, and the mean
 * nearest three hours before it, within ten minutes. Undefined when either is
 * missing: a tendency is never guessed, and an old change is never shown as current.
 */
export function threeHourChange(series: SeriesData | undefined): { then: number; now: number } | undefined {
  const sampled = series?.buckets.filter(bucket => bucket.mean !== null) ?? [];
  const latest = sampled[sampled.length - 1];
  if (!series || !latest || Date.parse(series.to) - Date.parse(latest.end) > TOLERANCE) return undefined;
  const target = Date.parse(latest.start) - THREE_HOURS;
  let best: { distance: number; mean: number } | undefined;
  for (const bucket of sampled) {
    const distance = Math.abs(Date.parse(bucket.start) - target);
    if (distance <= TOLERANCE && (!best || distance < best.distance)) best = { distance, mean: bucket.mean! };
  }
  return best && { then: best.mean, now: latest.mean! };
}
