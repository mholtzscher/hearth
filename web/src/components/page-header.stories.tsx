import type { Meta, StoryObj } from "@storybook/react-vite";

import { expectNoHorizontalOverflow } from "../../.storybook/play";
import { Button } from "./button";
import { PageHeader } from "./page-header";

const meta = {
  title: "Components/PageHeader",
  component: PageHeader,
  args: {
    title: "Devices",
    description: "Inspect registered Devices and their Entities.",
    children: <Button variant="outline">Refresh</Button>,
  },
} satisfies Meta<typeof PageHeader>;
export default meta;
type Story = StoryObj<typeof meta>;

export const WithActions: Story = {};
export const TitleOnly: Story = {
  args: { description: undefined, children: undefined },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  args: {
    title: "Weather station in the back garden",
    description: (
      <span className="text-label font-mono break-all">
        dev_019a3e6a-4111-7000-8000-000000000003
      </span>
    ),
  },
  play: async ({ canvasElement }) => {
    await expectNoHorizontalOverflow(canvasElement);
  },
};
