import { z } from "zod";

import catalog from "../../../../../contracts/state-fields/v1/state-fields.json";
import type { EntitySource } from "./types";

const reference = z.strictObject({
  root: z.literal("support"),
  path: z
    .string()
    .regex(/^(?:\/[a-z][a-z0-9_]*)+$/u)
    .max(64),
});
const bound = z.union([z.number(), reference]);
const presence = z.enum(["always", "sometimes"]);
const numericField = z.strictObject({
  value_type: z.enum(["integer", "number"]),
  presence,
  quantity: z.strictObject({
    unit: z.union([z.string().min(1), reference]),
    quantity_kind: z.union([z.string().min(1), reference]).optional(),
    quantity_kind_optional: z.boolean().optional(),
    unit_optional: z.boolean().optional(),
    range: z.strictObject({ minimum: bound, maximum: bound }).optional(),
  }),
});
const booleanField = z.strictObject({
  value_type: z.literal("boolean"),
  presence,
});
const stringField = z.strictObject({
  value_type: z.literal("string"),
  presence,
  enum: z.array(z.string()).optional(),
});
const stateFieldSchema = z.discriminatedUnion("value_type", [
  numericField,
  booleanField,
  stringField,
]);
export type QuantityField = z.infer<typeof numericField>;
export type TextField =
  | z.infer<typeof booleanField>
  | z.infer<typeof stringField>;
export type StateField = z.infer<typeof stateFieldSchema>;

const catalogSchema = z.strictObject({
  $comment: z.string(),
  types: z.record(z.string(), z.record(z.string(), stateFieldSchema)),
});
/** Strictly parse generated catalog facts, independently of household presentation. */
export const parseStateFields = (
  raw: unknown
): Readonly<Record<string, Readonly<Record<string, StateField>>>> =>
  catalogSchema.parse(raw).types;
export const stateFields = parseStateFields(catalog);
export const isQuantityField = (field: StateField): field is QuantityField =>
  field.value_type === "number" || field.value_type === "integer";
export const isTextField = (field: StateField): field is TextField =>
  field.value_type === "boolean" || field.value_type === "string";
/** Types with at least one catalogued State field: every type a reading card can bind. */
export const fieldTypes = Object.keys(stateFields).toSorted();
export const quantityTypes = fieldTypes.filter((type) =>
  Object.values(stateFields[type]).some(isQuantityField)
);
export const textTypes = fieldTypes.filter((type) =>
  Object.values(stateFields[type]).some(isTextField)
);

/** The catalogued field a source selects, whatever its value type. */
export const stateField = (source: EntitySource): StateField | undefined => {
  const fields = Object.hasOwn(stateFields, source.entity_type)
    ? stateFields[source.entity_type]
    : undefined;
  const path = source.field ?? "";
  return fields && Object.hasOwn(fields, path) ? fields[path] : undefined;
};
export const quantityField = (
  source: EntitySource
): QuantityField | undefined => {
  const selected = stateField(source);
  return selected && isQuantityField(selected) ? selected : undefined;
};
export const textField = (source: EntitySource): TextField | undefined => {
  const selected = stateField(source);
  return selected && isTextField(selected) ? selected : undefined;
};
export const pointer = (value: unknown, path: string): unknown => {
  if (path === "") {
    return value;
  }
  let current = value;
  for (const token of path.slice(1).split("/")) {
    if (
      current === null ||
      typeof current !== "object" ||
      Array.isArray(current)
    ) {
      throw new Error("Field reference traverses a non-object");
    }
    if (!Object.hasOwn(current, token)) {
      return undefined;
    }
    current = (current as Record<string, unknown>)[token];
  }
  return current;
};
export const resolveQuantity = (
  field: QuantityField,
  support: unknown
): { unit: string } => {
  const { unit, unit_optional } = field.quantity;
  const value = typeof unit === "string" ? unit : pointer(support, unit.path);
  const code = value === undefined && unit_optional ? "1" : value;
  if (typeof code !== "string" || code.length === 0) {
    throw new Error("Entity support has no unit");
  }
  return { unit: code };
};

export const quantityAxis = (
  source: EntitySource,
  support: unknown
): string => {
  const field = quantityField(source);
  if (!field) {
    throw new Error("Unsupported quantity field");
  }
  return resolveQuantity(field, support).unit;
};

const textFieldDescription = (field: TextField): string => {
  if (field.value_type === "boolean") {
    return "yes/no";
  }
  if (field.enum) {
    return `one of ${field.enum.join(", ")}`;
  }
  return "text";
};

export const fieldGuide = `Select a numeric State field by JSON Pointer; omit for the root. ${quantityTypes
  .map((type) => {
    const fields = Object.entries(stateFields[type]).filter(
      (entry): entry is [string, QuantityField] => isQuantityField(entry[1])
    );
    return `${type}: ${fields
      .map(([path, field]) => {
        const unit =
          typeof field.quantity.unit === "string"
            ? field.quantity.unit
            : "Entity support unit";
        return `${path || '"" (root)'} (${unit}${field.presence === "sometimes" ? "; may be absent, mixed chart buckets are gaps" : ""}${field.quantity.range ? "; has a range" : ""})`;
      })
      .join(", ")}`;
  })
  .join(
    "; "
  )}. Charts use arithmetic sample summaries, not time-weighted or circular means.`;

export const textFieldGuide = `Select a yes/no or text State field by JSON Pointer; omit for the root. ${textTypes
  .map((type) => {
    const fields = Object.entries(stateFields[type]).filter(
      (entry): entry is [string, TextField] => isTextField(entry[1])
    );
    return `${type}: ${fields.map(([path, field]) => `${path || '"" (root)'} (${textFieldDescription(field)})`).join(", ")}`;
  })
  .join("; ")}.`;

export const readingFieldGuide = `Select any State field by JSON Pointer; omit for the root. Numeric fields show a reading with its unit; yes/no and text fields show words. ${fieldTypes
  .map(
    (type) =>
      `${type}: ${Object.keys(stateFields[type])
        .map((path) => path || '"" (root)')
        .join(", ")}`
  )
  .join("; ")}.`;

/**
 * Fields that are set, not measured. Such a field holds its last value until
 * the next change, so a chart draws it as a step line.
 */
export const isHeldField = (entityType: string, field: string): boolean =>
  entityType === "hearth.numericsetting/v1" ||
  entityType === "hearth.brightness/v1" ||
  (entityType === "hearth.thermostat/v1" && field.startsWith("/target_"));
