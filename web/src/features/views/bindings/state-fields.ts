import { z } from "zod";
import catalog from "../../../../../contracts/state-fields/v1/state-fields.json";
import type { EntitySource } from "./types";

const reference = z.strictObject({ root: z.literal("support"), path: z.string().regex(/^(\/[a-z][a-z0-9_]*)+$/).max(64) });
const bound = z.union([z.number(), reference]);
const presence = z.enum(["always", "sometimes"]);
const numericField = z.strictObject({
  value_type: z.enum(["integer", "number"]), presence,
  quantity: z.strictObject({
    unit: z.union([z.string().min(1), reference]),
    unit_optional: z.boolean().optional(),
    range: z.strictObject({ minimum: bound, maximum: bound }).optional(),
  }),
});
const booleanField = z.strictObject({ value_type: z.literal("boolean"), presence });
const stringField = z.strictObject({ value_type: z.literal("string"), presence, enum: z.array(z.string()).optional() });
const field = z.discriminatedUnion("value_type", [numericField, booleanField, stringField]);
export type QuantityField = z.infer<typeof numericField>;
export type TextField = z.infer<typeof booleanField> | z.infer<typeof stringField>;
export type StateField = z.infer<typeof field>;

// Only generated schema facts live here. Household presentation belongs to units.
export const stateFields: Readonly<Record<string, Readonly<Record<string, StateField>>>> =
  z.strictObject({ $comment: z.string(), types: z.record(z.string(), z.record(z.string(), field)) }).parse(catalog).types;
/** Types with at least one catalogued State field: every type a reading card can bind. */
export const fieldTypes = Object.keys(stateFields).sort();
export const quantityTypes = fieldTypes.filter(type => Object.values(stateFields[type]).some(isQuantityField));
export const textTypes = fieldTypes.filter(type => Object.values(stateFields[type]).some(isTextField));

export function isQuantityField(field: StateField): field is QuantityField {
  return field.value_type === "number" || field.value_type === "integer";
}
export function isTextField(field: StateField): field is TextField {
  return field.value_type === "boolean" || field.value_type === "string";
}
/** The catalogued field a source selects, whatever its value type. */
export function stateField(source: EntitySource): StateField | undefined {
  const fields = Object.prototype.hasOwnProperty.call(stateFields, source.entity_type) ? stateFields[source.entity_type] : undefined;
  const path = source.field ?? "";
  return fields && Object.prototype.hasOwnProperty.call(fields, path) ? fields[path] : undefined;
}
export function quantityField(source: EntitySource): QuantityField | undefined {
  const selected = stateField(source);
  return selected && isQuantityField(selected) ? selected : undefined;
}
export function textField(source: EntitySource): TextField | undefined {
  const selected = stateField(source);
  return selected && isTextField(selected) ? selected : undefined;
}
export function pointer(value: unknown, path: string): unknown {
  if (path === "") return value;
  for (const token of path.slice(1).split("/")) {
    if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("Field reference traverses a non-object");
    if (!Object.prototype.hasOwnProperty.call(value, token)) return undefined;
    value = (value as Record<string, unknown>)[token];
  }
  return value;
}
export function resolveQuantity(field: QuantityField, support: unknown): { unit: string } {
  const { unit, unit_optional } = field.quantity;
  const value = typeof unit === "string" ? unit : pointer(support, unit.path);
  const code = value === undefined && unit_optional ? "1" : value;
  if (typeof code !== "string" || code.length === 0) throw new Error("Entity support has no unit");
  return { unit: code };
}

/**
 * A field's declared range in its canonical unit, or undefined when its type
 * declares none. Support that cannot bound a declared range is an error.
 */
export function resolveRange(field: QuantityField, support: unknown): { minimum: number; maximum: number } | undefined {
  const range = field.quantity.range;
  if (!range) return undefined;
  const value = (source: z.infer<typeof bound>) => typeof source === "number" ? source : z.number().parse(pointer(support, source.path));
  const minimum = value(range.minimum), maximum = value(range.maximum);
  if (!(minimum < maximum)) throw new Error("Entity support has an empty range");
  return { minimum, maximum };
}

export function quantityAxis(source: EntitySource, support: unknown): string {
  const field = quantityField(source);
  if (!field) throw new Error("Unsupported quantity field");
  return resolveQuantity(field, support).unit;
}

export const fieldGuide = "Select a numeric State field by JSON Pointer; omit for the root. " + quantityTypes.map(type => {
  const fields = Object.entries(stateFields[type]).filter((entry): entry is [string, QuantityField] => isQuantityField(entry[1]));
  return `${type}: ${fields.map(([path, field]) => {
    const unit = typeof field.quantity.unit === "string" ? field.quantity.unit : "Entity support unit";
    return `${path || '"" (root)'} (${unit}${field.presence === "sometimes" ? "; may be absent, mixed chart buckets are gaps" : ""}${field.quantity.range ? "; has a range" : ""})`;
  }).join(", ")}`;
}).join("; ") + ". Charts use arithmetic sample summaries, not time-weighted or circular means.";

export const textFieldGuide = "Select a yes/no or text State field by JSON Pointer; omit for the root. " + textTypes.map(type => {
  const fields = Object.entries(stateFields[type]).filter((entry): entry is [string, TextField] => isTextField(entry[1]));
  return `${type}: ${fields.map(([path, field]) => `${path || '"" (root)'} (${field.value_type === "boolean" ? "yes/no" : field.enum ? `one of ${field.enum.join(", ")}` : "text"})`).join(", ")}`;
}).join("; ") + ".";

export const readingFieldGuide = "Select any State field by JSON Pointer; omit for the root. Numeric fields show a reading with its unit; yes/no and text fields show words. " +
  fieldTypes.map(type => `${type}: ${Object.keys(stateFields[type]).map(path => path || '"" (root)').join(", ")}`).join("; ") + ".";
