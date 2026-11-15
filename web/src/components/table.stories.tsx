import type { Meta, StoryObj } from "@storybook/react-vite";

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

export const Readings: Story = {};
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
