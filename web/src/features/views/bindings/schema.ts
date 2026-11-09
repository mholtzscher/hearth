import { z } from "zod";
import { deviceID, entityID, plainText } from "../catalog/primitives";
import { fieldGuide, fieldTypes, quantityTypes, readingFieldGuide, textFieldGuide, textTypes } from "./state-fields";

export const numericTypes = quantityTypes;
export const seriesWindows = ["1h", "6h", "24h", "7d"] as const;
const fieldPointer = z.string().max(64).regex(/^(\/[a-z][a-z0-9_]*)*$/);
const entitySource = {
  entity_id: entityID, entity_type: z.enum(numericTypes),
  field: fieldPointer.describe(fieldGuide).optional(),
};
export const stateBindingSchema = z.strictObject({ kind: z.literal("entity-state"), ...entitySource });
export const seriesBindingSchema = z.strictObject({
  kind: z.literal("entity-series"), ...entitySource, window: z.enum(seriesWindows),
});
/** A yes/no or text State field, shown in words. */
export const textBindingSchema = z.strictObject({
  kind: z.literal("entity-state"), entity_id: entityID, entity_type: z.enum(textTypes),
  field: fieldPointer.describe(textFieldGuide).optional(),
});
/** Any State field: a numeric reading with its unit, or words. */
export const readingBindingSchema = z.strictObject({
  kind: z.literal("entity-state"), entity_id: entityID, entity_type: z.enum(fieldTypes),
  field: fieldPointer.describe(readingFieldGuide).optional(),
});
/** A Device and whichever readable Entities it has when the view is shown. */
export const deviceBindingSchema = z.strictObject({ kind: z.literal("device"), device_id: deviceID });
/** Only a power Entity's root boolean has the power.set Command contract. */
export const powerBindingSchema = z.strictObject({
  kind: z.literal("entity-state"), entity_id: entityID,
  entity_type: z.literal("hearth.power/v1"), field: z.literal("").optional(),
});
export type PowerSource = z.infer<typeof powerBindingSchema>;
/** Words for a yes/no field, such as Open and Closed for a door contact. Text fields ignore them. */
export const wordsSchema = z.strictObject({ true: plainText(40), false: plainText(40) })
  .describe("Words for a yes/no field's true and false values, such as Open and Closed for a door contact. Only yes/no fields accept words.");
export type StateBinding = z.infer<typeof stateBindingSchema>;
export type SeriesBinding = z.infer<typeof seriesBindingSchema>;
export type ReadBinding = StateBinding | SeriesBinding;
