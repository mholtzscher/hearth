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

export const Alert: Story = {};

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

/** Temporary reference; remove when the workspace adopts Notice. */
export const HouseholdComparison: Story = {
  render: (args) => (
    <div className="grid gap-4">
      <div className="workspace-notice" role="alert">{args.children}</div>
      <Notice {...args} />
    </div>
  ),
  play: async ({ canvas, canvasElement }) => {
    const [legacy, shared] = canvas.getAllByRole("alert").map(element => getComputedStyle(element));
    for (const property of ["height", "width", "color", "fontFamily", "fontSize", "fontWeight", "lineHeight", "padding", "margin", "borderWidth", "borderStyle"] as const) {
      await expect(shared[property], property).toBe(legacy[property]);
    }
    // Tailwind mixes in oklab; legacy CSS uses oklch. Compare their displayed RGBA values.
    const context = canvasElement.ownerDocument.createElement("canvas").getContext("2d")!;
    const rgba = (color: string) => {
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = color;
      context.fillRect(0, 0, 1, 1);
      return [...context.getImageData(0, 0, 1, 1).data];
    };
    await expect(rgba(shared.backgroundColor)).toEqual(rgba(legacy.backgroundColor));
    await expect(rgba(shared.borderColor)).toEqual(rgba(legacy.borderColor));
    await expect(legacy.borderRadius).toBe("9px");
    await expect(shared.borderRadius).toBe("8px");
    await expectNoHorizontalOverflow(canvasElement);
  },
};
