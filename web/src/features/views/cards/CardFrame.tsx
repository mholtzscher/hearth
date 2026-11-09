import { createContext, useContext, useId, type ReactNode } from "react";
import { observationAge, useArrivals, useMinuteClock, type Arrival } from "./observation";
import { CardSource, type SourceItem } from "./CardSource";
import type { StatusEntry } from "./status";

export type { SourceItem } from "./CardSource";

/**
 * The heading level of a view's own Heading elements, set by the host: the
 * canvas title is h2, so its views start at h3; a chat artifact title is h3.
 * Card titles sit one level below.
 */
export const HeadingLevel = createContext<3 | 4>(3);

/** Status entries in precedence order: a tone dot and words, never color alone. */
export function StatusList({ entries, className = "view-status" }: { entries: StatusEntry[]; className?: string }) {
  if (entries.length === 0) return null;
  return <ul className={className}>
    {entries.map(entry => <li key={entry.key} data-tone={entry.tone}>
      <span className="view-status-dot" aria-hidden="true" /><span>{entry.text}{entry.detail && <span className="view-status-detail"> {entry.detail}</span>}</span>
    </li>)}
  </ul>;
}

/**
 * The one frame every data card uses (DESIGN.md, View cards): a header with the
 * title and status, the body, and a footer with compact age and Source details.
 * When newer data changes what the card shows
 * while it is on screen, the top edge glows orange and fades over a minute.
 */
export function CardFrame({ title, unit, status, observedAt, observedLabel = "Observed", compactTimeLabel = false, arrival, arrivals, sources, sourceExtra, className, children }: {
  title: string;
  /** A chart's unit, shown beside its title. */
  unit?: string;
  status: StatusEntry[];
  /** Shown as the footer's observation time. */
  observedAt?: string;
  /** Names the footer time; a card of several readings states its oldest. */
  observedLabel?: string;
  /** A narrow PowerTile can hide only the visual prefix, preserving the full accessible label. */
  compactTimeLabel?: boolean;
  /** Displayed reading; eligible defaults to true. Availability and enablement determine eligibility. */
  arrival?: Arrival;
  arrivals?: Array<Arrival & { key: string }>;
  sources: SourceItem[];
  sourceExtra?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  const level = useContext(HeadingLevel) + 1;
  const Title = `h${level}` as "h4";
  const titleId = useId();
  const now = useMinuteClock();
  const age = observedAt ? observationAge(observedAt, now) : undefined;
  const ageWords = observedAt ? observationAge(observedAt, now, "long") : undefined;
  const shortLabel = observedLabel === "Oldest reading" ? "Oldest" : observedLabel;
  const glow = useArrivals(arrivals ?? (arrival ? [{ ...arrival, key: "reading" }] : []));
  const hasSource = sources.length > 0 || sourceExtra !== undefined;
  return <article className={`view-card${className ? ` ${className}` : ""}`} aria-labelledby={titleId}>
    {/* A new key restarts the fade for each arrival. */}
    {glow !== undefined && <span key={glow} className="view-card-arrival" aria-hidden="true" />}
    <header className="view-card-header">
      <Title id={titleId} className="view-card-title">{title}</Title>
      {unit && <span className="view-card-unit">{unit}</span>}
      <StatusList entries={status} />
    </header>
    <div className="view-card-body">{children}</div>
    {(observedAt || hasSource) && <footer className="view-card-footer">
      {observedAt && (age ? <time dateTime={observedAt} aria-label={`${observedLabel} ${ageWords}`}>{compactTimeLabel ? <><span className="view-card-time-label">{shortLabel} </span>{age}</> : `${shortLabel} ${age}`}</time> : <span>Observation time unavailable</span>)}
      {hasSource && <CardSource title={title} observedAt={observedAt} observedLabel={observedLabel} now={now} sources={sources}>{sourceExtra}</CardSource>}
    </footer>}
  </article>;
}
