import { z } from "zod";
import { textBindingSchema, wordsSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const stateSchema = z.strictObject({
  source: textBindingSchema, label: plainText(120).optional(), words: wordsSchema.optional(),
});
