import { z } from "zod";
import { entityID } from "../catalog/primitives";

const entitySource = { entity_id: entityID, entity_type: z.literal("hearth.temperature/v1") };
export const stateBindingSchema = z.strictObject({ kind: z.literal("entity-state"), ...entitySource });
export const seriesBindingSchema = z.strictObject({
  kind: z.literal("entity-series"), ...entitySource, window: z.enum(["6h", "24h"]),
});
export const displaySchema = z.strictObject({ unit: z.enum(["celsius", "fahrenheit"]) });
export type StateBinding = z.infer<typeof stateBindingSchema>;
export type SeriesBinding = z.infer<typeof seriesBindingSchema>;
export type ReadBinding = StateBinding | SeriesBinding;
export type NumericDisplay = z.infer<typeof displaySchema>;
