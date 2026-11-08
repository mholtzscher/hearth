import { z } from "zod";
import { historySeriesRequest } from "./history";
import { entityMetadata, entityStateRequest, readEntity, type FieldEntity } from "./entity";
import { pointer, quantityField, resolveQuantity, resolveRange } from "./state-fields";
import type { MetricData, NumericAdapter, NumericFormat, NumericSource, SeriesData } from "./types";
import { displayUnit, type UnitSystem } from "../../units/units";

function readQuantityEntity(raw: unknown, source: NumericSource): FieldEntity {
  const data = readEntity(raw, source);
  if (!quantityField(source)) throw new Error("Unsupported quantity field");
  return data;
}

function present(data: FieldEntity, source: NumericSource, system: UnitSystem): { format: NumericFormat; convert: (value: number) => number } {
  const field = quantityField(source);
  if (!field) throw new Error("Unsupported quantity field");
  const { unit } = resolveQuantity(field, data.support);
  const display = displayUnit(unit, field.value_type, system);
  return { format: { unit_label: display.label, fraction_digits: display.fraction_digits }, convert: display.convert };
}

/** The field's declared range in the displayed unit. Support that cannot bound it shows no range. */
function displayRange(data: FieldEntity, source: NumericSource, convert: (value: number) => number): MetricData["range"] {
  const field = quantityField(source);
  if (!field) return undefined;
  try {
    const range = resolveRange(field, data.support);
    return range && { minimum: convert(range.minimum), maximum: convert(range.maximum) };
  } catch {
    return undefined;
  }
}

export const quantityAdapter: NumericAdapter = {
  stateRequest: source => entityStateRequest(source),
  seriesRequest: (source, window) => historySeriesRequest(source, window),
  metadata: (raw, source) => entityMetadata(readQuantityEntity(raw, source), source),
  metric(raw, source, system) {
    const data = readQuantityEntity(raw, source);
    if (!data.state) return null;
    const value = pointer(data.state.value, source.field ?? "");
    if (value === undefined) return { absent: true, observed_at: data.state.observed_at };
    const { format, convert } = present(data, source, system);
    const range = displayRange(data, source, convert);
    return { value: convert(z.number().parse(value)), observed_at: data.state.observed_at, format, ...(range ? { range } : {}) };
  },
  series(data, source, system, entityRaw) {
    const { format, convert } = present(readQuantityEntity(entityRaw, source), source, system);
    const scaled = (value: number | null) => value === null ? null : convert(value);
    return { from: data.from, to: data.to, format, buckets: data.buckets.map(
      bucket => ({ start: bucket.start, end: bucket.end, min: scaled(bucket.min), max: scaled(bucket.max), mean: scaled(bucket.mean) }),
    ) } satisfies SeriesData;
  },
};
