import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import {
  entityId,
  fixtureNow,
  observedAt,
} from "../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../.storybook/play";
import { CardSource } from "./card-source";

const meta = {
  title: "Components/Cards/CardSource",
  component: CardSource,
  tags: ["ai-generated"],
  args: {
    title: "Living room",
    observedAt,
    observedLabel: "Observed",
    now: Date.parse(fixtureNow),
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
          "A card's Source details: an anchored popover at a desk and a focus-contained sheet on a phone. Each source links to its Entity.",
      },
    },
  },
} satisfies Meta<typeof CardSource>;
export default meta;
type Story = StoryObj<typeof meta>;

export const SingleSource: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Source" }));
    const body = within(canvasElement.ownerDocument.body);
    await expect(
      await body.findByRole("heading", { name: "Reading details" })
    ).toBeVisible();
    await expect(
      body.getByRole("link", { name: "Living room" })
    ).toHaveAttribute("href", `/entities/${entityId(15)}`);
    await expect(body.getByText("Available")).toBeVisible();
  },
};
/** Each source of a multi-reading card states its own availability and enablement. */
export const SeveralSources: Story = {
  args: {
    title: "Rooms",
    observedLabel: "Oldest reading",
    sources: [
      {
        entityId: entityId(15),
        label: "Living room",
        availability: "available",
        enabled: true,
      },
      {
        entityId: entityId(114),
        label: "Bedroom temperature",
        availability: "unavailable",
        enabled: false,
      },
    ],
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Source" }));
    const body = within(canvasElement.ownerDocument.body);
    await expect(
      await body.findByRole("heading", { name: "Sources" })
    ).toBeVisible();
    await expect(body.getByText("Unavailable")).toBeVisible();
    await expect(body.getByText("Disabled")).toBeVisible();
  },
};
export const NeverObserved: Story = {
  args: { observedAt: undefined },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Source" }));
    await expect(
      await within(canvasElement.ownerDocument.body).findByRole("heading", {
        name: "Source details",
      })
    ).toBeVisible();
  },
};
export const PhoneSheet: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Source" }));
    const body = within(canvasElement.ownerDocument.body);
    await expect(
      await body.findByRole("dialog", { name: "Reading details" })
    ).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
    await userEvent.click(body.getByRole("button", { name: "Close details" }));
    await expect(
      body.queryByRole("dialog", { name: "Reading details" })
    ).not.toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Source" })).toHaveFocus();
  },
};

export const DesktopClose: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    const trigger = canvas.getByRole("button", { name: "Source" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    const panel = await body.findByRole("dialog", { name: "Reading details" });
    await expect(panel).not.toHaveAttribute("aria-modal", "true");
    await userEvent.click(
      within(panel).getByRole("button", { name: "Close details" })
    );
    await expect(
      body.queryByRole("dialog", { name: "Reading details" })
    ).not.toBeInTheDocument();
    await expect(trigger).toHaveFocus();
  },
};

export const DesktopEscape: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    const trigger = canvas.getByRole("button", { name: "Source" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    await expect(
      await body.findByRole("dialog", { name: "Reading details" })
    ).toBeVisible();
    await userEvent.keyboard("{Escape}");
    await expect(
      body.queryByRole("dialog", { name: "Reading details" })
    ).not.toBeInTheDocument();
    await expect(trigger).toHaveFocus();
  },
};

export const PhoneEscape: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const trigger = canvas.getByRole("button", { name: "Source" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    const panel = await body.findByRole("dialog", { name: "Reading details" });
    await expect(panel).toHaveAttribute("aria-modal", "true");
    const view = canvasElement.ownerDocument.defaultView;
    if (!view) {
      throw new Error("The story canvas has no window");
    }
    await expect(panel.getBoundingClientRect().bottom).toBeCloseTo(
      view.innerHeight,
      0
    );
    await expectNoHorizontalOverflow(canvasElement);
    await userEvent.keyboard("{Escape}");
    await expect(
      body.queryByRole("dialog", { name: "Reading details" })
    ).not.toBeInTheDocument();
    await expect(trigger).toHaveFocus();
  },
};
