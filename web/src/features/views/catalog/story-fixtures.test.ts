import { expect, it } from "vitest";
import { catalogExamples, dashboardDefinition, definitionFor } from "../../../../.storybook/fixtures";
import { elementSchemas } from "./index";
import { parseDefinition } from "./compatibility";

it("keeps an accepted Storybook example for every production catalog element", () => {
  expect(Object.keys(catalogExamples).sort()).toEqual(Object.keys(elementSchemas).sort());
  for (const element of Object.values(catalogExamples)) {
    const definition = definitionFor(element);
    expect(parseDefinition(definition), element.type).toEqual(definition);
  }
  expect(parseDefinition(dashboardDefinition)).toEqual(dashboardDefinition);
});
