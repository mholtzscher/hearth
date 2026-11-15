import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { RawJson } from "./raw-json.tsx";

const meta = {
  title: "Components/RawJson",
  component: RawJson,
  tags: ["ai-generated"],
  args: { value: { name: "Living room", value: 21.5 }, title: "Raw reading JSON" },
} satisfies Meta<typeof RawJson>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Expand: Story = {
  play: async ({ canvas, userEvent }) => {
    const toggle = canvas.getByRole("button", { name: "Raw reading JSON" });
    await userEvent.click(toggle);
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(canvas.getByRole("region")).toHaveTextContent('"Living room"');
  },
};
export const PageSection: Story = {
  args: { headingLevel: 2 },
  play: async ({ canvas }) => { await expect(canvas.getByRole("heading", { level: 2, name: "Raw reading JSON" })).toBeVisible(); },
};
