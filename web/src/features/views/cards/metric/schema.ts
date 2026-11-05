import { z } from "zod";
import { stateBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const metricSchema = z.strictObject({
  source: stateBindingSchema, label: plainText(120).optional(),
});
