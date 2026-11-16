import type { ReactNode } from "react";

/** Label/value rows for detail cards. Values render monospace and wrap long IDs. */
export const Facts = ({ rows }: { rows: [string, ReactNode][] }) => (
  <dl data-slot="facts" className="grid gap-2">
    {rows.map(([label, value]) => (
      <div key={label} className="flex flex-wrap gap-x-4 gap-y-1">
        <dt className="text-muted-foreground w-32 shrink-0">{label}</dt>
        <dd className="text-caption min-w-0 flex-1 basis-48 font-mono wrap-anywhere">
          {value}
        </dd>
      </div>
    ))}
  </dl>
);
