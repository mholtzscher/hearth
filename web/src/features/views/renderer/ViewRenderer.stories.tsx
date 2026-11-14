import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { catalogExamples, dashboardDefinition, definitionFor } from "../../../../.storybook/fixtures";
import { ViewRenderer } from "./ViewRenderer";

const meta = {
  component: ViewRenderer,
  tags: ["ai-generated"],
  args: { definition: dashboardDefinition, headingLevel: 3 },
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
};
export const Stack: Story = { args: { definition: definitionFor(catalogExamples.Stack) } };
export const Grid: Story = { args: { definition: definitionFor(catalogExamples.Grid) } };
export const Heading: Story = { args: { definition: definitionFor(catalogExamples.Heading) } };
export const Note: Story = { args: { definition: definitionFor(catalogExamples.Note) } };
export const Inactive: Story = { args: { active: false } };
export const UnsupportedElement: Story = {
  args: { definition: {
    schema_version: 1, catalog_version: "hearth.dashboard/v1", title: "Future dashboard",
    spec: { root: "root", elements: { root: { type: "FutureCard", props: {} } } },
  } },
};
export const InvalidDefinition: Story = { args: { definition: { schema_version: 999 } } };
