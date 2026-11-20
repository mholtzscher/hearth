import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect } from "storybook/test";

import { entityId } from "../../../../../.storybook/fixtures";
import { entityScenario } from "../../../../../.storybook/msw-handlers";
import { watchHeight } from "../../../../../.storybook/play";
import { BoundToggleList } from "./toggle-list";

const meta = {
  title: "Components/Cards/ToggleList",
  component: BoundToggleList,
  tags: ["ai-generated"],
  args: {
    title: "Office",
    all_off: true,
    rows: [
      {
        label: "Fan",
        source: {
          kind: "entity-state",
          entity_id: entityId(3),
          entity_type: "hearth.power/v1",
        },
      },
      {
        label: "Plug",
        source: {
          kind: "entity-state",
          entity_id: entityId(4),
          entity_type: "hearth.power/v1",
        },
      },
    ],
  },
} satisfies Meta<typeof BoundToggleList>;
export default meta;
type Story = StoryObj<typeof meta>;

export const AllOff: Story = {
  play: async ({ canvas, userEvent }) => {
    const fan = await canvas.findByRole("switch", { name: /Fan/u });
    await expect(fan).toHaveAttribute("aria-checked", "true");
    const plug = await canvas.findByRole("switch", { name: /Plug/u });
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(canvas.getByRole("button", { name: /All off/iu }));
      await expect(await canvas.findByText("1 of 1 Off")).toHaveRole("status");
      await expect(fan).toHaveAttribute("aria-checked", "false");
      await expect(plug).toHaveAttribute("aria-checked", "false");
    });
  },
};
export const NarrowGroupFailure: Story = {
  decorators: [
    (Story) => (
      <div className="w-[248px]">
        <Story />
      </div>
    ),
  ],
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/commands", () =>
        HttpResponse.json({ detail: "Rejected" }, { status: 502 })
      )
    );
  },
  play: async ({ canvas, userEvent }) => {
    await canvas.findByRole("switch", { name: /Plug/u });
    const allOff = await canvas.findByRole("button", { name: /All off/iu });
    await watchHeight(canvas.getByRole("article"), async () => {
      await userEvent.click(allOff);
      await expect(await canvas.findByText("0 of 1 Off · 1 failed")).toHaveRole(
        "status"
      );
      await expect(await canvas.findByRole("alert")).toHaveTextContent(
        "Change rejected. Try again."
      );
    });
  },
};
export const NoGroupAction: Story = {
  args: { all_off: undefined },
  play: async ({ canvas }) => {
    await canvas.findByRole("switch", { name: /Fan/u });
    await expect(
      canvas.queryByRole("button", { name: /All off/iu })
    ).not.toBeInTheDocument();
  },
};
export const OneTargetUnavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(3, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
  },
};
export const ReadOnlyRow: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(3, "read-only"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Read-only")).toBeVisible();
    await expect(
      canvas.queryByRole("switch", { name: /Fan/u })
    ).not.toBeInTheDocument();
    await expect(
      await canvas.findByRole("switch", { name: /Plug/u })
    ).toBeVisible();
  },
};
