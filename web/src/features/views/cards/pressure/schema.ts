import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { seriesWindows } from "../../bindings/schema";
import { typedReading } from "../weather/schema";

export const pressureSchema = z.strictObject({
  label: plainText(120).optional(),
  source: typedReading("hearth.pressure/v1").describe("Barometric pressure."),
  trend: z.enum(seriesWindows).describe("Draw a history line under the reading over this window.").optional(),
});
