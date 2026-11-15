import { TableCell, TableRow } from "./table.tsx";

/** Tables render headers even with no rows; say so instead of showing a stub. */
export function EmptyRow({ colSpan, message }: { colSpan: number; message: string }) {
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell colSpan={colSpan} className="py-6 text-center text-muted-foreground">
        {message}
      </TableCell>
    </TableRow>
  );
}
