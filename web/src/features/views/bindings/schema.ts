import { z } from "zod";
import { entityID } from "../catalog/primitives";
import { readingTypes } from "./readings";

export const numericTypes = readingTypes;
export const seriesWindows = ["1h", "6h", "24h", "7d"] as const;
const entitySource = { entity_id: entityID, entity_type: z.enum(numericTypes) };
export const stateBindingSchema = z.strictObject({ kind: z.literal("entity-state"), ...entitySource });
export const seriesBindingSchema = z.strictObject({
  kind: z.literal("entity-series"), ...entitySource, window: z.enum(seriesWindows),
});
export type StateBinding = z.infer<typeof stateBindingSchema>;
export type SeriesBinding = z.infer<typeof seriesBindingSchema>;
export type ReadBinding = StateBinding | SeriesBinding;
