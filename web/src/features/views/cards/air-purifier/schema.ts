import { z } from "zod";
import { entityID, plainText } from "../../catalog/primitives";
import { choiceBindingSchema, powerBindingSchema } from "../../bindings/schema";

export const airPurifierLayouts = ["card", "tile"] as const;

/** A numeric sensor's root reading. Another numeric type sharing the unit, such as humidity in %, is not a purifier reading. */
const sensorBindingSchema = z.strictObject({
  kind: z.literal("entity-state"), entity_id: entityID,
  entity_type: z.literal("hearth.numericsensor/v1"), field: z.literal("").optional(),
}).meta({ id: "NumericSensorSource" });

export const airPurifierSchema = z.strictObject({
  layout: z.enum(airPurifierLayouts).describe("card: Power, readings, and every bound control. tile: a whole-card power press with mode and PM2.5, for grids of quick switches."),
  label: plainText(120).optional(),
  power: powerBindingSchema,
  pm25: sensorBindingSchema.describe("Numeric sensor in µg/m³ on the power Entity's Device."),
  mode: choiceBindingSchema.describe("Enum setting with 2 to 5 choices, such as auto, sleep, manual.").optional(),
  fan_level: choiceBindingSchema.describe("Enum setting whose choices are \"1\" to \"N\", N from 2 to 10.").optional(),
  display: powerBindingSchema.describe("Screen on/off. A power Entity other than the main power.").optional(),
  child_lock: powerBindingSchema.describe("Child lock on/off. A power Entity other than the main power.").optional(),
  filter_life: sensorBindingSchema.describe("Numeric sensor in % on the same Device.").optional(),
});
export type AirPurifierProps = z.infer<typeof airPurifierSchema>;

/** Role names in display order, including optional roles. */
export const airPurifierRoles = ["power", "pm25", "filter_life", "mode", "fan_level", "display", "child_lock"] as const;
export type AirPurifierRole = (typeof airPurifierRoles)[number];

/** Role bindings in display order, omitting roles that aren't bound. */
export function boundRoles(props: AirPurifierProps): Array<[AirPurifierRole, { entity_id: string; entity_type: string }]> {
  return airPurifierRoles.flatMap(role => {
    const source = props[role];
    return source ? [[role, source] as [AirPurifierRole, { entity_id: string; entity_type: string }]] : [];
  });
}
