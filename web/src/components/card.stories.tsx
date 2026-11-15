import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";

import { Card } from "./card";

const meta = {
  title: "Components/Card",
  component: Card,
  tags: ["ai-generated"],
  args: {
    children: (
      <>
        <h2 className="text-label font-medium">Morning overview</h2>
        <p className="text-label text-muted-foreground">
          Household readings and controls
        </p>
      </>
    ),
  },
  parameters: {
    docs: {
      description: {
        component:
          "The card surface: 12px corners, a 1px neutral border, and the card background. View data cards build on it through DataCard.",
      },
    },
  },
} satisfies Meta<typeof Card>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
/** The denser card that Development pages use. */
export const Compact: Story = { args: { size: "compact" } };
/** The card renders its child element, keeping the child's semantics. */
export const AsArticle: Story = {
  args: {
    asChild: true,
    children: (
      <article aria-label="Morning overview">
        <h2 className="text-label font-medium">Morning overview</h2>
      </article>
    ),
  },
  play: async ({ canvas }) => {
    const card = canvas.getByRole("article", { name: "Morning overview" });
    await expect(card).toHaveAttribute("data-slot", "card");
  },
};
