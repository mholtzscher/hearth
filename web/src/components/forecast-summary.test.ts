import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { forecastSummary, stateCellText } from "./forecast-summary.ts";

const hourly = "hearth.hourlyforecast/v1";
const daily = "hearth.dailyforecast/v1";

describe("forecastSummary", () => {
  let previous: string | undefined;
  beforeEach(() => { previous = process.env.TZ; process.env.TZ = "America/Chicago"; });
  afterEach(() => { if (previous === undefined) delete process.env.TZ; else process.env.TZ = previous; });

  it("names the hourly period count and the first local hour", () => {
    const periods = Array.from({ length: 48 }, (_, i) => ({ starts_at: new Date(Date.UTC(2026, 10, 10, 1 + i)).toISOString().replace(".000", "") }));
    expect(forecastSummary(hourly, { periods })).toBe("48 hourly periods from Mon 7 PM");
  });

  it("names the daily count and the first calendar day without a timezone shift", () => {
    expect(forecastSummary(daily, { periods: [{ starts_at: "2026-11-09T06:00:00Z", date: "2026-11-09" }] }))
      .toBe("1 daily period from Mon, Nov 9");
  });

  it("leaves other types and malformed forecasts to the caller", () => {
    expect(forecastSummary("hearth.temperature/v1", 21500)).toBeNull();
    expect(forecastSummary(hourly, { periods: [] })).toBeNull();
    expect(forecastSummary(hourly, { periods: [{ starts_at: "not a time" }] })).toBeNull();
    expect(forecastSummary(daily, { periods: [{ starts_at: "2026-11-09T06:00:00Z", date: "Nov 9" }] })).toBeNull();
    expect(forecastSummary(daily, "garbage")).toBeNull();
  });

  it("falls back to raw JSON in table cells", () => {
    expect(stateCellText("hearth.temperature/v1", 21500)).toBe("21500");
    expect(stateCellText(hourly, { periods: [] })).toBe('{"periods":[]}');
    expect(stateCellText(daily, { periods: [{ starts_at: "2026-11-09T06:00:00Z", date: "2026-11-09" }] }))
      .toBe("1 daily period from Mon, Nov 9");
  });
});
