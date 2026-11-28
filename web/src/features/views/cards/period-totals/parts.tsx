import type { ReactNode } from "react";

import { durationWords } from "../../bindings/changes";
import type { PeriodTotal } from "../../bindings/types";
import type { PeriodTotalsPeriod } from "./schema";

/** A partial period is tinted and outlined: it is a total still being counted. */
export const barClass =
  "bg-chart-1 data-[partial]:border data-[partial]:border-chart-1 data-[partial]:bg-[color-mix(in_oklch,var(--chart-1)_28%,var(--gray-1))]";

const hourWord = (start: number): string =>
  new Date(start).toLocaleTimeString([], { hour: "numeric" });

/** The short name of a period: a weekday, "Today", or an hour of the day. */
export const periodName = (
  period: PeriodTotal,
  kind: PeriodTotalsPeriod
): string => {
  if (kind === "hour") {
    return hourWord(period.start);
  }
  return period.partial
    ? "Today"
    : new Date(period.start).toLocaleDateString([], { weekday: "short" });
};

/** One sentence per period for assistive technology; the drawing carries the same facts. */
export const periodSentence = (
  period: PeriodTotal,
  kind: PeriodTotalsPeriod,
  trueWord: string
): string => {
  const name =
    kind === "day"
      ? new Date(period.start).toLocaleDateString([], { weekday: "long" })
      : new Date(period.start).toLocaleString([], {
          weekday: "short",
          hour: "numeric",
        });
  if (period.seconds === null) {
    return `${name}: no history.`;
  }
  return `${name}: ${durationWords(period.seconds)} ${trueWord.toLowerCase()}${period.partial ? " so far" : ""}.${period.gaps ? " Some of it has no history." : ""}`;
};

/** The chart's drawn part is hidden from assistive technology; its sentences are not. */
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
