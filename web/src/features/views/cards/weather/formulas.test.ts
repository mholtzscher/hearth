import { describe, expect, it } from "vitest";
import { beaufort, beaufortNames, compassPoint, dewPoint, feelsLike, pressureTendency, rainWord, windWord } from "./formulas";

const fahrenheit = (celsius: number) => celsius * 9 / 5 + 32;
const celsius = (f: number) => (f - 32) * 5 / 9;

describe("feels like", () => {
  // NWS heat index table: 90 °F at 70 % reads 106 °F; 100 °F at 40 % reads 109 °F.
  it.each([[90, 70, 106], [100, 40, 109]])("uses the heat index at %i °F and %i %%", (f, rh, expected) => {
    const result = feelsLike(celsius(f), rh)!;
    expect(result.method).toBe("heat index");
    expect(Math.round(fahrenheit(result.celsius))).toBe(expected);
  });

  // NWS wind chill table: 30 °F with 15 mph reads 19 °F; 0 °F with 25 mph reads −24 °F.
  it.each([[30, 15, 19], [0, 25, -24]])("uses wind chill at %i °F and %i mph", (f, mph, expected) => {
    const result = feelsLike(celsius(f), 50, mph * 0.44704)!;
    expect(result.method).toBe("wind chill");
    expect(Math.round(fahrenheit(result.celsius))).toBe(expected);
  });

  it("applies neither formula to mild, dry, or calm air", () => {
    expect(feelsLike(18, 60, 4)).toBeUndefined();
    expect(feelsLike(celsius(90), 30)).toBeUndefined();
    expect(feelsLike(celsius(40), 50, 3 * 0.44704)).toBeUndefined();
    expect(feelsLike(celsius(40))).toBeUndefined();
  });
});

it("computes dew point with the Magnus formula", () => {
  expect(dewPoint(20, 50)).toBeCloseTo(9.3, 1);
  expect(dewPoint(30, 70)).toBeCloseTo(23.9, 1);
  expect(dewPoint(10, 100)).toBeCloseTo(10, 5);
  expect(dewPoint(20, 0)).toBeUndefined();
});

it("names Beaufort forces at their band edges", () => {
  expect([0, 0.49, 0.5, 5.4, 5.5, 32.59, 32.6, 50].map(beaufort)).toEqual([0, 0, 1, 3, 4, 11, 12, 12]);
  expect(beaufortNames[beaufort(6.3)]).toBe("Moderate breeze");
  expect([0.2, 2, 5, 9, 18, 30].map(windWord)).toEqual(["Calm", "Light wind", "Breezy", "Windy", "Very windy", "Storm-force wind"]);
});

it("rounds bearings to the nearest of 16 compass points", () => {
  expect([0, 11.24, 11.25, 135, 315, 348.75, 359.9, 720].map(degrees => compassPoint(degrees).short))
    .toEqual(["N", "N", "NNE", "SE", "NW", "N", "N", "N"]);
  expect(compassPoint(225).words).toBe("southwest");
});

it("words rain intensity from the rate", () => {
  expect([0, 0.2, 2.49, 2.5, 7.6, 7.7].map(rainWord)).toEqual([undefined, "Light rain", "Light rain", "Rain", "Rain", "Heavy rain"]);
});

it("words the three-hour pressure tendency with Met Office thresholds", () => {
  expect([0, 0.09, -0.1, 1.5, 1.6, -3.5, 3.6, -6, 6.1].map(change => pressureTendency(change).words)).toEqual([
    "Steady", "Steady", "Falling slowly", "Rising slowly", "Rising", "Falling", "Rising quickly", "Falling quickly", "Rising very rapidly",
  ]);
  expect(pressureTendency(-2).direction).toBe("falling");
});
