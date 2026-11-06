import { z } from "zod";
import { seriesWindows, stateBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const metricSchema = z.strictObject({
  source: stateBindingSchema, label: plainText(120).optional(),
  trend: z.enum(seriesWindows).describe("Draw a small history line under the reading over this window.").optional(),
  range: z.literal(true).describe("Show the reading against its field's declared range. Only fields with a range accept it.").optional(),
});
