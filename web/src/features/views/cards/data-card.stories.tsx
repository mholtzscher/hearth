import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";

import { entityId, observedAt } from "../../../../.storybook/fixtures";
import { DataCard } from "./data-card";
import { statusEntry } from "./status";

const meta = {
  title: "Components/Cards/DataCard",
  component: DataCard,
  tags: ["ai-generated"],
  args: {
    title: "Living room",
    status: [],
    observedAt,
    children: "21.5 °C",
    sources: [
      {
        entityId: entityId(15),
        label: "Living room",
        availability: "available",
        enabled: true,
      },
    ],
  },
  parameters: {
    docs: {
      description: {
        component:
          "The one frame every data card uses: a header with the title and status, the body, and a footer with the observation age and Source details.",
      },
    },
  },
} satisfies Meta<typeof DataCard>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Ready: Story = {
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("article", { name: "Living room" })
    ).toBeVisible();
    await expect(canvas.getByText("Observed just now")).toBeVisible();
  },
};
/** Every status that applies is listed, in precedence order. */
export const WithStatus: Story = {
  args: { status: [statusEntry("unavailable"), statusEntry("refresh-failed")] },
  play: async ({ canvas }) => {
    const entries = canvas.getAllByRole("listitem");
    await expect(entries[0]).toHaveTextContent("Unavailable");
    await expect(entries[1]).toHaveTextContent("Couldn't refresh");
  },
};
export const ChartUnit: Story = {
  args: { title: "Temperatures", unit: "°C" },
  play: async ({ canvas }) => {
    await expect(canvas.getByText("°C")).toBeVisible();
  },
};
export const OldestReading: Story = {
  args: { observedLabel: "Oldest reading" },
  play: async ({ canvas }) => {
    await expect(canvas.getByText("Oldest just now")).toBeVisible();
  },
};
export const NoFooter: Story = {
  args: { observedAt: undefined, sources: [] },
  play: async ({ canvas }) => {
    await expect(
      canvas.queryByRole("button", { name: "Source" })
    ).not.toBeInTheDocument();
  },
};
