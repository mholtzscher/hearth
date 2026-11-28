import type { z } from "zod";

import type { periodTotalsSchema } from "./schema";

/** Placeholder until the editor is implemented. */
export const PeriodTotalsEditor = (_props: {
  value: z.infer<typeof periodTotalsSchema>;
  onChange: (value: z.infer<typeof periodTotalsSchema>) => void;
}) => null;
