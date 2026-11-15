import type { ReactNode } from "react";

import { Table, TableBody, TableCell, TableRow } from "./table.tsx";

/** Label/value rows for detail cards. Values render monospace and wrap long IDs. */
export const Facts = ({ rows }: { rows: [string, ReactNode][] }) => (
  <Table className="mt-1">
    <TableBody>
      {rows.map(([label, value]) => (
        <TableRow key={label} className="border-0 hover:bg-transparent">
          <TableCell className="text-muted-foreground w-36 px-0 py-0.5 align-top whitespace-normal">
            {label}
          </TableCell>
          <TableCell className="px-0 py-0.5 align-top font-mono text-xs wrap-anywhere whitespace-normal">
            {value}
          </TableCell>
        </TableRow>
      ))}
    </TableBody>
  </Table>
);
