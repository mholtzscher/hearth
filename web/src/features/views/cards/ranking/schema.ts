import { z } from "zod";

import { seriesWindows, stateBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

/** The most rows one ranking shows; more readings belong in another card. */
export const MAX_RANKING_ROWS = 12;
export const rankingSchema = z.strictObject({
  title: plainText(120),
  rows: z
    .array(
      z.strictObject({
        source: stateBindingSchema,
        label: plainText(120).optional(),
      })
    )
    .min(2)
    .max(MAX_RANKING_ROWS)
    .describe("Distinct numeric State fields in one canonical unit."),
  range: z
    .enum(seriesWindows)
    .describe("Also show each row's minimum and maximum over this window.")
    .optional(),
});
