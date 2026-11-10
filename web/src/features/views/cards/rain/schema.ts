import { z } from "zod";
import { plainText } from "../../catalog/primitives";
import { seriesWindows } from "../../bindings/schema";
import { sensorReading, sensorUnits } from "../weather/schema";

const total = sensorReading(sensorUnits.rainDepth);

export const rainPeriods = ["day", "week", "month", "year"] as const;
export type RainPeriod = typeof rainPeriods[number];

export const rainSchema = z.strictObject({
  label: plainText(120).optional(),
  rate: sensorReading(sensorUnits.rainRate).describe("Rain rate, also the trend's source."),
  storm: total.describe("The station's storm (event) rain total.").optional(),
  totals: z.strictObject({ day: total.optional(), week: total.optional(), month: total.optional(), year: total.optional() })
    .describe("The station's own rain counters in mm, shown in this order. Never sum the rate.").optional(),
  trend: z.enum(seriesWindows).describe("Draw the rain rate over this window.").optional(),
});
