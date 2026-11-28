import type { ReadRequest } from "../data/cache";
import type { HistorySeries } from "./history";

export type SeriesWindow = "1h" | "6h" | "24h" | "7d";
export interface NumericFormat {
  unit_label: string;
  fraction_digits: number;
}
/** A numeric reading. range is the field's declared extent in the displayed unit, when its type declares one. */
export interface MetricData {
  value: number;
  format: NumericFormat;
  observed_at: string;
  range?: { minimum: number; maximum: number };
}
/** A yes/no or text reading, already in the words a card shows. */
export interface TextData {
  text: string;
  observed_at: string;
}
/** Card-chosen words for a boolean field. */
export interface Words {
  true: string;
  false: string;
}
export interface AbsentMetric {
  absent: true;
  observed_at: string;
}
export interface SeriesData {
  from: string;
  to: string;
  unit?: string;
  format: NumericFormat;
  buckets: {
    start: string;
    end: string;
    min: number | null;
    max: number | null;
    mean: number | null;
  }[];
}
export interface ResourceMetadata {
  id: string;
  label: string;
  enabled: boolean;
  availability: string;
}
/** One Entity State field. A numeric source selects a quantity field. */
export interface EntitySource {
  entity_id: string;
  entity_type: string;
  field?: string;
}
export type NumericSource = EntitySource;
/** Household units for display; canonical units for formulas with fixed working units. */
export type ReadUnits = "household" | "canonical";
export interface NumericAdapter {
  stateRequest: (source: NumericSource, units?: ReadUnits) => ReadRequest;
  seriesRequest: (
    source: NumericSource,
    window: SeriesWindow,
    units?: ReadUnits
  ) => ReadRequest;
  metadata: (raw: unknown, source: NumericSource) => ResourceMetadata;
  metric: (
    raw: unknown,
    source: NumericSource
  ) => MetricData | AbsentMetric | null;
  series: (
    data: HistorySeries,
    source: NumericSource,
    entity: unknown
  ) => SeriesData;
}
export type ResourceStatus =
  | "loading"
  | "missing"
  | "error"
  | "unobserved"
  | "absent"
  | "ready";
export interface Resolved<T> {
  status: ResourceStatus;
  data?: T;
  metadata?: ResourceMetadata;
  error?: string;
  refreshFailed?: boolean;
  unobserved?: boolean;
}

export type TimelineInterval =
  | {
      kind: "value";
      from: number;
      to: number;
      text: string;
      truthy: boolean | null;
    }
  | { kind: "absent"; from: number; to: number }
  | { kind: "unknown"; from: number; to: number };
export interface TimelineData {
  from: number;
  to: number;
  window: SeriesWindow;
  intervals: TimelineInterval[];
  totals: {
    text: string;
    truthy: boolean | null;
    seconds: number;
    entries: number;
  }[];
}
export interface WeekCell {
  start: number;
  end: number;
  seconds: number | null;
  entries: number;
  future: boolean;
}
export interface WeekPatternData {
  from: number;
  to: number;
  days: { start: number; hours: WeekCell[] }[];
}
/** One period's seconds true. `seconds` is null when the period has no history, which is not zero. */
export interface PeriodTotal {
  start: number;
  end: number;
  seconds: number | null;
  /** The current period, still being counted. */
  partial: boolean;
  /** Part of the period has no history. */
  gaps: boolean;
}
export interface PeriodTotalsData {
  from: number;
  to: number;
  periods: PeriodTotal[];
}
