import { useId, useState, useSyncExternalStore, type MouseEvent, type ReactNode } from "react";
import { ArrowUpRightIcon, XIcon } from "lucide-react";
import { Dialog, Popover } from "radix-ui";
import { Link } from "react-router-dom";
import type { ResourceMetadata } from "../bindings/types";
import { observationAge } from "./observation";

export type SourceItem = {
  entityId: string;
  label: string;
  availability?: ResourceMetadata["availability"];
  enabled?: boolean;
};

const phoneQuery = "(max-width: 720px)";
function subscribePhone(change: () => void) {
  const media = window.matchMedia?.(phoneQuery);
  media?.addEventListener("change", change);
  return () => media?.removeEventListener("change", change);
}
const isPhone = () => window.matchMedia?.(phoneQuery).matches ?? false;

function EntityFacts({ source }: { source: SourceItem }) {
  return <>
    {source.availability !== undefined && <div><dt>Entity availability</dt><dd>{source.availability === "available" ? "Available" : source.availability === "unavailable" ? "Unavailable" : "Unknown"}</dd></div>}
    {source.enabled !== undefined && <div><dt>Entity enablement</dt><dd>{source.enabled ? "Enabled" : "Disabled"}</dd></div>}
  </>;
}

/** Source is out of the grid flow: an anchored popover at a desk, a focus-contained sheet on phones. */
export function CardSource({ title, observedAt, observedLabel, now, sources, children }: {
  title: string;
  observedAt?: string;
  observedLabel: string;
  now: number;
  sources: SourceItem[];
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const phone = useSyncExternalStore(subscribePhone, isPhone);
  const headingId = useId(), descriptionId = useId();
  const heading = <h2 id={headingId}>{observedAt ? "Reading details" : "Source details"}</h2>;
  const description = <p id={descriptionId} className="view-source-description">{title}</p>;
  const age = observedAt ? observationAge(observedAt, now, "long") : undefined;
  const single = sources.length === 1 ? sources[0] : undefined;
  const trigger = <button type="button" className="view-source-toggle" aria-expanded={open}>Source<ArrowUpRightIcon aria-hidden="true" /></button>;
  function closeOnNavigation(event: MouseEvent) {
    // Release the sheet before a route blocker opens its own changes dialog.
    if (event.target instanceof Element && event.target.closest("a[href]")) setOpen(false);
  }
  const content = <>
    <header className="view-source-header">
      {phone ? <Dialog.Title>{observedAt ? "Reading details" : "Source details"}</Dialog.Title> : heading}
      <button type="button" className="view-source-close" aria-label="Close details" onClick={() => setOpen(false)}><XIcon aria-hidden="true" /></button>
    </header>
    {phone ? <Dialog.Description className="view-source-description">{title}</Dialog.Description> : description}
    {(observedAt || single?.availability !== undefined || single?.enabled !== undefined) && <dl className="view-source-facts">
      {observedAt && <div><dt>{observedLabel === "Observed" ? "Observed at" : `${observedLabel} observed at`}</dt><dd>{Number.isFinite(Date.parse(observedAt)) ? <time dateTime={observedAt}>{new Date(observedAt).toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit", timeZoneName: "short" })}</time> : "Unavailable"}</dd></div>}
      {age && <div><dt>Observation age</dt><dd>{age}</dd></div>}
      {single && <EntityFacts source={single} />}
    </dl>}
    {sources.length > 0 && <section className="view-source-section">
      <h3>{sources.length === 1 ? "Source" : "Sources"}</h3>
      <ul>{sources.map(source => <li key={source.entityId}>
        <Link to={`/entities/${source.entityId}`}>{source.label}</Link>
        <code>{source.entityId}</code>
        {!single && (source.availability !== undefined || source.enabled !== undefined) && <dl className="view-source-facts"><EntityFacts source={source} /></dl>}
      </li>)}</ul>
    </section>}
    {children !== undefined && <section className="view-source-section view-source-extra">{children}</section>}
  </>;
  return phone ? <Dialog.Root open={open} onOpenChange={setOpen}>
    <Dialog.Trigger asChild>{trigger}</Dialog.Trigger>
    <Dialog.Portal>
      <Dialog.Overlay className="view-source-backdrop" />
      <Dialog.Content className="view-source view-source-sheet" aria-modal="true" onClick={closeOnNavigation}>{content}</Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root> : <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>{trigger}</Popover.Trigger>
    <Popover.Portal>
      <Popover.Content className="view-source" align="end" sideOffset={8} collisionPadding={12} aria-labelledby={headingId} aria-describedby={descriptionId} onClick={closeOnNavigation}>{content}</Popover.Content>
    </Popover.Portal>
  </Popover.Root>;
}
