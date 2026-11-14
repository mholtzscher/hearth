import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { deviceId } from "../../../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../../../.storybook/play";
import { BoundDevice } from "./Device";

const meta = {
  component: BoundDevice,
  tags: ["ai-generated"],
  args: { label: "Hallway lamp", device: { kind: "device", device_id: deviceId(2) } },
  parameters: { docs: { description: { component: "A capability-derived device card. A lamp combines one power Entity, one brightness Entity, and supported color controls." } } },
} satisfies Meta<typeof BoundDevice>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Lamp: Story = {
  play: async ({ canvas }) => { await expect(await canvas.findByRole("switch")).toHaveAttribute("aria-checked", "true"); },
};
/** The real color picker portal is exercised, rather than a story-only replacement. */
export const AdjustColor: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: /Adjust color/ }));
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByRole("heading", { name: "Light color" })).toBeVisible();
    await userEvent.clear(body.getByRole("textbox", { name: "Hex" }));
    await userEvent.type(body.getByRole("textbox", { name: "Hex" }), "#ff0000");
    await userEvent.click(body.getByRole("button", { name: "Set hex" }));
    await waitFor(() => expect(body.getByRole("textbox", { name: "Hex" })).toHaveValue("#FF0000"));
    await expect(await canvas.findByText(/Hue 0.*Saturation 100/)).toBeVisible();
    await expect(body.queryByRole("alert")).not.toBeInTheDocument();
  },
};
export const PhoneColorSheet: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(await canvas.findByRole("button", { name: /Adjust color/ }));
    const body = within(canvasElement.ownerDocument.body);
    await expect(await body.findByRole("dialog", { name: "Light color" })).toBeVisible();
    await userEvent.click(body.getByRole("button", { name: "Close color picker" }));
    await expect(canvas.getByRole("button", { name: /Adjust color/ })).toHaveFocus();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
