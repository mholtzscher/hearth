import { createContext, useContext, useId, useState, type CSSProperties, type ReactNode } from "react";
import { ChevronDownIcon } from "lucide-react";
import { afterglow, observationAge, useMinuteClock } from "./metric/afterglow";
import type { StatusEntry } from "./status";

/**
 * The heading level of a view's own Heading elements, set by the host: the
 * canvas title is h2, so its views start at h3; a chat artifact title is h3.
 * Card titles sit one level below.
 */
export const HeadingLevel = createContext<3 | 4>(3);

export type SourceItem = { entityId: string; label: string };

/** Status entries in precedence order: a tone dot and words, never color alone. */
export function StatusList({ entries, className = "view-status" }: { entries: StatusEntry[]; className?: string }) {
  if (entries.length === 0) return null;
  return <ul className={className}>
    {entries.map(entry => <li key={entry.key} data-tone={entry.tone}>
      <span className="view-status-dot" aria-hidden="true" />{entry.text}{entry.detail && <span className="view-status-detail"> {entry.detail}</span>}
    </li>)}
  </ul>;
}

/**
 * The one frame every data card uses (DESIGN.md, View cards): a header with the
 * title and status, the body, and a footer with the observation time and the
 * Source disclosure. Only a card that shows one current reading passes
 * observedAt with glows, which draws the afterglow edge.
 */
export function CardFrame({ title, unit, status, observedAt, glows = false, sources, sourceExtra, className, children }: {
  title: string;
  /** A chart's unit, shown beside its title. */
  unit?: string;
  status: StatusEntry[];
  observedAt?: string;
  glows?: boolean;
  sources: SourceItem[];
  sourceExtra?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  const level = useContext(HeadingLevel) + 1;
  const Title = `h${level}` as "h4";
  const titleId = useId();
  const panelId = useId();
  const [open, setOpen] = useState(false);
  const now = useMinuteClock();
  const age = observedAt ? observationAge(observedAt, now) : undefined;
  const glow = observedAt && glows ? afterglow(observedAt, now) : undefined;
  const hasSource = sources.length > 0 || sourceExtra !== undefined;
  return <article className={`view-card${className ? ` ${className}` : ""}`} aria-labelledby={titleId}
    data-afterglow={glow === undefined ? undefined : ""} style={glow === undefined ? undefined : { "--afterglow": glow } as CSSProperties}>
    <header className="view-card-header">
      <Title id={titleId} className="view-card-title">{title}</Title>
      {unit && <span className="view-card-unit">{unit}</span>}
      <StatusList entries={status} />
    </header>
    <div className="view-card-body">{children}</div>
    {(observedAt || hasSource) && <footer className="view-card-footer">
      {observedAt && <time dateTime={observedAt}>Observed {new Date(observedAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}{age && ` · ${age}`}</time>}
      {hasSource && <button type="button" className="view-source-toggle" aria-expanded={open} aria-controls={panelId} onClick={() => setOpen(value => !value)}>
        Source<ChevronDownIcon aria-hidden="true" />
      </button>}
      {hasSource && <div id={panelId} className="view-source" hidden={!open}>
        {sources.length > 0 && <ul>{sources.map(source => <li key={source.entityId}>
          <a href={`#/entities/${source.entityId}`}>{source.label}</a> <code>{source.entityId}</code>
        </li>)}</ul>}
        {sourceExtra}
      </div>}
    </footer>}
  </article>;
}
