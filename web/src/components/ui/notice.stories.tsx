import type { ReactNode } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";
import { Button } from "./button.tsx";

/** Tests-only baseline: the existing household notice before its shared component exists. */
function HouseholdNotice({ role, className = "", children }: {
  role: "alert" | "status"; className?: string; children: ReactNode;
}) {
  return <div className={`workspace-notice ${className}`} role={role}>{children}</div>;
}

const meta = {
  title: "components/ui/notice",
  component: HouseholdNotice,
  tags: ["ai-generated"],
  args: { role: "alert", children: "The dashboard could not be saved. Your changes are still here." },
} satisfies Meta<typeof HouseholdNotice>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Alert: Story = {};

export const Status: Story = {
  args: { role: "status", children: "Fix or discard the invalid editor values before saving." },
  play: async ({ canvas }) => {
    await expect(canvas.getByRole("status")).toHaveTextContent("Fix or discard");
  },
};

const retry = fn();
export const RecoveryAction: Story = {
  args: {
    children: <>
      History could not be loaded. Your prompt is preserved.
      <Button className="m-1.5" variant="outline" size="touch" onClick={retry}>Retry loading history</Button>
    </>,
  },
  beforeEach: () => { retry.mockClear(); },
  play: async ({ canvas, userEvent, canvasElement }) => {
    await userEvent.tab();
    await expect(canvas.getByRole("button", { name: "Retry loading history" })).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    await expect(retry).toHaveBeenCalledOnce();
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
      <HouseholdNotice {...args} />
    </div>
  ),
  play: async ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
