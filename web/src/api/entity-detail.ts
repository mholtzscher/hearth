import { z } from "zod";
import type { Entity } from "./types";

const quantity = z.object({
  value: z.number(), unit: z.string(),
  label: z.string(), fraction_digits: z.number().int().min(0).max(100),
  range: z.object({ minimum: z.number(), maximum: z.number() }).optional(),
});
const canonicalQuantity = quantity.pick({ value: true, unit: true });
export const entityDetailSchema = z.object({
  id: z.string(), type: z.string(), name: z.string(), enabled: z.boolean(),
  device_id: z.string(), adapter_id: z.string(), adapter_name: z.string(),
  name_override: z.string().nullable(), support: z.record(z.string(), z.unknown()),
  availability: z.object({
    status: z.string(), source: z.string(), since: z.string(),
    evidence_at: z.string(), source_observed_at: z.string().optional(),
    reason: z.object({ code: z.string() }).optional(),
  }),
  state: z.object({
    canonical: z.unknown(), display: z.unknown(),
    observed_at: z.string().datetime({ offset: true }),
    observation_id: z.string().min(1),
    adapter_received_at: z.string().datetime({ offset: true }),
    source_updated_at: z.string().datetime({ offset: true }).optional(),
  }).nullable(),
}).passthrough();

/** Internal projections reuse the collection/Device model; the wire detail is paired. */
export type EntityRead = Entity & { canonical: Entity };

export function decodeEntityDetail(raw: unknown): EntityRead {
  const parsed = entityDetailSchema.parse(raw);
  const displayQuantities: NonNullable<Entity["quantities"]> = {};
  const canonicalQuantities: NonNullable<Entity["quantities"]> = {};
  function project(canonical: unknown, display: unknown, path: string): [unknown, unknown] {
    if (typeof canonical === "object" && canonical !== null && "value" in canonical && "unit" in canonical) {
      const working = canonicalQuantity.parse(canonical), shown = quantity.parse(display);
      canonicalQuantities[path] = { unit: working.unit, label: working.unit, fraction_digits: 1 };
      const { value, ...format } = shown;
      displayQuantities[path] = format;
      return [working.value, value];
    }
    if (Array.isArray(canonical)) {
      if (!Array.isArray(display) || canonical.length !== display.length) throw new Error("Paired arrays differ");
      const values = canonical.map((value, index) => project(value, display[index], `${path}/${index}`));
      return [values.map(value => value[0]), values.map(value => value[1])];
    }
    if (typeof canonical === "object" && canonical !== null) {
      if (typeof display !== "object" || display === null || Array.isArray(display)) throw new Error("Paired fields differ");
      const shown = display as Record<string, unknown>;
      const entries = Object.entries(canonical);
      if (entries.length !== Object.keys(shown).length || entries.some(([key]) => !Object.prototype.hasOwnProperty.call(shown, key))) throw new Error("Paired fields differ");
      const values = entries.map(([key, value]) => [key, project(value, shown[key], `${path}/${key.replace(/~/g, "~0").replace(/\//g, "~1")}`)] as const);
      return [Object.fromEntries(values.map(([key, value]) => [key, value[0]])), Object.fromEntries(values.map(([key, value]) => [key, value[1]]))];
    }
    if ((typeof canonical !== "string" && typeof canonical !== "boolean") || canonical !== display) throw new Error("Invalid paired State leaf");
    return [canonical, display];
  }
  const { state, ...metadata } = parsed;
  if (!state) {
    const entity = { ...metadata, state: null };
    return { ...entity, canonical: entity };
  }
  const { canonical, display, ...evidence } = state;
  const [working, shown] = project(canonical, display, "");
  return {
    ...metadata, quantities: displayQuantities, state: { ...evidence, value: shown },
    canonical: { ...metadata, quantities: canonicalQuantities, state: { ...evidence, value: working } },
  };
}
