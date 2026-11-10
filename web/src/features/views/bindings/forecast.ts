import { z } from "zod";
import { ApiError } from "../../../api/client";
import { useRead } from "../data/provider";
import { entityBody, entityMetadata, entityStateRequest } from "./entity";
import { forecastTypes } from "../cards/forecast/schema";
import type { Quantity } from "./quantity";
import type { EntitySource, Resolved } from "./types";

const condition = z.enum(["clear-day", "clear-night", "partly-cloudy-day", "partly-cloudy-night", "cloudy", "rain", "snow", "sleet", "thunderstorm", "wind", "fog"]);
const measurements = {
  precipitation_probability: z.number().min(0).max(100).optional(), precipitation_rate: z.number().optional(),
  humidity: z.number().optional(), pressure: z.number().optional(), wind_speed: z.number().optional(),
  wind_gust: z.number().optional(), wind_direction: z.number().optional(), cloud_cover: z.number().optional(), uv_index: z.number().optional(),
};
const common = { starts_at: z.string().datetime(), condition: condition.optional(), summary: z.string().optional(), ...measurements };
const hourly = z.object({ periods: z.array(z.object({ ...common, temperature: z.number().optional(), apparent_temperature: z.number().optional() }).strict()).min(1).max(48) });
// Core's schema checks only the date's shape, so reject impossible days such as 2026-02-30 here.
const calendarDate = z.string().regex(/^\d{4}-\d{2}-\d{2}$/).refine(date => {
  const parsed = new Date(`${date}T00:00:00Z`);
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().startsWith(date);
});
const daily = z.object({ periods: z.array(z.object({ ...common, date: calendarDate, temperature_min: z.number().optional(), temperature_max: z.number().optional() }).strict()).min(1).max(8) });
export type ForecastCondition = z.infer<typeof condition>;
type PeriodQuantities = { quantities: Record<string, Quantity> };
export type HourlyPeriod = z.infer<typeof hourly>["periods"][number] & PeriodQuantities;
export type DailyPeriod = z.infer<typeof daily>["periods"][number] & PeriodQuantities;
export type ForecastData = { kind: "hourly"; periods: HourlyPeriod[]; observed_at: string } | { kind: "daily"; periods: DailyPeriod[]; observed_at: string };

/** Keep metadata attached to its original index when expired periods are filtered out. */
function attachQuantities<T extends object>(periods: T[], quantities: Record<string, Quantity> | undefined): (T & PeriodQuantities)[] {
  let temperatureUnit: Quantity | undefined;
  return periods.map((period, index) => {
    const selected: Record<string, Quantity> = {};
    for (const [field, value] of Object.entries(period)) {
      if (typeof value !== "number") continue;
      const quantity = quantities?.[`/periods/${index}/${field}`];
      if (!quantity) throw new Error("Missing forecast quantity metadata");
      if (field === "precipitation_probability" && quantity.unit !== "%") throw new Error("Incompatible precipitation unit");
      if (field === "temperature" || field === "temperature_min" || field === "temperature_max") {
        if (temperatureUnit && (quantity.unit !== temperatureUnit.unit || quantity.label !== temperatureUnit.label)) throw new Error("Incompatible forecast temperature units");
        temperatureUnit = quantity;
      }
      selected[field] = quantity;
    }
    return { ...period, quantities: selected };
  });
}

/** Read the whole forecast State from the shared Entity cache. */
export function useForecast(source: EntitySource): Resolved<ForecastData> {
  const snapshot = useRead(entityStateRequest(source));
  const { error } = snapshot;
  const missing = error instanceof ApiError && error.status === 404;
  if (snapshot.data === undefined) return { status: missing ? "missing" : error ? "error" : "loading", error: error instanceof z.ZodError ? "The resource returned incompatible data." : error?.message };
  try {
    const entity = entityBody.parse(snapshot.data);
    if (entity.id !== source.entity_id || entity.type !== source.entity_type) throw new Error("Entity identity/type mismatch");
    const metadata = entityMetadata(entity, source);
    if (!entity.state) return { status: missing ? "missing" : "unobserved", metadata, refreshFailed: !!error, error: error?.message };
    const observed_at = entity.state.observed_at;
    const data: ForecastData = source.entity_type === forecastTypes.hourly
      ? { kind: "hourly", periods: attachQuantities(hourly.parse(entity.state.value).periods, entity.quantities), observed_at }
      : { kind: "daily", periods: attachQuantities(daily.parse(entity.state.value).periods, entity.quantities), observed_at };
    return { status: missing ? "missing" : "ready", metadata, data, refreshFailed: !!error, error: error?.message };
  } catch {
    return { status: "error", error: "The resource returned incompatible data." };
  }
}
