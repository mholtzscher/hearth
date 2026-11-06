import { z } from "zod";
import { deviceBindingSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

export const deviceSchema = z.strictObject({ device: deviceBindingSchema, label: plainText(120).optional() });
