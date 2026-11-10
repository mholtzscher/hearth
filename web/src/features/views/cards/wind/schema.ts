import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { sensorReading, sensorUnits, typedReading } from "../weather/schema";

const speed = typedReading("hearth.speed/v1");

export const windSchema = z.strictObject({
  label: plainText(120).optional(),
  speed: speed.describe("Sustained wind speed."),
  gust: speed.describe("Current gust speed.").optional(),
  direction: sensorReading(sensorUnits.direction).describe("Wind direction in degrees.").optional(),
  max_gust: speed.describe("The station's maximum gust today.").optional(),
});
