import type { ReactNode } from "react";

import { cn } from "../../../../lib/utils";
import { DataCard } from "../../cards/data-card";
import type { SourceItem } from "../../cards/data-card";

/** A synthetic Entity for a prototype's Source details. */
export const source = (number: number, label: string): SourceItem => ({
  entityId: `ent_019a3e6a-4111-7000-8000-${String(number).padStart(12, "0")}`,
  label,
  availability: "available",
  enabled: true,
});

/** The shared card frame, with the chart's method in Source rather than a caption. */
export const ChartCard = ({
  title,
  unit,
  sources,
  method,
  children,
}: {
  title: string;
  unit?: string;
  sources: SourceItem[];
  method: string;
  children: ReactNode;
}) => (
  <DataCard
    className="@container"
    title={title}
    unit={unit}
    status={[]}
    sources={sources}
    sourceExtra={<p>{method}</p>}
  >
    {children}
  </DataCard>
);

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
      {summary.map((sentence) => (
        <li key={sentence}>{sentence}</li>
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

/** The tooltip surface the TimeSeries card uses. */
export const Tip = ({ title, rows }: { title: string; rows: string[] }) => (
  <div className="border-gray-4 bg-gray-2 text-label text-foreground grid gap-0.5 rounded-md border px-2.5 py-2">
    <p className="text-muted-foreground">{title}</p>
    {rows.map((row) => (
      <p key={row}>{row}</p>
    ))}
  </div>
);

/** Shared Recharts axis styling: gray-8 caption labels, no tick marks. */
export const axis = {
  stroke: "var(--gray-8)",
  tickLine: false,
} as const;
