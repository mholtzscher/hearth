import type { Meta, StoryObj } from "@storybook/react-vite";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "./card";

const meta = {
  title: "Components/Card",
  component: Card,
  tags: ["ai-generated"],
  args: {
    children: <>
      <CardHeader>
        <CardTitle>Morning overview</CardTitle>
        <CardDescription>Household readings and controls</CardDescription>
      </CardHeader>
      <CardContent>Living room · 21.5 °C</CardContent>
      <CardFooter>Observed just now</CardFooter>
    </>,
  },
} satisfies Meta<typeof Card>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {};
export const Compact: Story = { args: { size: "sm" } };
