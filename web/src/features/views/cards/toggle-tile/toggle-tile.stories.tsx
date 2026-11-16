import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, waitFor, within } from "storybook/test";

import { createEntities, entityId } from "../../../../../.storybook/fixtures";
import {
  createCoreHandlers,
  entityScenario,
} from "../../../../../.storybook/msw-handlers";
import { BoundToggleTile } from "./toggle-tile";

const meta = {
  title: "Components/Cards/ToggleTile",
  component: BoundToggleTile,
  tags: ["ai-generated"],
  args: {
    label: "Office plug",
    source: {
      kind: "entity-state",
      entity_id: entityId(4),
      entity_type: "hearth.power/v1",
    },
    metric: {
      source: {
        kind: "entity-state",
        entity_id: entityId(6),
        entity_type: "hearth.numericsensor/v1",
      },
      window: "24h",
      detail: {
        source: {
          kind: "entity-state",
          entity_id: entityId(7),
          entity_type: "hearth.numericsensor/v1",
        },
        label: "Total energy",
      },
    },
  },
  parameters: {
    docs: {
      description: {
        component:
          "A single power target with optional independent metering. Command success refreshes observed State; it does not optimistically invent State.",
      },
    },
  },
} satisfies Meta<typeof BoundToggleTile>;
export default meta;
type Story = StoryObj<typeof meta>;

export const DeviceName: Story = {
  args: { label: undefined, metric: undefined },
  beforeEach: ({ msw }) => {
    const entity = createEntities().find((item) => item.id === entityId(4));
    if (!entity) {
      throw new Error("Missing power Entity fixture");
    }
    msw.use(...createCoreHandlers([{ ...entity, name: "Power" }]));
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("article", { name: "Office plug" })
    ).toBeVisible();
    await expect(
      canvas.getByRole("switch", { name: "Office plug power" })
    ).toHaveAttribute("aria-checked", "false");
  },
};

export const MeteredPlug: Story = {
  play: async ({ canvas, userEvent }) => {
    const control = await canvas.findByRole("switch", {
      name: "Office plug power",
    });
    await expect(control).toHaveAttribute("aria-checked", "false");
    await userEvent.click(control);
    await waitFor(() =>
      expect(control).toHaveAttribute("aria-checked", "true")
    );
  },
};
export const Disabled: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(4, "disabled"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Disabled")).toBeVisible();
    await expect(canvas.getByRole("switch")).toBeDisabled();
  },
};
/** An Entity without a set Operation shows its State and offers no control. */
export const ReadOnly: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(4, "read-only"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Read-only")).toBeVisible();
    await expect(canvas.queryByRole("switch")).not.toBeInTheDocument();
  },
};
export const Rejected: Story = {
  beforeEach: ({ msw }) => {
    msw.use(
      http.post("/v1/entities/:id/commands", () =>
        HttpResponse.json({ detail: "Rejected" }, { status: 502 })
      )
    );
  },
  play: async ({ canvas, userEvent }) => {
    await userEvent.click(await canvas.findByRole("switch"));
    await expect(await canvas.findByRole("alert")).toHaveTextContent(
      "Change rejected. Try again."
    );
    await expect(canvas.getByRole("switch")).toHaveAttribute(
      "aria-checked",
      "false"
    );
  },
};
export const SourceDetails: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    await canvas.findByRole("switch");
    await userEvent.click(canvas.getByRole("button", { name: "Source" }));
    await expect(
      await within(canvasElement.ownerDocument.body).findByRole("link", {
        name: /Office plug/u,
      })
    ).toHaveAttribute("href", `/entities/${entityId(4)}`);
  },
};
export const MeteringUnavailable: Story = {
  beforeEach: ({ msw }) => {
    msw.use(entityScenario(6, "unavailable"));
  },
  play: async ({ canvas }) => {
    await expect(await canvas.findByText("Unavailable")).toBeVisible();
    await expect(canvas.getByText("7.9")).toBeVisible();
  },
};
