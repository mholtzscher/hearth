import type { ReactNode } from "react";

import { cn } from "../../../../lib/utils";

/** The chart's answer in words: a compact reading and the sentence it belongs to. */
export const Answer = ({
  value,
  children,
}: {
  value: string;
  children: ReactNode;
}) => (
  <p className="text-label text-muted-foreground mb-4 flex flex-wrap items-baseline gap-x-2">
    <span className="text-compact-reading text-foreground">{value}</span>
    <span>{children}</span>
  </p>
);

/** A named plot region: sentences for assistive technology, the drawing hidden from it. */
export const Plot = ({
  label,
  summary,
  className,
  children,
}: {
  label: string;
  summary: string[];
  className?: string;
  children: ReactNode;
}) => (
  <section aria-label={label}>
    <ul className="sr-only">
      {summary.map((sentence, index) => (
        <li key={index}>{sentence}</li>
      ))}
    </ul>
    <div aria-hidden="true" className={className}>
      {children}
    </div>
  </section>
);

export interface KeyItem {
  label: string;
  /** Classes that draw the mark: a swatch, a line, a band, or a dot. */
  mark: string;
}

/** A legend: every mark is paired with words. */
export const Key = ({
  items,
  className,
}: {
  items: KeyItem[];
  className?: string;
}) => (
  <ul
    className={cn(
      "text-label flex list-none flex-wrap gap-x-4 gap-y-1",
      className
    )}
  >
    {items.map((item) => (
      <li className="inline-flex items-center gap-2" key={item.label}>
        <span className={cn("shrink-0", item.mark)} aria-hidden="true" />
        {item.label}
      </li>
    ))}
  </ul>
);
