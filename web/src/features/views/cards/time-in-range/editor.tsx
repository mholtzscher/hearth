import type { z } from "zod";

import type { timeInRangeSchema } from "./schema";

/** Placeholder until the editor is implemented. */
export const TimeInRangeEditor = (_props: {
  value: z.infer<typeof timeInRangeSchema>;
  onChange: (value: z.infer<typeof timeInRangeSchema>) => void;
}) => null;
