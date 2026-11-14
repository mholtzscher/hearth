import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundToggleList } from "./ToggleList";

const meta = {
  title: "features/views/cards/ToggleList",
  component: BoundToggleList,
  tags: ["ai-generated"],
  args: {
    title: "Office", all_off: true,
    rows: [
      { label: "Fan", source: { kind: "entity-state", entity_id: entityId(3), entity_type: "hearth.power/v1" } },
      { label: "Plug", source: { kind: "entity-state", entity_id: entityId(4), entity_type: "hearth.power/v1" } },
    ],
  },
} satisfies Meta<typeof BoundToggleList>;
export default meta;
type Story = StoryObj<typeof meta>;

export const AllOff: Story = {
  play: async ({ canvas, userEvent }) => {
    const fan = await canvas.findByRole("switch", { name: /Fan/ });
    await expect(fan).toHaveAttribute("aria-checked", "true");
    await userEvent.click(canvas.getByRole("button", { name: /All off/i }));
    await expect(await canvas.findByText("1 of 1 Off")).toHaveAttribute("role", "status");
    await expect(fan).toHaveAttribute("aria-checked", "false");
    await expect(canvas.getByRole("switch", { name: /Plug/ })).toHaveAttribute("aria-checked", "false");
  },
};
export const NoGroupAction: Story = {
  args: { all_off: undefined },
  play: async ({ canvas }) => {
    await canvas.findByRole("switch", { name: /Fan/ });
    await expect(canvas.queryByRole("button", { name: /All off/i })).not.toBeInTheDocument();
  },
};
export const OneTargetUnavailable: Story = {
  beforeEach: ({ msw }) => { msw.use(entityScenario(3, "unavailable")); },
  play: async ({ canvas }) => { await expect(await canvas.findByText("Unavailable")).toBeVisible(); },
};
