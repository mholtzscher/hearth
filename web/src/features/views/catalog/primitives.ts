import { z } from "zod";

// Regex quantifiers count Unicode code points in both Zod and JSON Schema.
export const plainText = (max: number) => z.string().regex(new RegExp(`^[\\s\\S]{1,${max}}$`, "u"));
export const elementKey = z.string().regex(/^[A-Za-z0-9_-]{1,64}$/).meta({ id: "ElementKey" });
export const entityID = z.string().regex(/^ent_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/).meta({ id: "EntityID" });
export const childrenSchema = z.array(elementKey).min(1).max(16);
export const deviceID = z.string().regex(/^dev_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
