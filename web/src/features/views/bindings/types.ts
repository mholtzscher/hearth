import type { ReadRequest } from "../data/cache";
import type { UnitSystem } from "../../units/units";

export type SeriesWindow = "1h" | "6h" | "24h" | "7d";
export type NumericFormat = { unit_label: string; fraction_digits: number };
export type MetricData = { value: number; format: NumericFormat; observed_at: string };
export type AbsentMetric = { absent: true; observed_at: string };
/** observed_at is the Entity's current State observation, so a chart can glow when a new reading arrives. */
export type SeriesData = { from: string; to: string; format: NumericFormat; observed_at?: string; buckets: Array<{
  start: string; end: string; min: number | null; max: number | null; mean: number | null;
}> };
export type ResourceMetadata = {
  id: string; label: string; enabled: boolean; availability: string;
};
export type NumericSource = { entity_id: string; entity_type: string; field?: string };
export type NumericAdapter = {
  stateRequest(source: NumericSource): ReadRequest;
  seriesRequest(source: NumericSource, window: SeriesWindow): ReadRequest;
  metadata(raw: unknown, source: NumericSource): ResourceMetadata;
  metric(raw: unknown, source: NumericSource, system: UnitSystem): MetricData | AbsentMetric | null;
  series(raw: unknown, source: NumericSource, system: UnitSystem, entity: unknown): SeriesData;
};
export type ResourceStatus = "loading" | "missing" | "error" | "unobserved" | "absent" | "ready";
export type Resolved<T> = {
  status: ResourceStatus; data?: T; metadata?: ResourceMetadata; error?: string; refreshFailed?: boolean; unobserved?: boolean;
};
