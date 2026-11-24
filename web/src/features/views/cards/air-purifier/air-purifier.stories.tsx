import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, waitFor } from "storybook/test";

import { entityId, fixtureEntity } from "../../../../../.storybook/fixtures";
import {
  createCoreHandlers,
  entityScenario,
} from "../../../../../.storybook/msw-handlers";
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
  name: "Motion · Rejected fan level",
  tags: ["motion"],
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
      await waitFor(() =>
        expect(canvas.getByText("Fan level: Rejected.")).toBeVisible()
      );
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

export const MotionPurifierTile: Story = {
  ...Tile,
  name: "Motion · Tile powers on",
  tags: ["motion"],
  beforeEach: ({ msw }) => {
    const power = fixtureEntity(301);
    if (!power.state) {
      throw new Error("Expected purifier power State");
    }
    msw.use(
      ...createCoreHandlers(
        [{ ...power, state: { ...power.state, value: false } }],
        { commandDelayMs: 400 }
      )
    );
  },
  play: async ({ canvas, userEvent }) => {
    const tile = canvas.getByRole("article", { name: "Living room purifier" });
    const control = await canvas.findByRole("switch", { name: /power/iu });
    await expect(await canvas.findByText("Good")).toBeVisible();
    await watchHeight(tile, async () => {
      await userEvent.click(control);
      await expect(control).toHaveAttribute("aria-checked", "false");
      await expect(
        await canvas.findByText("On", { exact: true })
      ).toBeVisible();
      await expect(tile).toHaveAttribute("data-on", "true");
    });
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

export const MotionRejectedTile: Story = {
  ...Tile,
  name: "Motion · Rejected tile power",
  tags: ["motion"],
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(`/v1/entities/${entityId(301)}/commands`, async () => {
        await delay(450);
        return HttpResponse.json({ detail: "Rejected" }, { status: 502 });
      })
    );
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const control = await canvas.findByRole("switch", { name: /power/iu });
    const tile = canvas.getByRole("article");
    await canvas.findByText("Good");
    await watchHeight(tile, async () => {
      await userEvent.click(control);
      await expect(await canvas.findByText("Turning off…")).toBeVisible();
      await expect(
        canvasElement.querySelector('[data-slot="pending-command-dot"]')
      ).toBeInTheDocument();
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Change rejected. Try again."
      );
      await expect(control).toHaveAttribute("aria-checked", "true");
      await expect(tile).toHaveAttribute("data-on", "true");
    });
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
