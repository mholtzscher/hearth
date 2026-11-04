import { z } from "zod";
import { entityID } from "../catalog/primitives";
import { TEMPERATURE_TYPE, nativeNumericTypes } from "./types";

export const numericTypes = [TEMPERATURE_TYPE, ...nativeNumericTypes] as const;
export const seriesWindows = ["1h", "6h", "24h", "7d"] as const;
const entitySource = { entity_id: entityID, entity_type: z.enum(numericTypes) };
export const stateBindingSchema = z.strictObject({ kind: z.literal("entity-state"), ...entitySource });
export const seriesBindingSchema = z.strictObject({
  kind: z.literal("entity-series"), ...entitySource, window: z.enum(seriesWindows),
});
export const displaySchema = z.union([
  z.strictObject({ unit: z.enum(["celsius", "fahrenheit"]) }),
  z.strictObject({ unit: z.literal("native") }),
]);
export type StateBinding = z.infer<typeof stateBindingSchema>;
export type SeriesBinding = z.infer<typeof seriesBindingSchema>;
export type ReadBinding = StateBinding | SeriesBinding;
export type NumericDisplay = z.infer<typeof displaySchema>;
