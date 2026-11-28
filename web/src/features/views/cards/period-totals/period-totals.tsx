import type { z } from "zod";

import { DataCard } from "../data-card";
import type { periodTotalsSchema } from "./schema";

/** Placeholder until the card is implemented. */
export const BoundPeriodTotals = (
  props: z.infer<typeof periodTotalsSchema>
) => (
  <DataCard title={props.label ?? "State"} status={[]} sources={[]}>
    <p className="text-label text-muted-foreground py-6">Not implemented.</p>
  </DataCard>
);
