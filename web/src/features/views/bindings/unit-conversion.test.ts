import { describe, expect, it } from "vitest";

import { unitConversion } from "./unit-conversion";

const convert = (from: string, to: string, value: number) => {
  const affine = unitConversion(from, to);
  return affine && affine.scale * value + affine.offset;
};

describe(unitConversion, () => {
  it("is the identity for one unit", () => {
    expect(unitConversion("%", "%")).toStrictEqual({ scale: 1, offset: 0 });
  });

  it.each([
    ["mCel", "Cel", 20_500, 20.5],
    ["mCel", "[degF]", 20_000, 68],
    ["Cel", "[degF]", 22, 71.6],
    ["m/s", "[mi_i]/h", 10, 22.36936292054402],
    ["hPa", "[in_i'Hg]", 33.863788, 1],
    ["mm", "[in_i]", 25.4, 1],
    ["mm/h", "[in_i]/h", 50.8, 2],
    ["10*-4", "1", 3127, 0.3127],
  ])("converts %s to %s", (from, to, value, expected) => {
    expect(convert(from, to, value)).toBeCloseTo(expected, 9);
  });

  it("knows no conversion between unrelated units", () => {
    expect(unitConversion("mCel", "%")).toBeUndefined();
  });
});
