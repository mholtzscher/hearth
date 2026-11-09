import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { powerBindingSchema } from "../../bindings/schema";

export const MAX_TOGGLE_ROWS = 12;
export const toggleListSchema = z.strictObject({
  title: plainText(120),
  rows: z.array(z.strictObject({ source: powerBindingSchema, label: plainText(120).optional() })).min(1).max(MAX_TOGGLE_ROWS),
  all_off: z.literal(true).describe("Offer native Turn all off for supported, enabled power Entities. One user gesture requests separate Commands; it is not an atomic group operation.").optional(),
});
