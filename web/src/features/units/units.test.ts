import { describe, expect, it } from "vitest";
import { displayUnit, present } from "./units";

describe("household units", () => {
  // UCUM print symbols use a no-break space where a unit has two words ("in Hg").
  it.each([
    ["mCel", "integer", "metric", 21500, 21.5, "°C", 1],
    ["mCel", "integer", "us", 21500, 70.7, "°F", 1],
    ["m/s", "number", "metric", 3.4, 3.4, "m/s", 1],
    ["m/s", "number", "us", 3.4, 7.605583392984967, "mi/h", 1],
    ["hPa", "number", "metric", 1013.2, 1013.2, "hPa", 1],
    ["hPa", "number", "us", 1013.2, 29.919866023257644, "in\u00a0Hg", 2],
    ["%", "number", "us", 45, 45, "%", 1],
    ["%", "integer", "metric", 80, 80, "%", 0],
    ["lqi", "number", "us", 120, 120, "lqi", 1],
    ["1", "integer", "metric", 40, 40, "", 0],
    ["/MK", "integer", "metric", 370, 370, "mired", 0],
    ["/MK", "integer", "us", 370, 370, "mired", 0],
    ["10*-4", "integer", "metric", 3127, 0.3127, "", 4],
    ["10*-4", "integer", "us", 3127, 0.3127, "", 4],
    ["deg", "integer", "metric", 210, 210, "°", 0],
  ] as const)("shows %s (%s) under %s", (unit, valueType, system, value, shown, label, digits) => {
    const measurement = present(value, unit, valueType, system);
    expect(measurement.value).toBeCloseTo(shown, 6);
    expect(measurement.label).toBe(label);
    expect(measurement.fraction_digits).toBe(digits);
  });
  it("never throws for unknown or malformed units", () => {
    for (const unit of ["", "not a unit", "[unclosed", "lqi/%%"]) {
      expect(() => displayUnit(unit, "number", "us")).not.toThrow();
      expect(present(1.5, unit, "number", "us").value).toBe(1.5);
    }
  });
  it("converts temperature extremes on the affine line", () => {
    expect(present(-40000, "mCel", "integer", "us").value).toBeCloseTo(-40, 6);
    expect(present(100000, "mCel", "integer", "us").value).toBeCloseTo(212, 6);
  });
  it.each([0, 50, 51, 99, 100])("preserves canonical integer percentage %s", value => {
    for (const system of ["metric", "us"] as const) {
      const display = displayUnit("%", "integer", system);
      expect(display.convert(value)).toBe(value);
      expect(display).toMatchObject({ label: "%", fraction_digits: 0 });
    }
  });
});
