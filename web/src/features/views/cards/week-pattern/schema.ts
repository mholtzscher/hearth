import { z } from "zod";
import { textBindingSchema, wordsSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const weekPatternSchema = z.strictObject({
  source: textBindingSchema.describe("A yes/no State field. Text fields are rejected."),
  label: plainText(120).optional(),
  words: wordsSchema.optional(),
});
