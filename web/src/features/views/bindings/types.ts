import type { ReadRequest } from "../data/cache";

export type SeriesWindow = "1h" | "6h" | "24h" | "7d";
export type NumericFormat = { unit_label: string; fraction_digits: number };
export type MetricData = { value: number; format: NumericFormat; observed_at: string };
export type SeriesData = { from: string; to: string; format: NumericFormat; buckets: Array<{
  start: string; end: string; min: number | null; max: number | null; mean: number | null;
}> };
export type ResourceMetadata = {
  id: string; label: string; enabled: boolean; availability: string;
};
export type NumericSource = { entity_id: string; entity_type: string };
export type NumericAdapter = {
  stateRequest(source: NumericSource): ReadRequest;
  seriesRequest(source: NumericSource, window: SeriesWindow): ReadRequest;
  metadata(raw: unknown, source: NumericSource): ResourceMetadata;
  metric(raw: unknown, source: NumericSource, unit: string): MetricData | null;
  series(raw: unknown, source: NumericSource, unit: string, entity: unknown): SeriesData;
};
export type ResourceStatus = "loading" | "missing" | "error" | "unobserved" | "ready";
export type Resolved<T> = {
  status: ResourceStatus; data?: T; metadata?: ResourceMetadata; error?: string; refreshFailed?: boolean; unobserved?: boolean;
};
