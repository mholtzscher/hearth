import type { z } from "zod";

import type { weekGridSchema } from "./schema";

/** Placeholder until the editor is implemented. */
export const WeekGridEditor = (_props: {
  value: z.infer<typeof weekGridSchema>;
  onChange: (value: z.infer<typeof weekGridSchema>) => void;
}) => null;
