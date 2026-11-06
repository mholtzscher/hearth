import { z } from "zod";
import { readingBindingSchema, wordsSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

/** The most rows one list shows; more readings belong in another list. */
export const MAX_LIST_ROWS = 12;
export const readingListSchema = z.strictObject({
  title: plainText(120),
  rows: z.array(z.strictObject({ source: readingBindingSchema, label: plainText(120).optional(), words: wordsSchema.optional() })).min(1).max(MAX_LIST_ROWS),
});
