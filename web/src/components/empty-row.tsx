import { TableCell, TableRow } from "./table.tsx";

/** Tables render headers even with no rows; say so instead of showing a stub. */
export const EmptyRow = ({
  colSpan,
  message,
}: {
  colSpan: number;
  message: string;
}) => (
  <TableRow className="hover:bg-transparent">
    <TableCell
      colSpan={colSpan}
      className="text-muted-foreground py-6 text-center"
    >
      {message}
    </TableCell>
  </TableRow>
);
