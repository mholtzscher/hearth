import { describe, expect, it } from "vitest";
import { afterglow, observationAge } from "./afterglow";

const observed = "2026-11-05T22:00:00Z";
const at = (minutes: number) => Date.parse(observed) + minutes * 60_000;

describe("afterglow", () => {
  it("stays fully orange for the first five minutes", () => {
    expect(afterglow(observed, at(0))).toBe(1);
    expect(afterglow(observed, at(5))).toBe(1);
  });

  it("cools linearly toward violet until one hour", () => {
    expect(afterglow(observed, at(32.5))).toBeCloseTo(0.5);
    expect(afterglow(observed, at(60))).toBe(0);
    expect(afterglow(observed, at(600))).toBe(0);
  });

  it("treats an observation timestamped slightly ahead of the clock as fresh", () => {
    expect(afterglow(observed, at(-1))).toBe(1);
  });

  it("shows no orange for an unparseable timestamp", () => {
    expect(afterglow("not a time", at(0))).toBe(0);
  });
});

describe("observationAge", () => {
  it("names recent ages in words", () => {
    expect(observationAge(observed, at(0.5))).toBe("just now");
    expect(observationAge(observed, at(3))).toBe("3 min ago");
    expect(observationAge(observed, at(150))).toBe("2 h ago");
  });

  it("leaves day-old observations to the absolute time", () => {
    expect(observationAge(observed, at(24 * 60))).toBeUndefined();
  });
});
