import { describe, expect, it } from "vitest";
import { decodeEntityDetail } from "./entity-detail";

const evidence = { observed_at: "2026-11-09T10:00:00Z", observation_id: "obs_one", adapter_received_at: "2026-11-09T10:00:01Z" };
const body = (canonical: unknown, display: unknown) => ({
  id: "ent_test", type: "hearth.temperature/v1", name: "Reading", enabled: true,
  device_id: "dev_test", adapter_id: "test", adapter_name: "Reading", name_override: null, support: {},
  availability: { status: "available", source: "entity_report", since: evidence.observed_at, evidence_at: evidence.observed_at },
  state: { ...evidence, canonical, display },
});
const shown = (value: number, unit: string, label: string) => ({ value, unit, label, fraction_digits: 1 });

describe("paired Entity detail decoder", () => {
  it("retains shared observation evidence and distinct scalar values and units", () => {
    const entity = decodeEntityDetail(body({ value: 21500, unit: "mCel" }, shown(70.7, "[degF]", "°F")));
    expect(entity.state).toEqual({ ...evidence, value: 70.7 });
    expect(entity.canonical.state).toEqual({ ...evidence, value: 21500 });
    expect(entity.quantities?.[""]).toEqual({ unit: "[degF]", label: "°F", fraction_digits: 1 });
    expect(entity.canonical.quantities?.[""].unit).toBe("mCel");
  });

  it("unpacks composite quantities alongside identical plain boolean and string leaves", () => {
    const entity = decodeEntityDetail(body(
      { active: true, mode: "heat", temperature: { value: 21000, unit: "mCel" } },
      { active: true, mode: "heat", temperature: shown(69.8, "[degF]", "°F") },
    ));
    expect(entity.state?.value).toEqual({ active: true, mode: "heat", temperature: 69.8 });
    expect(entity.canonical.state?.value).toEqual({ active: true, mode: "heat", temperature: 21000 });
    expect(entity.quantities?.["/temperature"].unit).toBe("[degF]");
  });

  it("preserves forecast arrays and absent optional leaves", () => {
    const entity = decodeEntityDetail(body(
      { periods: [{ temperature: { value: 16, unit: "Cel" }, condition: "rain" }, { condition: "clear" }] },
      { periods: [{ temperature: shown(60.8, "[degF]", "°F"), condition: "rain" }, { condition: "clear" }] },
    ));
    expect(entity.state?.value).toEqual({ periods: [{ temperature: 60.8, condition: "rain" }, { condition: "clear" }] });
    expect(entity.canonical.state?.value).toEqual({ periods: [{ temperature: 16, condition: "rain" }, { condition: "clear" }] });
    expect(entity.canonical.quantities?.["/periods/0/temperature"].unit).toBe("Cel");
    expect(entity.quantities?.["/periods/1/temperature"]).toBeUndefined();
  });

  it("keeps never-observed State null in both projections", () => {
    const entity = decodeEntityDetail({ ...body(false, false), state: null });
    expect(entity.state).toBeNull();
    expect(entity.canonical.state).toBeNull();
  });

  it.each([
    ["raw numeric leaves", body(21, 21)],
    ["legacy value", { ...body(false, false), state: { ...evidence, value: 21 } }],
    ["different composite fields", body({ active: true }, { active: true, extra: false })],
    ["different arrays", body({ periods: [true] }, { periods: [] })],
    ["different string leaves", body("open", "closed")],
    ["malformed quantity", body({ value: 21, unit: "Cel" }, { value: 21, unit: "Cel" })],
  ])("rejects %s", (_name, raw) => {
    expect(() => decodeEntityDetail(raw)).toThrow();
  });
});
