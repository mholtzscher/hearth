import { describe, expect, it } from "vitest";

import {
  beaufort,
  beaufortNames,
  compassPoint,
  pressureTendency,
  rainWord,
  windWord,
} from "./formulas";

describe("Weather words", () => {
  it("names Beaufort forces at their band edges", () => {
    expect(
      [0, 0.49, 0.5, 5.4, 5.5, 32.59, 32.6, 50].map(beaufort)
    ).toStrictEqual([0, 0, 1, 3, 4, 11, 12, 12]);
    expect(beaufortNames[beaufort(6.3)]).toBe("Moderate breeze");
    expect([0.2, 2, 5, 9, 18, 30].map(windWord)).toStrictEqual([
      "Calm",
      "Light wind",
      "Breezy",
      "Windy",
      "Very windy",
      "Storm-force wind",
    ]);
  });

  it("rounds bearings to the nearest of 16 compass points", () => {
    expect(
      [0, 11.24, 11.25, 135, 315, 348.75, 359.9, 720].map(
        (degrees) => compassPoint(degrees).short
      )
    ).toStrictEqual(["N", "N", "NNE", "SE", "NW", "N", "N", "N"]);
    expect(compassPoint(225).words).toBe("southwest");
  });

  it("words rain intensity from the rate", () => {
    expect([0, 0.2, 2.49, 2.5, 7.6, 7.7].map(rainWord)).toStrictEqual([
      undefined,
      "Light rain",
      "Light rain",
      "Rain",
      "Rain",
      "Heavy rain",
    ]);
  });

  it("words the three-hour pressure tendency with Met Office thresholds", () => {
    expect(
      [0, 0.09, -0.1, 1.5, 1.6, -3.5, 3.6, -6, 6.1].map(
        (change) => pressureTendency(change).words
      )
    ).toStrictEqual([
      "Steady",
      "Steady",
      "Falling slowly",
      "Rising slowly",
      "Rising",
      "Falling",
      "Rising quickly",
      "Falling quickly",
      "Rising very rapidly",
    ]);
    expect(pressureTendency(-2).direction).toBe("falling");
  });

  it.each([
    { past: 1003.2, now: 1003.3, words: "slowly" },
    { past: 1022.9, now: 1024.4, words: "slowly" },
    { past: 1020.9, now: 1024.4, words: "" },
    { past: 1018.4, now: 1024.4, words: "quickly" },
  ])(
    "classifies realistic pressure differences at the threshold ($past → $now)",
    ({ past, now, words }) => {
      expect(pressureTendency(now - past)).toStrictEqual({
        direction: "rising",
        words: `Rising${words ? ` ${words}` : ""}`,
      });
      expect(pressureTendency(past - now)).toStrictEqual({
        direction: "falling",
        words: `Falling${words ? ` ${words}` : ""}`,
      });
    }
  );

  it("preserves pressure changes just below and above each threshold without rounding to display precision", () => {
    expect(
      [
        0.099999, 0.100001, 1.499999, 1.500001, 3.499999, 3.500001, 5.999999,
        6.000001,
      ].map((change) => pressureTendency(change).words)
    ).toStrictEqual([
      "Steady",
      "Rising slowly",
      "Rising slowly",
      "Rising",
      "Rising",
      "Rising quickly",
      "Rising quickly",
      "Rising very rapidly",
    ]);
  });
});
