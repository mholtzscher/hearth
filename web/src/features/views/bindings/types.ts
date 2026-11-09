import type { ReadRequest } from "../data/cache";
import type { HistorySeries } from "./history";
import type { UnitSystem } from "../../units/units";

export type SeriesWindow = "1h" | "6h" | "24h" | "7d";
export type NumericFormat = { unit_label: string; fraction_digits: number };
/** A numeric reading. range is the field's declared extent in the displayed unit, when its type declares one. */
export type MetricData = { value: number; format: NumericFormat; observed_at: string; range?: { minimum: number; maximum: number } };
/** A yes/no or text reading, already in the words a card shows. */
export type TextData = { text: string; observed_at: string };
/** Card-chosen words for a boolean field. */
export type Words = { true: string; false: string };
export type AbsentMetric = { absent: true; observed_at: string };
export type SeriesData = { from: string; to: string; format: NumericFormat; buckets: Array<{
  start: string; end: string; min: number | null; max: number | null; mean: number | null;
}> };
export type ResourceMetadata = {
  id: string; label: string; enabled: boolean; availability: string;
};
/** One Entity State field. A numeric source selects a quantity field. */
export type EntitySource = { entity_id: string; entity_type: string; field?: string };
export type NumericSource = EntitySource;
export type NumericAdapter = {
  stateRequest(source: NumericSource): ReadRequest;
  seriesRequest(source: NumericSource, window: SeriesWindow): ReadRequest;
  metadata(raw: unknown, source: NumericSource): ResourceMetadata;
  metric(raw: unknown, source: NumericSource, system: UnitSystem): MetricData | AbsentMetric | null;
  series(data: HistorySeries, source: NumericSource, system: UnitSystem, entity: unknown): SeriesData;
};
export type ResourceStatus = "loading" | "missing" | "error" | "unobserved" | "absent" | "ready";
export type Resolved<T> = {
  status: ResourceStatus; data?: T; metadata?: ResourceMetadata; error?: string; refreshFailed?: boolean; unobserved?: boolean;
};

export type TimelineInterval =
  | { kind: "value"; from: number; to: number; text: string; truthy: boolean | null }
  | { kind: "absent"; from: number; to: number }
  | { kind: "unknown"; from: number; to: number };
export type TimelineData = {
  from: number; to: number; window: SeriesWindow; intervals: TimelineInterval[];
  totals: Array<{ text: string; truthy: boolean | null; seconds: number; entries: number }>;
};
export type WeekCell = { start: number; end: number; seconds: number | null; entries: number; future: boolean };
export type WeekPatternData = { from: number; to: number; days: Array<{ start: number; hours: WeekCell[] }> };
