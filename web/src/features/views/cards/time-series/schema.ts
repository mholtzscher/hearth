import { z } from "zod";

import { seriesBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const timeSeriesSchema = z.strictObject({
  title: plainText(120),
  series: z
    .array(
      z.strictObject({
        source: seriesBindingSchema,
        label: plainText(120).optional(),
      })
    )
    .min(1)
    .max(8),
  compare: z
    .literal("typical")
    .describe(
      "Draw today from local midnight over the last 7 days' range for each time of day. Requires exactly one series with window 24h."
    )
    .optional(),
});
