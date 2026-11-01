import { z } from "zod";
import { stateBindingSchema, displaySchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const metricSchema = z.strictObject({
  source: stateBindingSchema, label: plainText(120).optional(), display: displaySchema,
});
