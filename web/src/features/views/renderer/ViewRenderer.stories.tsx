import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { catalogExamples, dashboardDefinition, definitionFor } from "../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../.storybook/play";
import { ViewRenderer } from "./ViewRenderer";

// Controls choose a valid example instead of editing raw definition JSON.
const examples = Object.fromEntries([
  ["Morning dashboard", dashboardDefinition],
  ...Object.values(catalogExamples).map(element => [element.type, definitionFor(element)]),
]);

const meta = {
  component: ViewRenderer,
  tags: ["ai-generated"],
  args: { definition: dashboardDefinition, headingLevel: 3 },
  argTypes: { definition: { options: Object.keys(examples), mapping: examples, control: { type: "select" } } },
  parameters: { docs: { description: { component: "Render a validated hearth.dashboard/v1 graph. Layout and text nodes are catalog elements, not independent React components. Unsupported content remains recoverable." } } },
} satisfies Meta<typeof ViewRenderer>;
export default meta;
type Story = StoryObj<typeof meta>;

export const MorningDashboard: Story = {
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("9.4")).toBeVisible();
    await expect(await canvas.findByRole("switch")).toHaveAttribute("aria-checked", "true");
  },
};
export const PhoneDashboard: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await canvas.findByText("9.4");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
export const Stack: Story = { args: { definition: definitionFor(catalogExamples.Stack) } };
export const Grid: Story = { args: { definition: definitionFor(catalogExamples.Grid) } };
export const Heading: Story = { args: { definition: definitionFor(catalogExamples.Heading) } };
export const Note: Story = { args: { definition: definitionFor(catalogExamples.Note) } };
export const Inactive: Story = {
  args: { active: false },
  play: async ({ canvas }) => { await expect(canvas.getByText("Open view to load live readings")).toBeVisible(); },
};
export const UnsupportedElement: Story = {
  args: { definition: {
    schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Future dashboard",
    spec: { root: "root", elements: { root: { type: "FutureCard", props: {} } } },
  } },
  play: async ({ canvas }) => { await expect(canvas.getByText(/original content preserved/)).toBeVisible(); },
};
export const InvalidDefinition: Story = {
  args: { definition: { schema_version: 999 } },
  play: async ({ canvas }) => { await expect(canvas.getByText("Invalid view definition.")).toBeVisible(); },
};
