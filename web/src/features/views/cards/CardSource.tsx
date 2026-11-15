import { useId, useState, useSyncExternalStore, type MouseEvent, type ReactNode } from "react";
import { ArrowUpRightIcon, XIcon } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "../../../components/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "../../../components/popover";
import { cn } from "../../../lib/utils";
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
    {source.availability !== undefined && <div className="grid gap-[3px]"><dt className="text-label text-muted-foreground">Entity availability</dt><dd>{source.availability === "available" ? "Available" : source.availability === "unavailable" ? "Unavailable" : "Unknown"}</dd></div>}
    {source.enabled !== undefined && <div className="grid gap-[3px]"><dt className="text-label text-muted-foreground">Entity enablement</dt><dd>{source.enabled ? "Enabled" : "Disabled"}</dd></div>}
  </>;
}

/** Source is out of the grid flow: an anchored popover at a desk, a focus-contained sheet on phones. */
export function CardSource({ title, observedAt, observedLabel, now, sources, toggleClassName, children }: {
  title: string;
  observedAt?: string;
  observedLabel: string;
  now: number;
  sources: SourceItem[];
  toggleClassName?: string;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const phone = useSyncExternalStore(subscribePhone, isPhone);
  const headingId = useId(), descriptionId = useId();
  const headingClass = "m-0 text-title leading-[1.4] font-semibold";
  const descriptionClass = "mt-1.5 mb-[22px] text-muted-foreground";
  const heading = <h2 id={headingId} className={headingClass}>{observedAt ? "Reading details" : "Source details"}</h2>;
  const description = <p id={descriptionId} className={descriptionClass}>{title}</p>;
  const age = observedAt ? observationAge(observedAt, now, "long") : undefined;
  const single = sources.length === 1 ? sources[0] : undefined;
  const trigger = <button type="button" data-slot="source-toggle" className={cn("ms-auto -me-1.5 inline-flex min-h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-sm px-1.5 py-0 text-muted-foreground hover:bg-gray-2 hover:text-foreground max-tablet:relative max-tablet:after:absolute max-tablet:after:inset-x-0 max-tablet:after:-inset-y-2 pointer-coarse:relative pointer-coarse:after:absolute pointer-coarse:after:inset-x-0 pointer-coarse:after:-inset-y-2", toggleClassName)} aria-expanded={open}>Source<ArrowUpRightIcon className="size-3.5" aria-hidden="true" /></button>;
  function closeOnNavigation(event: MouseEvent) {
    // Release the sheet before a route blocker opens its own changes dialog.
    if (event.target instanceof Element && event.target.closest("a[href]")) setOpen(false);
  }
  const content = <>
    <header className="flex items-start justify-between gap-5">
      {phone ? <DialogTitle className={headingClass}>{observedAt ? "Reading details" : "Source details"}</DialogTitle> : heading}
      <button type="button" className="-mt-2.5 -me-2.5 grid size-11 shrink-0 cursor-pointer place-items-center rounded-md hover:bg-gray-3 focus-visible:outline-2 focus-visible:outline-offset-3 focus-visible:outline-ring" aria-label="Close details" onClick={() => setOpen(false)}><XIcon className="size-[18px]" aria-hidden="true" /></button>
    </header>
    {phone ? <DialogDescription className={descriptionClass}>{title}</DialogDescription> : description}
    {(observedAt || single?.availability !== undefined || single?.enabled !== undefined) && <dl className="m-0 grid gap-4 tabular-nums">
      {observedAt && <div className="grid gap-[3px]"><dt className="text-label text-muted-foreground">{observedLabel === "Observed" ? "Observed at" : `${observedLabel} observed at`}</dt><dd>{Number.isFinite(Date.parse(observedAt)) ? <time dateTime={observedAt}>{new Date(observedAt).toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit", timeZoneName: "short" })}</time> : "Unavailable"}</dd></div>}
      {age && <div className="grid gap-[3px]"><dt className="text-label text-muted-foreground">Observation age</dt><dd>{age}</dd></div>}
      {single && <EntityFacts source={single} />}
    </dl>}
    {sources.length > 0 && <section className="mt-[22px] border-t border-gray-4 pt-[18px]">
      <h3 className="text-label leading-normal font-medium">{sources.length === 1 ? "Source" : "Sources"}</h3>
      <ul className="mt-2.5 grid list-none gap-4 p-0">{sources.map(source => <li key={source.entityId}>
        <Link className="inline-flex min-h-7 items-center text-link underline underline-offset-[3px] focus-visible:outline-2 focus-visible:outline-offset-3 focus-visible:outline-ring max-tablet:min-h-11" to={`/entities/${source.entityId}`}>{source.label}</Link>
        <code className="mt-1 block text-caption text-muted-foreground">{source.entityId}</code>
        {!single && (source.availability !== undefined || source.enabled !== undefined) && <dl className="mt-2 grid gap-2 tabular-nums"><EntityFacts source={source} /></dl>}
      </li>)}</ul>
    </section>}
    {children !== undefined && <section className="mt-[22px] grid gap-3 border-t border-gray-4 pt-[18px] text-muted-foreground">{children}</section>}
  </>;
  return phone ? <Dialog open={open} onOpenChange={setOpen}>
    <DialogTrigger asChild>{trigger}</DialogTrigger>
    <DialogContent placement="bottom" data-placement="bottom" className="max-tablet:rounded-t-[14px]" aria-modal="true" onClick={closeOnNavigation}>{content}</DialogContent>
  </Dialog> : <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger asChild>{trigger}</PopoverTrigger>
    <PopoverContent className="block" align="end" sideOffset={8} collisionPadding={12} aria-labelledby={headingId} aria-describedby={descriptionId} onClick={closeOnNavigation}>{content}</PopoverContent>
  </Popover>;
}
