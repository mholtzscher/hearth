import { z } from "zod";

export const stackSchema = z.strictObject({ gap: z.enum(["small", "medium", "large"]) });
export const gridSchema = z.strictObject({ columns: z.literal([1, 2, 3, 4]) });
