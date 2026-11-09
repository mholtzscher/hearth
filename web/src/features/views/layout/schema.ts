import { z } from "zod";

export const stackSchema = z.strictObject({ gap: z.enum(["small", "medium", "large"]) });
export const gridSchema = z.strictObject({ columns: z.union([z.literal(1), z.literal(2), z.literal(3), z.literal(4)]) });
