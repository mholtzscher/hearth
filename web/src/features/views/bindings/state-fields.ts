import { z } from "zod";
import catalog from "../../../../../contracts/state-fields/v1/state-fields.json";
import type { NumericSource } from "./types";

const reference = z.strictObject({ root: z.literal("support"), path: z.string().regex(/^(\/[a-z][a-z0-9_]*)+$/).max(64) });
const presence = z.enum(["always", "sometimes"]);
const numericField = z.strictObject({
  value_type: z.enum(["integer", "number"]), presence,
  quantity: z.strictObject({
    unit: z.union([z.string().min(1), reference]),
    unit_optional: z.boolean().optional(),
    percent_of: reference.optional(),
  }),
});
const field = z.discriminatedUnion("value_type", [
  numericField,
  z.strictObject({ value_type: z.literal("boolean"), presence }),
  z.strictObject({ value_type: z.literal("string"), presence, enum: z.array(z.string()).optional() }),
]);
export type QuantityField = z.infer<typeof numericField>;
export type StateField = z.infer<typeof field>;

// Only generated schema facts live here. Household presentation belongs to units.
export const stateFields: Readonly<Record<string, Readonly<Record<string, StateField>>>> =
  z.strictObject({ $comment: z.string(), types: z.record(z.string(), z.record(z.string(), field)) }).parse(catalog).types;
export const quantityTypes = Object.keys(stateFields).filter(type => Object.values(stateFields[type]).some(isQuantityField)).sort();

export function isQuantityField(field: StateField): field is QuantityField {
  return field.value_type === "number" || field.value_type === "integer";
}
export function quantityField(source: NumericSource): QuantityField | undefined {
  const fields = Object.prototype.hasOwnProperty.call(stateFields, source.entity_type) ? stateFields[source.entity_type] : undefined;
  const path = source.field ?? "";
  const selected = fields && Object.prototype.hasOwnProperty.call(fields, path) ? fields[path] : undefined;
  return selected && isQuantityField(selected) ? selected : undefined;
}
export function pointer(value: unknown, path: string): unknown {
  return path === "" ? value : path.slice(1).split("/").reduce<unknown>((current, token) =>
    current !== null && typeof current === "object" && Object.prototype.hasOwnProperty.call(current, token)
      ? (current as Record<string, unknown>)[token] : undefined, value);
}
export function resolveQuantity(field: QuantityField, support: unknown): { unit: string; maximum?: number } {
  const { unit, unit_optional, percent_of } = field.quantity;
  const value = typeof unit === "string" ? unit : pointer(support, unit.path);
  const code = value === undefined && unit_optional ? "1" : value;
  if (typeof code !== "string" || code.length === 0) throw new Error("Entity support has no unit");
  if (!percent_of) return { unit: code };
  const maximum = pointer(support, percent_of.path);
  if (typeof maximum !== "number" || !Number.isFinite(maximum) || maximum <= 0) throw new Error("Entity support has no positive percentage scale");
  return { unit: code, maximum };
}

export function quantityAxis(source: NumericSource, support: unknown): string {
  const field = quantityField(source);
  if (!field) throw new Error("Unsupported quantity field");
  const quantity = resolveQuantity(field, support);
  return quantity.maximum === undefined ? quantity.unit : "%";
}

export const fieldGuide = "Select a State field by JSON Pointer; omit for the root. " + quantityTypes.map(type => {
  const fields = Object.entries(stateFields[type]).filter((entry): entry is [string, QuantityField] => isQuantityField(entry[1]));
  return `${type}: ${fields.map(([path, field]) => {
    const unit = field.quantity.percent_of ? "% of Entity maximum" : typeof field.quantity.unit === "string" ? field.quantity.unit : "Entity support unit";
    return `${path || '"" (root)'} (${unit}${field.presence === "sometimes" ? "; may be absent, mixed chart buckets are gaps" : ""})`;
  }).join(", ")}`;
}).join("; ") + ". Charts use arithmetic sample summaries, not time-weighted or circular means.";
