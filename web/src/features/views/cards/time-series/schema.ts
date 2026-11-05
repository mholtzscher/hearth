import { z } from "zod";
import { seriesBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const timeSeriesSchema = z.strictObject({
  title: plainText(120),
  series: z.array(z.strictObject({ source: seriesBindingSchema, label: plainText(120).optional() })).min(1).max(8),
});
