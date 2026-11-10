import { z } from "zod";
import { ApiError } from "../../../api/client";
import { useRead } from "../data/provider";
import { entityBody, entityMetadata, entityStateRequest } from "./entity";
import { forecastTypes } from "../cards/forecast/schema";
import type { EntitySource, Resolved } from "./types";

const condition = z.enum(["clear-day", "clear-night", "partly-cloudy-day", "partly-cloudy-night", "cloudy", "rain", "snow", "sleet", "thunderstorm", "wind", "fog"]);
const common = { starts_at: z.string().datetime(), condition: condition.optional(), precipitation_probability_percent: z.number().min(0).max(100).optional() };
const hourly = z.object({ periods: z.array(z.object({ ...common, temperature_c: z.number().optional() })).min(1).max(48) });
// Core's schema checks only the date's shape, so reject impossible days such as 2026-02-30 here.
const calendarDate = z.string().regex(/^\d{4}-\d{2}-\d{2}$/).refine(date => {
  const parsed = new Date(`${date}T00:00:00Z`);
  return !Number.isNaN(parsed.getTime()) && parsed.toISOString().startsWith(date);
});
const daily = z.object({ periods: z.array(z.object({ ...common, date: calendarDate, temperature_min_c: z.number().optional(), temperature_max_c: z.number().optional() })).min(1).max(8) });
export type ForecastCondition = z.infer<typeof condition>;
export type HourlyPeriod = z.infer<typeof hourly>["periods"][number];
export type DailyPeriod = z.infer<typeof daily>["periods"][number];
export type ForecastData = { kind: "hourly"; periods: HourlyPeriod[]; observed_at: string } | { kind: "daily"; periods: DailyPeriod[]; observed_at: string };

/** Read the whole forecast State from the shared Entity cache. */
export function useForecast(source: EntitySource): Resolved<ForecastData> {
  const snapshot = useRead(entityStateRequest(source));
  const { error } = snapshot;
  const missing = error instanceof ApiError && error.status === 404;
  if (snapshot.data === undefined) return { status: missing ? "missing" : error ? "error" : "loading", error: error?.message };
  try {
    const entity = entityBody.parse(snapshot.data);
    if (entity.id !== source.entity_id || entity.type !== source.entity_type) throw new Error("Entity identity/type mismatch");
    const metadata = entityMetadata(entity, source);
    if (!entity.state) return { status: missing ? "missing" : "unobserved", metadata, refreshFailed: !!error, error: error?.message };
    const observed_at = entity.state.observed_at;
    const data: ForecastData = source.entity_type === forecastTypes.hourly
      ? { kind: "hourly", ...hourly.parse(entity.state.value), observed_at }
      : { kind: "daily", ...daily.parse(entity.state.value), observed_at };
    return { status: missing ? "missing" : "ready", metadata, data, refreshFailed: !!error, error: error?.message };
  } catch {
    return { status: "error", error: "The resource returned incompatible data." };
  }
}
