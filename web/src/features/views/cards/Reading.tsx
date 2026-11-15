import type { ReactNode } from "react";
import { cn } from "../../../lib/utils";
import type { ValueTreatment } from "./status";
import type { StatusEntry } from "./status";
import { StatusList } from "./DataCard";

export type ReadingProps = {
  as?: "p" | "span";
  value?: ValueTreatment;
  className?: string;
  children: ReactNode;
};

/** The shared large reading, with unavailable and empty treatments. */
export function Reading({ as: Tag = "p", value, className, children }: ReadingProps) {
  return <Tag data-slot="reading" data-value={value} className={cn("text-reading leading-[1.15] font-[350] tracking-[-.03em] text-foreground tabular-nums data-[value=dimmed]:text-gray-8 data-[value=empty]:text-gray-6", className)}>{children}</Tag>;
}

export function ReadingUnit({ className, children }: { className?: string; children: ReactNode }) {
  return <small className={cn("ms-1 text-label tracking-normal text-muted-foreground", className)}>{children}</small>;
}

export type ReadingRowProps = {
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
};

/** A compact reading row carries its own status and afterglow. */
export function ReadingRow({ name, nameValue, value, arrival, status = [], className, beforeValue, afterValue, children }: ReadingRowProps) {
  // Flex avoids Safari retaining a grid column sized to the loading "—" and wrapping "Off".
  return <li data-slot="reading-row" className={cn("group/reading-row relative flex flex-wrap items-baseline gap-x-4 gap-y-0.5 py-2.5 tabular-nums not-first:border-t not-first:border-border", className)}>
    {arrival !== undefined && <span key={arrival} data-slot="reading-arrival" className="pointer-events-none absolute inset-x-0 -bottom-px z-[1] h-0.5 animate-arrival bg-orange group-last/reading-row:bottom-0" aria-hidden="true" />}
    <span data-slot="reading-name" className="min-w-0 flex-[1_1_9em] text-label leading-normal text-gray-9 [overflow-wrap:anywhere] data-[value=dimmed]:text-gray-8" data-value={nameValue}>{name}</span>
    {beforeValue}
    <span data-slot="reading-value" className="ms-auto max-w-full flex-none text-right text-compact-reading leading-[1.3] font-normal tracking-[-.02em] text-foreground [overflow-wrap:break-word] data-[value=dimmed]:text-gray-8 data-[value=empty]:text-gray-6" data-value={value}>{children}</span>
    {afterValue}
    <StatusList className="basis-full" entries={status} />
  </li>;
}
