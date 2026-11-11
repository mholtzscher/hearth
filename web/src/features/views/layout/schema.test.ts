import { describe, expect, it } from "vitest";
import { gridSchema } from "./schema";

describe("Grid columns", () => {
  for (const columns of [1, 2, 3, 4]) {
    it(`accepts ${columns} columns`, () => {
      expect(gridSchema.parse({ columns })).toEqual({ columns });
    });
  }

  for (const columns of [0, 5, 1.5, "2", null]) {
    it(`rejects ${JSON.stringify(columns)} columns`, () => {
      expect(gridSchema.safeParse({ columns }).success).toBe(false);
    });
  }
});
