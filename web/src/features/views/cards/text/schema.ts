import { z } from "zod";
import { plainText } from "../../catalog/primitives";

export const headingSchema = z.strictObject({ text: plainText(120) });
export const noteSchema = z.strictObject({ text: plainText(500) });
