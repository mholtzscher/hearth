import { expect, it } from "vitest";
import { beaufort, beaufortNames, compassPoint, pressureTendency, rainWord, windWord } from "./formulas";

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
