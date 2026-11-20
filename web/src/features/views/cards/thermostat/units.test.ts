import { describe, expect, it } from "vitest";

import {
  formatTarget,
  fromMilliCelsius,
  stepFrom,
  temperatureUnit,
  toMilliCelsius,
} from "./units";

describe(toMilliCelsius, () => {
  it("converts household values with the documented formula", () => {
    expect(toMilliCelsius(70, "[degF]")).toBe(21_111);
    expect(toMilliCelsius(68, "[degF]")).toBe(20_000);
    expect(toMilliCelsius(21.5, "Cel")).toBe(21_500);
    expect(toMilliCelsius(-0.5, "Cel")).toBe(-500);
  });

  it("returns every whole °F from 45 to 95 unchanged after a round trip", () => {
    for (let fahrenheit = 45; fahrenheit <= 95; fahrenheit += 1) {
      const mCel = toMilliCelsius(fahrenheit, "[degF]");
      expect(fromMilliCelsius(mCel, "[degF]", 1)).toBe(fahrenheit);
    }
  });
});

describe(fromMilliCelsius, () => {
  it("rounds to the quantity's fraction digits", () => {
    expect(fromMilliCelsius(21_000, "[degF]", 1)).toBe(69.8);
    expect(fromMilliCelsius(21_000, "[degF]", 0)).toBe(70);
    expect(fromMilliCelsius(21_500, "Cel", 1)).toBe(21.5);
  });
});

describe(stepFrom, () => {
  it.each([
    [69.8, "[degF]", 1, 70],
    [69.8, "[degF]", -1, 69],
    [70, "[degF]", 1, 71],
    [70, "[degF]", -1, 69],
    [21, "Cel", 1, 21.5],
    [21.3, "Cel", 1, 21.5],
    [21.3, "Cel", -1, 21],
    [21.5, "Cel", -1, 21],
  ] as const)("steps %s %s by %s to %s", (value, unit, direction, want) => {
    expect(stepFrom(value, unit, direction)).toBe(want);
  });
});

const quantity = (unit: string) => ({ unit, label: unit, fraction_digits: 1 });

describe(temperatureUnit, () => {
  it("accepts only units the card can convert back", () => {
    expect(temperatureUnit(quantity("[degF]"))).toBe("[degF]");
    expect(temperatureUnit(quantity("Cel"))).toBe("Cel");
    expect(temperatureUnit(quantity("K"))).toBeUndefined();
    expect(temperatureUnit(undefined)).toBeUndefined();
  });
});

describe(formatTarget, () => {
  it("drops trailing zeros but keeps real fractions", () => {
    expect(formatTarget(70, 1)).toBe("70");
    expect(formatTarget(69.8, 1)).toBe("69.8");
    expect(formatTarget(21.5, 1)).toBe("21.5");
    expect(formatTarget(70, 0)).toBe("70");
  });
});
