import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import { expect } from "storybook/test";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";
import { Button } from "./button.tsx";
import { Notice } from "./notice.tsx";

const meta = {
  title: "components/ui/notice",
  component: Notice,
  tags: ["ai-generated"],
  args: { role: "alert", children: "The dashboard could not be saved. Your changes are still here." },
} satisfies Meta<typeof Notice>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Alert: Story = {
  play: async ({ canvas, canvasElement }) => {
    await canvasElement.ownerDocument.fonts.ready;
    const notice = canvas.getByRole("alert");
    const style = getComputedStyle(notice);
    for (const [property, value] of Object.entries({
      color: "rgb(244, 244, 244)", fontSize: "12px", fontWeight: "400",
      lineHeight: "18px", padding: "12px", marginTop: "12px", marginBottom: "12px",
      borderWidth: "1px", borderStyle: "solid", borderRadius: "8px",
    })) await expect(style.getPropertyValue(property.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)), property).toBe(value);
    await expect(style.fontFamily).toContain("Figtree Variable");
    const text = canvasElement.ownerDocument.createRange();
    text.selectNodeContents(notice);
    await expect(notice.getBoundingClientRect().height).toBe(26 + 18 * text.getClientRects().length);
    const parent = notice.parentElement!;
    const parentStyle = getComputedStyle(parent);
    const availableWidth = parent.clientWidth - parseFloat(parentStyle.paddingLeft) - parseFloat(parentStyle.paddingRight);
    await expect(notice.getBoundingClientRect().width).toBeCloseTo(Math.min(1080, availableWidth), 1);
    const context = canvasElement.ownerDocument.createElement("canvas").getContext("2d")!;
    const rgba = (color: string) => {
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = color;
      context.fillRect(0, 0, 1, 1);
      return [...context.getImageData(0, 0, 1, 1).data];
    };
    await expect(rgba(style.backgroundColor)).toEqual([255, 89, 128, 20]);
    await expect(rgba(style.borderColor)).toEqual([255, 91, 122, 115]);
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const Status: Story = {
  args: { role: "status", children: "Fix or discard the invalid editor values before saving." },
  play: async ({ canvas }) => {
    await expect(canvas.getByRole("status")).toHaveTextContent("Fix or discard");
  },
};

export const RecoveryAction: Story = {
  render: function RecoveryAction(args) {
    const [requested, setRequested] = useState(false);
    return <Notice {...args}>
      {requested ? "Retry requested. Your prompt is preserved." : "History could not be loaded. Your prompt is preserved."}
      <Button className="m-1.5" variant="outline" size="touch" onClick={() => setRequested(true)}>Retry loading history</Button>
    </Notice>;
  },
  play: async ({ canvas, userEvent, canvasElement }) => {
    await userEvent.tab();
    await expect(canvas.getByRole("button", { name: "Retry loading history" })).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    await expect(canvas.getByRole("alert")).toHaveTextContent("Retry requested. Your prompt is preserved.");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

export const Multiline: Story = {
  args: {
    role: "status",
    children: <>
      Fix or discard invalid editor values before saving.
      <p>Living room temperature: choose an Entity with a temperature field.</p>
      <p>Hallway light: choose an Entity that accepts a power Command.</p>
    </>,
  },
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
