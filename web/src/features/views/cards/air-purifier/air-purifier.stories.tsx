import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { watchHeight } from "../../../../../.storybook/play";
import { BoundAirPurifier } from "./air-purifier";

const meta = {
  title: "Components/Cards/AirPurifier",
  component: BoundAirPurifier,
  tags: ["ai-generated"],
  args: {
    layout: "card",
    label: "Living room purifier",
    power: {
      kind: "entity-state",
      entity_id: entityId(301),
      entity_type: "hearth.power/v1",
    },
    pm25: {
      kind: "entity-state",
      entity_id: entityId(302),
      entity_type: "hearth.numericsensor/v1",
    },
    filter_life: {
      kind: "entity-state",
      entity_id: entityId(303),
      entity_type: "hearth.numericsensor/v1",
    },
    mode: {
      kind: "entity-state",
      entity_id: entityId(304),
      entity_type: "hearth.enumsetting/v1",
    },
    fan_level: {
      kind: "entity-state",
      entity_id: entityId(305),
      entity_type: "hearth.enumsetting/v1",
    },
    display: {
      kind: "entity-state",
      entity_id: entityId(306),
      entity_type: "hearth.power/v1",
    },
    child_lock: {
      kind: "entity-state",
      entity_id: entityId(307),
      entity_type: "hearth.power/v1",
    },
  },
} satisfies Meta<typeof BoundAirPurifier>;
export default meta;
type Story = StoryObj<typeof meta>;

export const FullCard: Story = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("switch", { name: /power/iu })
    ).toHaveAttribute("aria-checked", "true");
  },
};
export const NarrowFanFailure: Story = {
  decorators: [
    (Story) => (
      <div className="w-[248px]">
        <Story />
      </div>
    ),
  ],
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(`/v1/entities/${entityId(305)}/commands`, () =>
        HttpResponse.json({ detail: "Rejected" }, { status: 502 })
      )
    );
  },
  play: async ({ canvas, userEvent }) => {
    const raise = await canvas.findByRole("button", {
      name: "Raise fan level",
    });
    await canvas.findByRole("switch", { name: "Display" });
    await canvas.findByRole("switch", { name: "Child lock" });
    await canvas.findByText("Good");
    await canvas.findByText("Replace the filter soon");
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(raise);
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Fan level: Change rejected. Try again."
      );
      await expect(canvas.getByText("Fan level: Rejected.")).toBeVisible();
    });
  },
};
export const Tile: Story = {
  args: {
    layout: "tile",
    mode: undefined,
    fan_level: undefined,
    display: undefined,
    child_lock: undefined,
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Good")).toBeVisible();
    await expect(canvas.queryByText("Filter life")).not.toBeInTheDocument();
  },
};
export const Disabled: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(301, "disabled"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Disabled")).toBeVisible();
  },
};
export const TileWithModeUnavailable: Story = {
  args: { layout: "tile" },
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(304, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
    await expect(await canvas.findByText("Manual")).toBeVisible();
  },
};
