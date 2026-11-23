import { createContext, useContext, useId } from "react";
import type { MouseEventHandler, ReactNode } from "react";

import { Card } from "../../../components/card";
import { cn } from "../../../lib/utils";
import { StatusDot } from "../controls/status-dot";
import { CardSource } from "./card-source";
import type { SourceItem } from "./card-source";
import { observationAge, useArrivals, useMinuteClock } from "./observation";
import type { Arrival } from "./observation";
import type { StatusEntry } from "./status";

export type { SourceItem } from "./card-source";

/**
 * The heading level of a view's own Heading elements, set by the host: the
 * canvas title is h2, so its views start at h3; a chat artifact title is h3.
 * Card titles sit one level below.
 */
export const HeadingLevel = createContext<3 | 4>(3);

/** Status entries in precedence order: a tone dot and words, never color alone. */
export const StatusList = ({
  entries,
  className,
  separators = true,
}: {
  entries: StatusEntry[];
  className?: string;
  separators?: boolean;
}) => {
  if (entries.length === 0) {
    return null;
  }
  return (
    <ul
      data-slot="card-status"
      className={cn(
        "text-label text-gray-9 flex list-none flex-wrap gap-x-2 gap-y-0",
        className
      )}
    >
      {entries.map((entry, index) => (
        <li
          key={entry.key}
          data-tone={entry.tone}
          className={cn(
            "data-[tone=warn]:text-warning data-[tone=error]:text-destructive inline-flex items-baseline gap-1.5",
            separators &&
              index > 0 &&
              "before:text-gray-6 before:[content:'·'_/_'']"
          )}
        >
          <StatusDot tone={entry.tone} />
          <span>
            {entry.text}
            {entry.detail && (
              <span className="text-muted-foreground"> {entry.detail}</span>
            )}
          </span>
        </li>
      ))}
    </ul>
  );
};

/** The parts of a card that a caller may adjust. */
export type CardSlot = "body" | "footer" | "timeLabel" | "sourceToggle";
export interface DataCardProps {
  title: string;
  /** A chart's unit, shown beside its title. */
  unit?: string;
  status: StatusEntry[];
  /** Shown as the footer's observation time. */
  observedAt?: string;
  /** Names the footer time; a card of several readings states its oldest. */
  observedLabel?: string;
  /** Narrow single-reading and toggle cards may hide the visual prefix, preserving the full accessible label. */
  compactTimeLabel?: boolean;
  /** Displayed reading; eligible defaults to true. Availability and enablement determine eligibility. */
  arrival?: Arrival;
  arrivals?: (Arrival & { key: string })[];
  sources: SourceItem[];
  sourceExtra?: ReactNode;
  className?: string;
  "data-on"?: boolean;
  /** The whole-card press target supplies its press origin to the frame. */
  onClickCapture?: MouseEventHandler<HTMLElement>;
  "data-reduced-motion"?: "fade";
  /** Classes merged onto a named part; the caller's class wins conflicts. */
  classNames?: Partial<Record<CardSlot, string>>;
  children: ReactNode;
}

/** Compact age and Source details; a card without either draws no footer. */
const DataCardFooter = ({
  classNames,
  compactTimeLabel,
  observedAt,
  observedLabel,
  sources,
  sourceExtra,
  title,
}: {
  classNames?: Partial<Record<CardSlot, string>>;
  compactTimeLabel: boolean;
  observedAt?: string;
  observedLabel: string;
  sources: SourceItem[];
  sourceExtra?: ReactNode;
  title: string;
}) => {
  const now = useMinuteClock();
  const hasSource = sources.length > 0 || sourceExtra !== undefined;
  if (!observedAt && !hasSource) {
    return null;
  }
  const age = observedAt ? observationAge(observedAt, now) : undefined;
  const ageWords = observedAt
    ? observationAge(observedAt, now, "long")
    : undefined;
  const shortLabel =
    observedLabel === "Oldest reading" ? "Oldest" : observedLabel;
  return (
    <footer
      data-slot="card-footer"
      className={cn(
        "text-label text-muted-foreground mt-auto flex items-center gap-3",
        classNames?.footer
      )}
    >
      {observedAt &&
        (age ? (
          <time
            className="min-w-0 whitespace-nowrap"
            dateTime={observedAt}
            aria-label={`${observedLabel} ${ageWords}`}
          >
            {compactTimeLabel ? (
              <>
                <span
                  data-slot="card-time-label"
                  className={classNames?.timeLabel}
                >
                  {shortLabel}{" "}
                </span>
                {age}
              </>
            ) : (
              `${shortLabel} ${age}`
            )}
          </time>
        ) : (
          <span>Observation time unavailable</span>
        ))}
      {hasSource && (
        <CardSource
          title={title}
          observedAt={observedAt}
          observedLabel={observedLabel}
          now={now}
          sources={sources}
          toggleClassName={classNames?.sourceToggle}
        >
          {sourceExtra}
        </CardSource>
      )}
    </footer>
  );
};

/**
 * The one frame every data card uses (DESIGN.md, View cards), drawn on the shared
 * Card surface: a header with the title and status, the body, and a footer with
 * compact age and Source details.
 * Newer changed readings light the top edge and fade over ten seconds.
 */
export const DataCard = ({
  title,
  unit,
  status,
  observedAt,
  observedLabel = "Observed",
  compactTimeLabel = false,
  arrival,
  arrivals,
  sources,
  sourceExtra,
  className,
  classNames,
  children,
  "data-on": on,
  "data-reduced-motion": reducedMotion,
  onClickCapture,
}: DataCardProps) => {
  const level = useContext(HeadingLevel) + 1;
  const Title = `h${level}` as "h4";
  const titleId = useId();
  const glow = useArrivals(
    arrivals ?? (arrival ? [{ ...arrival, key: "reading" }] : [])
  );
  return (
    <Card
      asChild
      className={cn("relative flex flex-col gap-3 overflow-hidden", className)}
    >
      <article
        data-on={on || undefined}
        data-reduced-motion={reducedMotion}
        aria-labelledby={titleId}
        onClickCapture={onClickCapture}
      >
        {/* A new key restarts the fade for each arrival. */}
        {glow !== undefined && (
          <span
            key={glow}
            data-slot="card-arrival"
            className="animate-arrival bg-orange pointer-events-none absolute inset-x-0 top-0 h-0.5"
            aria-hidden="true"
          />
        )}
        <header
          data-slot="card-header"
          className="flex flex-wrap items-baseline gap-x-2 gap-y-1"
        >
          <Title
            id={titleId}
            data-slot="card-title"
            className="text-label text-foreground min-w-0 font-medium [overflow-wrap:anywhere]"
          >
            {title}
          </Title>
          {unit && (
            <span
              data-slot="card-unit"
              className="text-label text-muted-foreground"
            >
              {unit}
            </span>
          )}
          <StatusList entries={status} className="ms-auto justify-end" />
        </header>
        <div data-slot="card-body" className={cn("min-w-0", classNames?.body)}>
          {children}
        </div>
        <DataCardFooter
          classNames={classNames}
          compactTimeLabel={compactTimeLabel}
          observedAt={observedAt}
          observedLabel={observedLabel}
          sources={sources}
          sourceExtra={sourceExtra}
          title={title}
        />
      </article>
    </Card>
  );
};
