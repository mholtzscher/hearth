import { describe, expect, it } from "vitest";

import {
  catalogExamples,
  dashboardDefinition,
  definitionFor,
} from "../../../../.storybook/fixtures";
import { parseDefinition } from "./compatibility";
import { elementSchemas } from "./index";

describe("Storybook catalog fixtures", () => {
  it("keeps an accepted Storybook example for every production catalog element", () => {
    expect(Object.keys(catalogExamples).toSorted()).toStrictEqual(
      Object.keys(elementSchemas).toSorted()
    );
    for (const element of Object.values(catalogExamples)) {
      const definition = definitionFor(element);
      expect(parseDefinition(definition)).toStrictEqual(definition);
    }
    expect(parseDefinition(dashboardDefinition)).toStrictEqual(
      dashboardDefinition
    );
  });
});
