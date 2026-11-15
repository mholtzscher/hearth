import { z } from "zod";

/** Formatting and bounds describe the numbers delivered alongside this metadata. */
export const quantitySchema = z.object({
  unit: z.string(),
  label: z.string(),
  fraction_digits: z.number().int().min(0).max(100),
  range: z.object({ minimum: z.number(), maximum: z.number() }).optional(),
});
export type Quantity = z.infer<typeof quantitySchema>;

export const quantityFormat = (quantity: Quantity) => ({
  unit_label: quantity.label,
  fraction_digits: quantity.fraction_digits,
});
