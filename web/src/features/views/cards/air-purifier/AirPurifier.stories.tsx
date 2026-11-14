import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { BoundAirPurifier } from "./AirPurifier";

const meta = {
  component: BoundAirPurifier,
  tags: ["ai-generated"],
  args: {
    layout: "card", label: "Living room purifier",
    power: { kind: "entity-state", entity_id: entityId(301), entity_type: "hearth.power/v1" },
    pm25: { kind: "entity-state", entity_id: entityId(302), entity_type: "hearth.numericsensor/v1" },
    filter_life: { kind: "entity-state", entity_id: entityId(303), entity_type: "hearth.numericsensor/v1" },
    mode: { kind: "entity-state", entity_id: entityId(304), entity_type: "hearth.enumsetting/v1" },
    fan_level: { kind: "entity-state", entity_id: entityId(305), entity_type: "hearth.enumsetting/v1" },
    display: { kind: "entity-state", entity_id: entityId(306), entity_type: "hearth.power/v1" },
    child_lock: { kind: "entity-state", entity_id: entityId(307), entity_type: "hearth.power/v1" },
  },
} satisfies Meta<typeof BoundAirPurifier>;
export default meta;
type Story = StoryObj<typeof meta>;

export const FullCard: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByRole("switch", { name: /power/i })).toHaveAttribute("aria-checked", "true"); },
};
export const Tile: Story = { args: { layout: "tile", mode: undefined, fan_level: undefined, display: undefined, child_lock: undefined } };
export const Disabled: Story = { beforeEach: ({ msw }) => { msw.use(entityScenario(301, "disabled")); } };
