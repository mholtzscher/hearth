import type { ReactNode } from "react";

import { cn } from "../../../lib/utils";
import { StatusList } from "./data-card";
import type { ValueTreatment, StatusEntry } from "./status";

export interface ReadingProps {
  as?: "p" | "span";
  value?: ValueTreatment;
  className?: string;
  children: ReactNode;
}

const emptyStatus: StatusEntry[] = [];

/** The shared large reading, with unavailable and empty treatments. */
export const Reading = ({
  as: Tag = "p",
  value,
  className,
  children,
}: ReadingProps) => (
  <Tag
    data-slot="reading"
    data-value={value}
    className={cn(
      "text-reading text-foreground data-[value=dimmed]:text-gray-8 data-[value=empty]:text-gray-6 leading-[1.15] font-[350] tracking-[-.03em] tabular-nums",
      className
    )}
  >
    {children}
  </Tag>
);

export const ReadingUnit = ({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) => (
  <small
    className={cn(
      "text-label text-muted-foreground ms-1 tracking-normal",
      className
    )}
  >
    {children}
  </small>
);

export interface ReadingRowProps {
  name: ReactNode;
  nameValue?: ValueTreatment;
  value?: ValueTreatment;
  arrival?: number;
  status?: StatusEntry[];
  /** Additional classes on the row itself. */
  className?: string;
  /** Additional content alongside the name and immediately before the value. */
  beforeValue?: ReactNode;
  /** Additional content between the value and the status list. */
  afterValue?: ReactNode;
  children: ReactNode;
}

/** A compact reading row carries its own status and afterglow. */
export const ReadingRow = ({
  name,
  nameValue,
  value,
  arrival,
  status = emptyStatus,
  className,
  beforeValue,
  afterValue,
  children,
}: ReadingRowProps) => (
  // Flex avoids Safari retaining a grid column sized to the loading "—" and wrapping "Off".
  <li
    data-slot="reading-row"
    className={cn(
      "group/reading-row not-first:border-border relative flex flex-wrap items-baseline gap-x-4 gap-y-0.5 py-2.5 tabular-nums not-first:border-t",
      className
    )}
  >
    {arrival !== undefined && (
      <span
        key={arrival}
        data-slot="reading-arrival"
        className="animate-arrival bg-orange pointer-events-none absolute inset-x-0 -bottom-px z-[1] h-0.5 group-last/reading-row:bottom-0"
        aria-hidden="true"
      />
    )}
    <span
      data-slot="reading-name"
      className="text-label text-gray-9 data-[value=dimmed]:text-gray-8 min-w-0 flex-[1_1_9em] leading-normal [overflow-wrap:anywhere]"
      data-value={nameValue}
    >
      {name}
    </span>
    {beforeValue}
    <span
      data-slot="reading-value"
      className="text-compact-reading text-foreground data-[value=dimmed]:text-gray-8 data-[value=empty]:text-gray-6 ms-auto max-w-full flex-none text-right leading-[1.3] font-normal tracking-[-.02em] [overflow-wrap:break-word]"
      data-value={value}
    >
      {children}
    </span>
    {afterValue}
    <StatusList className="basis-full" entries={status} />
  </li>
);
