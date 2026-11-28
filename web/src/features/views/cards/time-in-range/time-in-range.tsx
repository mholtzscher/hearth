import type { z } from "zod";

import { DataCard } from "../data-card";
import type { timeInRangeSchema } from "./schema";

/** Placeholder until the card is implemented. */
export const BoundTimeInRange = (props: z.infer<typeof timeInRangeSchema>) => (
  <DataCard title={props.label ?? "Reading"} status={[]} sources={[]}>
    <p className="text-label text-muted-foreground py-6">Not implemented.</p>
  </DataCard>
);
