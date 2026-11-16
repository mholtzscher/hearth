import type { Meta, StoryObj } from "@storybook/react-vite";

import { expectNoHorizontalOverflow } from "../../.storybook/play";
import { Facts } from "./facts";

const meta = {
  title: "Components/Facts",
  component: Facts,
  args: {
    rows: [
      ["Type", "hearth.power/v1"],
      ["Device", "dev_019a3e6a-4111-7000-8000-000000000004"],
      ["State", "false obs=obs_story_4 at 2026-11-09T10:00:00Z"],
    ],
  },
} satisfies Meta<typeof Facts>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Metadata: Story = {};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvasElement }) => {
    await expectNoHorizontalOverflow(canvasElement);
  },
};
