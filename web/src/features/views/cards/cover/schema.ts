import { z } from "zod";
import { entityID, plainText } from "../../catalog/primitives";

export const coverLayouts = ["card", "tile"] as const;

/** A cover's whole State: its reported position and motion. */
export const coverBindingSchema = z.strictObject({
  kind: z.literal("entity-state"), entity_id: entityID,
  entity_type: z.literal("hearth.cover/v1"), field: z.literal("").optional(),
}).meta({ id: "CoverSource" });
export type CoverSource = z.infer<typeof coverBindingSchema>;

export const coverSchema = z.strictObject({
  layout: z.enum(coverLayouts).describe("card: a drawing of the cover with Open, Stop, and Close. tile: a whole-card press that opens, closes, or stops, for grids of quick switches."),
  label: plainText(120).optional(),
  source: coverBindingSchema,
  confirm_open: z.boolean().describe("Ask for a second press before Open, even at a reported 100 %. Omitted means true; set false for covers such as shades.").optional(),
});
export type CoverProps = z.infer<typeof coverSchema>;

/** Opening asks for confirmation unless the definition turns it off. */
export const confirmsOpen = (props: CoverProps) => props.confirm_open !== false;
