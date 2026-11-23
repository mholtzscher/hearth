import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { useState } from "react";
import type { ComponentProps } from "react";
import { expect, fn, within } from "storybook/test";

import {
  entityId,
  fixtureEntity,
  observedAt,
} from "../../../../.storybook/fixtures";
import { createCoreHandlers } from "../../../../.storybook/msw-handlers";
import { expectNoHorizontalOverflow } from "../../../../.storybook/play";
import { useObservedEntity } from "../../../../.storybook/use-observed-entity";
import { colorHex, lightColorControls, readColor } from "./light-color";
import { LightColorPicker } from "./light-color-picker";

const Picker = (args: ComponentProps<typeof LightColorPicker>) => {
  const [open, setOpen] = useState(args.open);
  const entity = useObservedEntity(args.controls.color?.entity);
  const color =
    entity && args.controls.color
      ? { ...args.controls.color, entity }
      : undefined;
  const value = color && readColor(color.entity.state?.value, color.kind);
  return (
    <LightColorPicker
      {...args}
      controls={{ ...args.controls, color }}
      observed={value ? colorHex(value) : undefined}
      open={open}
      onOpenChange={(next) => {
        args.onOpenChange(next);
        setOpen(next);
      }}
    />
  );
};

const meta = {
  title: "Components/Controls/LightColorPicker",
  component: LightColorPicker,
  tags: ["ai-generated"],
  args: {
    controls: lightColorControls([fixtureEntity(12)]),
    activeId: entityId(12),
    name: "Hallway lamp",
    open: false,
    onOpenChange: fn(),
  },
  argTypes: { open: { control: false } },
  // The owning card holds the open state, so the story does too.
  render: Picker,
  parameters: {
    docs: {
      description: {
        component:
          "A capability-derived color control: a popover at a desk and a sheet on a phone. It renders nothing for a light without a settable color or white Entity. These interactive stories own open state locally; use the trigger and close button rather than Controls.",
      },
    },
  },
} satisfies Meta<typeof LightColorPicker>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Closed: Story = {
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("button", { name: "Hallway lamp: Adjust color" })
    ).toHaveAttribute("aria-expanded", "false");
  },
};
export const Open: Story = {
  play: async ({ canvas, canvasElement, userEvent, args }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: /Adjust color/u })
    );
    const body = within(canvasElement.ownerDocument.body);
    await expect(
      await body.findByRole("heading", { name: "Light color" })
    ).toBeVisible();
    await expect(body.getByRole("textbox", { name: "Hex" })).toHaveValue(
      "#5CADFF"
    );
    await expect(args.onOpenChange).toHaveBeenLastCalledWith(true);
  },
};
export const ColorNeverObserved: Story = {
  args: { controls: lightColorControls([fixtureEntity(12, { state: null })]) },
  beforeEach: ({ msw }) => {
    msw.use(...createCoreHandlers([fixtureEntity(12, { state: null })]));
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: /Adjust color/u })
    );
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByText("No color observed")).toBeVisible();
    await expect(
      body.getByText("Color has not been observed yet.")
    ).toBeVisible();
    await expect(body.getByRole("textbox", { name: "Hex" })).toHaveValue(
      "#FFFFFF"
    );
  },
};
export const SetRed: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: /Adjust color/u })
    );
    const body = within(canvasElement.ownerDocument.body);
    const hex = await body.findByRole("textbox", { name: "Hex" });
    await userEvent.clear(hex);
    await userEvent.type(hex, "#ff0000");
    await userEvent.click(body.getByRole("button", { name: "Set hex" }));
    await expect(await body.findByText("#FF0000")).toBeVisible();
    await expect(body.queryByRole("alert")).not.toBeInTheDocument();
  },
};
export const FirstObservation: Story = {
  args: { controls: lightColorControls([fixtureEntity(12, { state: null })]) },
  beforeEach: ({ msw }) => {
    msw.use(...createCoreHandlers([fixtureEntity(12, { state: null })]));
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: /Adjust color/u })
    );
    const body = within(canvasElement.ownerDocument.body);
    await body.findByText("No color observed");
    const hex = body.getByRole("textbox", { name: "Hex" });
    await userEvent.clear(hex);
    await userEvent.type(hex, "#ff0000");
    await userEvent.click(body.getByRole("button", { name: "Set hex" }));
    await expect(await body.findByText("#FF0000")).toBeVisible();
    await expect(
      body.queryByText("Color has not been observed yet.")
    ).not.toBeInTheDocument();
  },
};
export const PhoneSheet: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: /Adjust color/u })
    );
    await expect(
      await within(canvasElement.ownerDocument.body).findByRole("dialog", {
        name: "Light color",
      })
    ).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
const desktopDismissal: NonNullable<Story["play"]> = async ({
  canvas,
  canvasElement,
  userEvent,
}) => {
  const trigger = canvas.getByRole("button", { name: /Adjust color/u });
  const body = within(canvasElement.ownerDocument.body);
  await userEvent.click(trigger);
  await expect(
    await body.findByRole("dialog", { name: "Light color" })
  ).toBeVisible();
  await userEvent.keyboard("{Escape}");
  await expect(
    body.queryByRole("dialog", { name: "Light color" })
  ).not.toBeInTheDocument();
  await expect(trigger).toHaveFocus();
  await userEvent.click(trigger);
  await userEvent.click(
    await body.findByRole("button", { name: "Close color picker" })
  );
  await expect(trigger).toHaveFocus();
};

export const DesktopDismissal: Story = {
  play: desktopDismissal,
};
export const PhoneDismissal: Story = {
  ...DesktopDismissal,
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async (context) => {
    await desktopDismissal(context);
    await expectNoHorizontalOverflow(context.canvasElement);
  },
};
export const WhiteAndColor: Story = {
  args: {
    controls: lightColorControls([
      fixtureEntity(12),
      fixtureEntity(12, {
        id: entityId(16),
        type: "hearth.colortemp/v1",
        name: "Lamp white",
        support: {
          state: { minimum: 153, maximum: 499 },
          operations: { set: { step: 5 } },
        },
        state: {
          observation_id: "obs_story_12",
          adapter_received_at: observedAt,
          observed_at: observedAt,
          value: { active: false, value: 370 },
        },
      }),
    ]),
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: /Adjust white or color/u })
    );
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await body.findByRole("button", { name: "White" }));
    await expect(body.getByText(/Warm ·/u)).toBeVisible();
    await expect(body.getByText(/Cool ·/u)).toBeVisible();
  },
};
export const InlineFeedback: Story = {
  name: "Motion · Rejected color",
  tags: ["motion"],
  args: { inlineFeedback: true },
  beforeEach: ({ msw }) => {
    msw.use(
      http.post(`/v1/entities/${entityId(12)}/commands`, () =>
        HttpResponse.json({ detail: "Rejected" }, { status: 502 })
      )
    );
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const trigger = canvas.getByRole("button", { name: /Adjust color/u });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    const hex = await body.findByRole("textbox", { name: "Hex" });
    await userEvent.clear(hex);
    await userEvent.type(hex, "#ff0000");
    await userEvent.click(body.getByRole("button", { name: "Set hex" }));
    await expect(await body.findByRole("alert")).toHaveTextContent(
      "Color: The light rejected the change."
    );
    await userEvent.click(
      body.getByRole("button", { name: "Close color picker" })
    );
    await expect(await canvas.findByRole("alert")).toHaveTextContent(
      "Color rejected"
    );
    await expect(canvas.getByText("Failed")).toBeVisible();
    await expect(trigger).toHaveAccessibleDescription("Color rejected");
  },
};
