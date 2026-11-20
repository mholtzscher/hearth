import { z } from "zod";

import { entityID, plainText } from "../../catalog/primitives";

/** A thermostat's whole State: mode, activity, readings, and targets. */
export const thermostatBindingSchema = z
  .strictObject({
    kind: z.literal("entity-state"),
    entity_id: entityID,
    entity_type: z.literal("hearth.thermostat/v1"),
    field: z.literal("").optional(),
  })
  .meta({ id: "ThermostatSource" });
export type ThermostatSource = z.infer<typeof thermostatBindingSchema>;

export const thermostatSchema = z.strictObject({
  label: plainText(120).optional(),
  source: thermostatBindingSchema,
});
export type ThermostatProps = z.infer<typeof thermostatSchema>;
