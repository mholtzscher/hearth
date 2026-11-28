import type { z } from "zod";

import { DataCard } from "../data-card";
import type { weekGridSchema } from "./schema";

/** Placeholder until the card is implemented. */
export const BoundWeekGrid = (props: z.infer<typeof weekGridSchema>) => (
  <DataCard title={props.label ?? "Reading"} status={[]} sources={[]}>
    <p className="text-label text-muted-foreground py-6">Not implemented.</p>
  </DataCard>
);
