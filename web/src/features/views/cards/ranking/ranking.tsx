import type { z } from "zod";

import { DataCard } from "../data-card";
import type { rankingSchema } from "./schema";

/** Placeholder until the card is implemented. */
export const BoundRanking = (props: z.infer<typeof rankingSchema>) => (
  <DataCard title={props.title} status={[]} sources={[]}>
    <p className="text-label text-muted-foreground py-6">Not implemented.</p>
  </DataCard>
);
