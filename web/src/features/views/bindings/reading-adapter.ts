import { z } from "zod";
import { apiFetch } from "../../../api/client";
import { historySeriesRequest, historySeriesSchema } from "./history";
import { readingTypes, readings } from "./readings";
import type { NumericAdapter, NumericFormat, NumericSource, SeriesData } from "./types";
import { displayUnit, type UnitSystem } from "../../units/units";

const timestamp = z.string().datetime({ offset: true });
const entity = z.object({
  id: z.string(), type: z.enum(readingTypes), name: z.string(), enabled: z.boolean(),
  availability: z.object({ status: z.string() }),
  // Only per-Entity units read support; fixed-unit readings tolerate its absence.
  support: z.unknown().optional(),
  state: z.object({ value: z.number(), observed_at: timestamp }).nullable(),
});
type ReadingEntity = z.infer<typeof entity>;

function readEntity(raw: unknown, source: NumericSource): ReadingEntity {
  const parsed = entity.parse(raw);
  if (parsed.id !== source.entity_id || parsed.type !== source.entity_type) throw new Error("Entity identity/type mismatch");
  return parsed;
}

function pointer(value: unknown, path: string): unknown {
  return path.split("/").slice(1).map(token => token.split("~1").join("/").split("~0").join("~")).reduce<unknown>(
    (current, token) => current !== null && typeof current === "object" && Object.prototype.hasOwnProperty.call(current, token) ? (current as Record<string, unknown>)[token] : undefined,
    value,
  );
}

function present(data: ReadingEntity, system: UnitSystem): { format: NumericFormat; convert: (value: number) => number } {
  const reading = readings[data.type];
  if (!reading) throw new Error("Unsupported reading");
  const unit = "support_path" in reading.unit ? pointer(data.support, reading.unit.support_path) : reading.unit.fixed;
  if (typeof unit !== "string" || unit === "") throw new Error("Entity support has no unit");
  const display = displayUnit(unit, reading.value_type, system);
  return { format: { unit_label: display.label, fraction_digits: display.fraction_digits }, convert: display.convert };
}

// One adapter serves every reading type: the generated catalog supplies the
// canonical unit and the units module converts it for the household.
export const readingAdapter: NumericAdapter = {
  stateRequest: source => ({ key: `entity-state:${source.entity_id}`, kind: "state",
    load: signal => apiFetch(`/v1/entities/${encodeURIComponent(source.entity_id)}`, { signal }),
  }),
  seriesRequest: (source, window) => historySeriesRequest(source, window),
  metadata(raw, source) {
    const data = readEntity(raw, source);
    return { id: data.id, label: data.name, enabled: data.enabled, availability: data.availability.status };
  },
  metric(raw, source, system) {
    const data = readEntity(raw, source);
    const { format, convert } = present(data, system);
    return data.state ? { value: convert(data.state.value), observed_at: data.state.observed_at, format } : null;
  },
  series(raw, source, system, entityRaw) {
    const { format, convert } = present(readEntity(entityRaw, source), system);
    const data = historySeriesSchema.parse(raw);
    const scaled = (value: number | null) => value === null ? null : convert(value);
    return { from: data.from, to: data.to, format, buckets: data.buckets.map(
      bucket => ({ start: bucket.start, end: bucket.end, min: scaled(bucket.min), max: scaled(bucket.max), mean: scaled(bucket.mean) }),
    ) } satisfies SeriesData;
  },
};
