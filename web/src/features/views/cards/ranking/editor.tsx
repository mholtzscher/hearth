import type { z } from "zod";

import type { SensorChoice } from "../time-series/editor";
import type { rankingSchema } from "./schema";

/** Placeholder until the editor is implemented. */
export const RankingEditor = (_props: {
  value: z.infer<typeof rankingSchema>;
  choices: SensorChoice[];
  onChange: (value: z.infer<typeof rankingSchema>) => void;
}) => null;
