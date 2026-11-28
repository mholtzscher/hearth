import { z } from "zod";

import { textBindingSchema, wordsSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const periodTotalsPeriods = ["hour", "day"] as const;
export type PeriodTotalsPeriod = (typeof periodTotalsPeriods)[number];
export const periodTotalsStyles = ["columns", "ledger"] as const;
export type PeriodTotalsStyle = (typeof periodTotalsStyles)[number];
export const periodTotalsSchema = z.strictObject({
  source: textBindingSchema.describe(
    "A yes/no State field. Text fields are rejected."
  ),
  label: plainText(120).optional(),
  words: wordsSchema.optional(),
  period: z
    .enum(periodTotalsPeriods)
    .describe("hour: the last 24 hours. day: the last 7 days."),
  style: z
    .enum(periodTotalsStyles)
    .describe(
      "columns: one labelled bar per period. ledger: one row per period, read like a table."
    ),
});
