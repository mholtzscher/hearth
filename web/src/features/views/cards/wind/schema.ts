import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { sensorReading, sensorUnits, typedReading } from "../weather/schema";

const speed = typedReading("hearth.speed/v1");

export const windSchema = z.strictObject({
  label: plainText(120).optional(),
  speed: speed.describe("Sustained wind speed: the reading and its Beaufort words."),
  gust: speed.describe("Current gust speed.").optional(),
  direction: sensorReading(sensorUnits.direction).describe("Wind direction in deg: where the wind comes from, drawn on a compass.").optional(),
  max_gust: speed.describe("The station's maximum gust today.").optional(),
});
