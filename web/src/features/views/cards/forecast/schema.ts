import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { typedReading } from "../weather/schema";

export const forecastTypes = { hourly: "hearth.hourlyforecast/v1", daily: "hearth.dailyforecast/v1" } as const;

export const forecastSchema = z.union([
  z.strictObject({
    label: plainText(120).optional(),
    source: typedReading(forecastTypes.hourly).describe("An hourly forecast Entity. The card charts its upcoming hours."),
    periods: z.number().int().min(6).max(48).describe("Hours to chart. Defaults to 24.").optional(),
  }),
  z.strictObject({
    label: plainText(120).optional(),
    source: typedReading(forecastTypes.daily).describe("A daily forecast Entity. The card lists its upcoming days."),
    periods: z.number().int().min(3).max(8).describe("Days to list. Defaults to 8.").optional(),
  }),
]);
