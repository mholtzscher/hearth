import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect, fn, within } from "storybook/test";
import { entityId, fixtureEntity } from "../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../.storybook/play";
import { createCoreHandlers } from "../../../../.storybook/msw-handlers";
import { useObservedEntity } from "../../../../.storybook/use-observed-entity";
import { LightColorPicker } from "./LightColorPicker";
import { colorHex, lightColorControls, readColor } from "./light-color";

const meta = {
  component: LightColorPicker,
  tags: ["ai-generated"],
  args: {
    controls: lightColorControls([fixtureEntity(12)]), activeId: entityId(12),
    name: "Hallway lamp", open: false, onOpenChange: fn(),
  },
  argTypes: { open: { control: false } },
  // The owning card holds the open state, so the story does too.
  render: function Picker(args) {
    const [open, setOpen] = useState(args.open);
    const entity = useObservedEntity(args.controls.color?.entity);
    const color = entity && args.controls.color ? { ...args.controls.color, entity } : undefined;
    const value = color && readColor(color.entity.state?.value, color.kind);
    return <LightColorPicker {...args} controls={{ ...args.controls, color }}
      observed={value ? colorHex(value) : undefined} open={open}
      onOpenChange={next => { args.onOpenChange(next); setOpen(next); }} />;
  },
  parameters: { docs: { description: { component: "A capability-derived color control: a popover at a desk and a sheet on a phone. It renders nothing for a light without a settable color or white Entity. These interactive stories own open state locally; use the trigger and close button rather than Controls." } } },
} satisfies Meta<typeof LightColorPicker>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Closed: Story = {
  play: async ({ canvas }) => { await expect(canvas.getByRole("button", { name: "Hallway lamp: Adjust color" })).toHaveAttribute("aria-expanded", "false"); },
};
export const Open: Story = {
  play: async ({ canvas, canvasElement, userEvent, args }) => {
    await userEvent.click(canvas.getByRole("button", { name: /Adjust color/ }));
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByRole("heading", { name: "Light color" })).toBeVisible();
    await expect(body.getByRole("textbox", { name: "Hex" })).toHaveValue("#5CADFF");
    await expect(args.onOpenChange).toHaveBeenLastCalledWith(true);
  },
};
export const ColorNeverObserved: Story = {
  args: { controls: lightColorControls([fixtureEntity(12, { state: null })]) },
  beforeEach: ({ msw }) => { msw.use(...createCoreHandlers([fixtureEntity(12, { state: null })])); },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: /Adjust color/ }));
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByText("No color observed")).toBeVisible();
    await expect(body.getByText("Color has not been observed yet.")).toBeVisible();
    await expect(body.getByRole("textbox", { name: "Hex" })).toHaveValue("#FFFFFF");
  },
};
export const SetRed: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: /Adjust color/ }));
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
  beforeEach: ({ msw }) => { msw.use(...createCoreHandlers([fixtureEntity(12, { state: null })])); },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: /Adjust color/ }));
    const body = within(canvasElement.ownerDocument.body);
    await body.findByText("No color observed");
    const hex = body.getByRole("textbox", { name: "Hex" });
    await userEvent.clear(hex);
    await userEvent.type(hex, "#ff0000");
    await userEvent.click(body.getByRole("button", { name: "Set hex" }));
    await expect(await body.findByText("#FF0000")).toBeVisible();
    await expect(body.queryByText("Color has not been observed yet.")).not.toBeInTheDocument();
  },
};
export const PhoneSheet: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: /Adjust color/ }));
    await expect(await within(canvasElement.ownerDocument.body).findByRole("dialog", { name: "Light color" })).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
export const WhiteAndColor: Story = {
  args: {
    controls: lightColorControls([
      fixtureEntity(12),
      fixtureEntity(12, {
        id: entityId(16), type: "hearth.colortemp/v1", name: "Lamp white",
        support: { state: { minimum: 153, maximum: 499 }, operations: { set: { step: 5 } } },
        state: { ...fixtureEntity(12).state!, value: { active: false, value: 370 } },
      }),
    ]),
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: /Adjust white or color/ }));
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(await body.findByRole("button", { name: "White" }));
    await expect(body.getByText(/Warm ·/)).toBeVisible();
    await expect(body.getByText(/Cool ·/)).toBeVisible();
  },
};
export const InlineFeedback: Story = {
  args: { inlineFeedback: true },
  play: async ({ canvas }) => {
    await expect(canvas.getByRole("button", { name: /Adjust color/ })).toBeVisible();
  },
};
