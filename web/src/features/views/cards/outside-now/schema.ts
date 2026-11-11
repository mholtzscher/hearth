import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { sensorReading, sensorUnits, typedReading } from "../weather/schema";

export const outsideNowSchema = z.strictObject({
  label: plainText(120).optional(),
  temperature: typedReading("hearth.temperature/v1").describe("Outdoor temperature. The card also reads its last 24 hours for the high and low."),
  humidity: typedReading("hearth.relativehumidity/v1").describe("Outdoor humidity.").optional(),
  wind: typedReading("hearth.speed/v1").describe("Sustained wind speed, for a row and wind words.").optional(),
  wind_direction: sensorReading(sensorUnits.direction).describe("Wind direction in deg, named on the wind row.").optional(),
  rain_rate: sensorReading(sensorUnits.rainRate).describe("Rain rate in mm/h, for rain words such as Light rain.").optional(),
  rain_today: sensorReading(sensorUnits.rainDepth).describe("The station's daily rain total in mm.").optional(),
});
