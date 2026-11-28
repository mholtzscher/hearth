import { z } from "zod";

import { stateBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const weekGridSchema = z.strictObject({
  source: stateBindingSchema.describe("A numeric State field."),
  label: plainText(120).optional(),
});
