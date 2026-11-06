import { z } from "zod";
import { apiFetch } from "../../../api/client";
import { historySeriesRequest, historySeriesSchema } from "./history";
import { isQuantityField, pointer, quantityField, quantityTypes, resolveQuantity, stateFields } from "./state-fields";
import type { NumericAdapter, NumericFormat, NumericSource, SeriesData } from "./types";
import { displayUnit, type UnitSystem } from "../../units/units";

const entity = z.object({
  id: z.string(), type: z.enum(quantityTypes), name: z.string(), enabled: z.boolean(),
  availability: z.object({ status: z.string() }), support: z.unknown().optional(),
  state: z.object({ value: z.unknown(), observed_at: z.string().datetime({ offset: true }) }).nullable(),
});
type QuantityEntity = z.infer<typeof entity>;

function readEntity(raw: unknown, source: NumericSource): QuantityEntity {
  const parsed = entity.parse(raw);
  if (parsed.id !== source.entity_id || parsed.type !== source.entity_type) throw new Error("Entity identity/type mismatch");
  if (!quantityField(source)) throw new Error("Unsupported quantity field");
  // Validate every field before the shared Entity request enters the cache,
  // regardless of which card requested it first.
  for (const [path, field] of Object.entries(stateFields[parsed.type])) {
    if (isQuantityField(field)) resolveQuantity(field, parsed.support);
    if (!parsed.state) continue;
    const value = pointer(parsed.state.value, path);
    if (value === undefined && field.presence === "sometimes") continue;
    switch (field.value_type) {
      case "integer": z.number().int().parse(value); break;
      case "number": z.number().parse(value); break;
      case "boolean": z.boolean().parse(value); break;
      case "string":
        z.string().parse(value);
        if (field.enum && !field.enum.includes(value as string)) throw new Error("Invalid State choice");
        break;
    }
  }
  return parsed;
}

function present(data: QuantityEntity, source: NumericSource, system: UnitSystem): { format: NumericFormat; convert: (value: number) => number } {
  const field = quantityField(source);
  if (!field) throw new Error("Unsupported quantity field");
  const { unit } = resolveQuantity(field, data.support);
  const display = displayUnit(unit, field.value_type, system);
  return { format: { unit_label: display.label, fraction_digits: display.fraction_digits }, convert: display.convert };
}

export const quantityAdapter: NumericAdapter = {
  stateRequest: source => ({ key: `entity-state:${source.entity_id}`, kind: "state",
    load: signal => apiFetch(`/v1/entities/${encodeURIComponent(source.entity_id)}`, { signal }, text => readEntity(JSON.parse(text), source)),
  }),
  seriesRequest: (source, window) => historySeriesRequest(source, window),
  metadata(raw, source) {
    const data = readEntity(raw, source);
    return { id: data.id, label: data.name + (source.field ? ` · ${source.field}` : ""), enabled: data.enabled, availability: data.availability.status };
  },
  metric(raw, source, system) {
    const data = readEntity(raw, source);
    if (!data.state) return null;
    const value = pointer(data.state.value, source.field ?? "");
    if (value === undefined) return { absent: true, observed_at: data.state.observed_at };
    const { format, convert } = present(data, source, system);
    return { value: convert(z.number().parse(value)), observed_at: data.state.observed_at, format };
  },
  series(raw, source, system, entityRaw) {
    const entity = readEntity(entityRaw, source);
    const { format, convert } = present(entity, source, system);
    const data = historySeriesSchema.parse(raw);
    const scaled = (value: number | null) => value === null ? null : convert(value);
    return { from: data.from, to: data.to, format, observed_at: entity.state?.observed_at, buckets: data.buckets.map(
      bucket => ({ start: bucket.start, end: bucket.end, min: scaled(bucket.min), max: scaled(bucket.max), mean: scaled(bucket.mean) }),
    ) } satisfies SeriesData;
  },
};
