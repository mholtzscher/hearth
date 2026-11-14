import { z } from "zod";
import { apiFetch } from "../../../api/client";
import { entityBody } from "./entity";
import { historySeriesRequest } from "./history";
import { quantityAdapter } from "./quantity-adapter";
import { quantityFormat, quantitySchema } from "./quantity";
import { isQuantityField, pointer, quantityField, resolveQuantity, stateFields } from "./state-fields";
import type { EntitySource, NumericAdapter, ReadUnits } from "./types";

const numeric = z.object({ value: z.number(), unit: z.string() });
const displayNumeric = quantitySchema.extend({ value: z.number() });
const pairedEntity = entityBody.omit({ quantities: true, state: true }).extend({
  state: z.object({
    observed_at: z.string().datetime({ offset: true }),
    observation_id: z.string().min(1).optional(),
    canonical: z.unknown(),
    display: z.unknown(),
  }).nullable(),
});
type PairedEntity = z.infer<typeof pairedEntity>;

/** Validate the complete Entity before sharing one paired response across fields. */
function readPairedEntity(raw: unknown, source: EntitySource, expectedUnit?: string): PairedEntity {
  const entity = pairedEntity.parse(raw);
  if (entity.id !== source.entity_id || entity.type !== source.entity_type) throw new Error("Entity identity/type mismatch");
  const fields = Object.prototype.hasOwnProperty.call(stateFields, entity.type) ? stateFields[entity.type] : undefined;
  if (!fields || !Object.prototype.hasOwnProperty.call(fields, source.field ?? "")) throw new Error("Unsupported State field");
  for (const [path, field] of Object.entries(fields)) {
    const unit = isQuantityField(field) ? resolveQuantity(field, entity.support).unit : undefined;
    if (!entity.state) continue;
    const canonical = pointer(entity.state.canonical, path), display = pointer(entity.state.display, path);
    if (canonical === undefined && display === undefined && field.presence === "sometimes") continue;
    if (isQuantityField(field)) {
      const working = numeric.parse(canonical);
      displayNumeric.parse(display);
      if (working.unit !== unit) throw new Error("Canonical quantity unit mismatch");
      if (path === (source.field ?? "") && expectedUnit && working.unit !== expectedUnit) throw new Error("Unexpected canonical quantity unit");
    } else {
      const schema = field.value_type === "boolean" ? z.boolean() : z.string();
      const value = schema.parse(canonical);
      if (schema.parse(display) !== value) throw new Error("Paired State fields differ");
      if (field.value_type === "string" && field.enum && !field.enum.includes(String(value))) throw new Error("Invalid State choice");
    }
  }
  return entity;
}

/** Logical canonical and display readers share one cache entry and one HTTP request. */
export function pairedQuantityAdapter(units: ReadUnits, canonicalUnit?: string): NumericAdapter {
  return {
    stateRequest: source => ({
      key: `entity-state:${source.entity_id}:paired`, kind: "state",
      load: signal => apiFetch(`/v1/entities/${encodeURIComponent(source.entity_id)}?include=canonical`, { signal },
        text => readPairedEntity(JSON.parse(text), source, canonicalUnit)),
    }),
    seriesRequest: (source, window) => {
      const field = quantityField(source);
      const unit = canonicalUnit ?? (typeof field?.quantity.unit === "string" ? field.quantity.unit : undefined);
      if (!unit) throw new Error("A paired history read requires its canonical unit");
      return historySeriesRequest(source, window, "household", true, unit);
    },
    metadata(raw, source) {
      const entity = readPairedEntity(raw, source);
      return { id: entity.id, label: entity.name + (source.field ? ` · ${source.field}` : ""),
        enabled: entity.enabled, availability: entity.availability.status };
    },
    metric(raw, source) {
      const entity = readPairedEntity(raw, source);
      if (!entity.state) return null;
      const value = pointer(units === "canonical" ? entity.state.canonical : entity.state.display, source.field ?? "");
      if (value === undefined) return { absent: true, observed_at: entity.state.observed_at };
      if (units === "canonical") {
        const reading = numeric.parse(value);
        return { value: reading.value, observed_at: entity.state.observed_at,
          format: { unit_label: reading.unit, fraction_digits: 1 } };
      }
      const reading = displayNumeric.parse(value);
      return { value: reading.value, observed_at: entity.state.observed_at, format: quantityFormat(reading),
        ...(reading.range ? { range: reading.range } : {}) };
    },
    series(data, source, raw) {
      if (!data.canonical) throw new Error("Missing paired history");
      return quantityAdapter.series(units === "canonical" ? data.canonical : data, source, raw);
    },
  };
}
