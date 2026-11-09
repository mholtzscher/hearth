import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { powerBindingSchema, seriesWindows, stateBindingSchema } from "../../bindings/schema";

export const toggleTileSchema = z.strictObject({
  source: powerBindingSchema,
  label: plainText(120).optional(),
  metric: z.strictObject({
    source: stateBindingSchema.describe("Explicit numeric State source from the power Entity's Device. The reading and sparkline share this source; never choose by name or list order at render time."),
    window: z.enum(seriesWindows),
    detail: z.strictObject({ source: stateBindingSchema, label: plainText(120).optional() })
      .describe("Optional additional numeric reading from the same Device, such as its total-energy counter. Display the reading, not an integration of the sparkline.").optional(),
  }).describe("Metering variant: the selected numeric reading and a gap-preserving history line. Omit for an ordinary On/Off tile.").optional(),
});
