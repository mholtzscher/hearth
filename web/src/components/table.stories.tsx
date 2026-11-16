import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";

import { expectNoHorizontalOverflow } from "../../.storybook/play";
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./table";

const meta = {
  title: "Components/Table",
  component: Table,
  tags: ["ai-generated"],
  args: {
    children: (
      <>
        <TableCaption>Observed household readings</TableCaption>
        <TableHeader>
          <TableRow>
            <TableHead>Entity</TableHead>
            <TableHead>State</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow>
            <TableCell>Living room</TableCell>
            <TableCell>21.5 °C</TableCell>
          </TableRow>
          <TableRow>
            <TableCell>Front door</TableCell>
            <TableCell>Closed</TableCell>
          </TableRow>
        </TableBody>
      </>
    ),
  },
} satisfies Meta<typeof Table>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Readings: Story = {
  play: async ({ canvas, userEvent }) => {
    const header = canvas.getByRole("row", { name: "Entity State" });
    await userEvent.hover(header);
    await expect(getComputedStyle(header).backgroundColor).toBe(
      "rgba(0, 0, 0, 0)"
    );
  },
};
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  args: { className: "min-w-[600px]" },
  play: async ({ canvas, canvasElement, userEvent }) => {
    const scroller = await canvas.findByRole("region", {
      name: "Scrollable table",
    });
    await userEvent.tab();
    await expect(scroller).toHaveFocus();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
export const Empty: Story = {
  args: {
    children: (
      <>
        <TableCaption>Observed household readings</TableCaption>
        <TableHeader>
          <TableRow>
            <TableHead>Entity</TableHead>
            <TableHead>State</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow>
            <TableCell colSpan={2}>No Entities yet.</TableCell>
          </TableRow>
        </TableBody>
      </>
    ),
  },
};
