import { z } from "zod";

import { seriesWindows, stateBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const timeInRangeSchema = z.strictObject({
  source: stateBindingSchema.describe("A numeric State field."),
  label: plainText(120).optional(),
  window: z.enum(seriesWindows),
  thresholds: z
    .array(z.number())
    .min(1)
    .max(2)
    .describe(
      "One or two ascending thresholds in the field's canonical unit. One splits the window into below and above; two add the range between them."
    ),
});
