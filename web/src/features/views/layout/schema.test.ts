import { describe, expect, it } from "vitest";

import { gridSchema } from "./schema";

describe("Grid columns", () => {
  it.each([1, 2, 3, 4])("accepts %i columns", (columns) => {
    expect(gridSchema.parse({ columns })).toStrictEqual({ columns });
  });

  it.each([0, 5, 1.5, "2", null])("rejects %j columns", (columns) => {
    expect(gridSchema.safeParse({ columns }).success).toBeFalsy();
  });
});
