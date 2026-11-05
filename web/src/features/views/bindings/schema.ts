import { z } from "zod";
import { entityID } from "../catalog/primitives";
import { fieldGuide, quantityTypes } from "./state-fields";

export const numericTypes = quantityTypes;
export const seriesWindows = ["1h", "6h", "24h", "7d"] as const;
const entitySource = {
  entity_id: entityID, entity_type: z.enum(numericTypes),
  field: z.string().max(64).regex(/^(\/[a-z][a-z0-9_]*)*$/).describe(fieldGuide).optional(),
};
export const stateBindingSchema = z.strictObject({ kind: z.literal("entity-state"), ...entitySource });
export const seriesBindingSchema = z.strictObject({
  kind: z.literal("entity-series"), ...entitySource, window: z.enum(seriesWindows),
});
export type StateBinding = z.infer<typeof stateBindingSchema>;
export type SeriesBinding = z.infer<typeof seriesBindingSchema>;
export type ReadBinding = StateBinding | SeriesBinding;
