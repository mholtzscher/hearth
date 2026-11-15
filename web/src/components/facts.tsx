import type { ReactNode } from "react";
import { Table, TableBody, TableCell, TableRow } from "./table.tsx";

/** Label/value rows for detail cards. Values render monospace and wrap long IDs. */
export function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <Table className="mt-1">
      <TableBody>
        {rows.map(([label, value]) => (
          <TableRow key={label} className="border-0 hover:bg-transparent">
            <TableCell className="w-36 whitespace-normal px-0 py-0.5 align-top text-muted-foreground">
              {label}
            </TableCell>
            <TableCell className="px-0 py-0.5 align-top font-mono text-xs whitespace-normal wrap-anywhere">
              {value}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
