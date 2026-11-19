import { ArrowUpRightIcon, XIcon } from "lucide-react";
import { useId, useState, useSyncExternalStore } from "react";
import type { MouseEvent, ReactNode } from "react";
import { Link } from "react-router-dom";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "../../../components/dialog";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "../../../components/popover";
import { cn } from "../../../lib/utils";
import type { ResourceMetadata } from "../bindings/types";
import { observationAge } from "./observation";

export interface SourceItem {
  entityId: string;
  label: string;
  availability?: ResourceMetadata["availability"];
  enabled?: boolean;
}

const phoneQuery = "(max-width: 720px)";
const subscribePhone = (change: () => void) => {
  const media = window.matchMedia?.(phoneQuery);
  media?.addEventListener("change", change);
  return () => media?.removeEventListener("change", change);
};
const isPhone = () => window.matchMedia?.(phoneQuery).matches ?? false;

const availabilityLabel = (
  availability: NonNullable<SourceItem["availability"]>
) => {
  if (availability === "available") {
    return "Available";
  }
  if (availability === "unavailable") {
    return "Unavailable";
  }
  return "Unknown";
};

const singleSource = (sources: SourceItem[]) =>
  sources.length === 1 ? sources[0] : undefined;

const EntityFacts = ({ source }: { source: SourceItem }) => (
  <>
    {source.availability !== undefined && (
      <div className="grid gap-[3px]">
        <dt className="text-label text-muted-foreground">
          Entity availability
        </dt>
        <dd>{availabilityLabel(source.availability)}</dd>
      </div>
    )}
    {source.enabled !== undefined && (
      <div className="grid gap-[3px]">
        <dt className="text-label text-muted-foreground">Entity enablement</dt>
        <dd>{source.enabled ? "Enabled" : "Disabled"}</dd>
      </div>
    )}
  </>
);

/** Source is out of the grid flow: an anchored popover at a desk, a focus-contained sheet on phones. */
export const CardSource = ({
  title,
  observedAt,
  observedLabel,
  now,
  sources,
  toggleClassName,
  children,
}: {
  title: string;
  observedAt?: string;
  observedLabel: string;
  now: number;
  sources: SourceItem[];
  toggleClassName?: string;
  children?: ReactNode;
}) => {
  const [open, setOpen] = useState(false);
  const phone = useSyncExternalStore(subscribePhone, isPhone);
  const headingId = useId();
  const descriptionId = useId();
  const headingClass = "m-0 text-title";
  const descriptionClass = "mt-1.5 mb-[22px] text-muted-foreground";
  const heading = (
    <h2 id={headingId} className={headingClass}>
      {observedAt ? "Reading details" : "Source details"}
    </h2>
  );
  const description = (
    <p id={descriptionId} className={descriptionClass}>
      {title}
    </p>
  );
  const age = observedAt ? observationAge(observedAt, now, "long") : undefined;
  const single = singleSource(sources);
  const trigger = (
    <button
      type="button"
      data-slot="source-toggle"
      className={cn(
        "text-muted-foreground hover:bg-gray-2 hover:text-foreground max-tablet:relative max-tablet:after:absolute max-tablet:after:inset-x-0 max-tablet:after:-inset-y-2 ms-auto -me-1.5 inline-flex min-h-7 shrink-0 items-center gap-1 rounded-md px-1.5 py-0 whitespace-nowrap pointer-coarse:relative pointer-coarse:after:absolute pointer-coarse:after:inset-x-0 pointer-coarse:after:-inset-y-2",
        toggleClassName
      )}
      aria-expanded={open}
    >
      Source
      <ArrowUpRightIcon className="size-3.5" aria-hidden="true" />
    </button>
  );
  const closeOnNavigation = (event: MouseEvent) => {
    // Release the sheet before a route blocker opens its own changes dialog.
    if (event.target instanceof Element && event.target.closest("a[href]")) {
      setOpen(false);
    }
  };
  const content = (
    <>
      <header className="flex items-start justify-between gap-5">
        {phone ? (
          <DialogTitle>
            {observedAt ? "Reading details" : "Source details"}
          </DialogTitle>
        ) : (
          heading
        )}
        <button
          type="button"
          className="hover:bg-gray-3 focus-visible:outline-ring -me-2.5 -mt-2.5 grid size-11 shrink-0 cursor-pointer place-items-center rounded-md focus-visible:outline-2 focus-visible:outline-offset-3"
          aria-label="Close details"
          onClick={() => setOpen(false)}
        >
          <XIcon className="size-[18px]" aria-hidden="true" />
        </button>
      </header>
      {phone ? (
        <DialogDescription className={descriptionClass}>
          {title}
        </DialogDescription>
      ) : (
        description
      )}
      {(observedAt ||
        single?.availability !== undefined ||
        single?.enabled !== undefined) && (
        <dl className="m-0 grid gap-4 tabular-nums">
          {observedAt && (
            <div className="grid gap-[3px]">
              <dt className="text-label text-muted-foreground">
                {observedLabel === "Observed"
                  ? "Observed at"
                  : `${observedLabel} observed at`}
              </dt>
              <dd>
                {Number.isFinite(Date.parse(observedAt)) ? (
                  <time dateTime={observedAt}>
                    {new Date(observedAt).toLocaleString([], {
                      year: "numeric",
                      month: "short",
                      day: "numeric",
                      hour: "numeric",
                      minute: "2-digit",
                      second: "2-digit",
                      timeZoneName: "short",
                    })}
                  </time>
                ) : (
                  "Unavailable"
                )}
              </dd>
            </div>
          )}
          {age && (
            <div className="grid gap-[3px]">
              <dt className="text-label text-muted-foreground">
                Observation age
              </dt>
              <dd>{age}</dd>
            </div>
          )}
          {single && <EntityFacts source={single} />}
        </dl>
      )}
      {sources.length > 0 && (
        <section className="border-gray-4 mt-[22px] border-t pt-[18px]">
          <h3 className="text-label font-medium">
            {sources.length === 1 ? "Source" : "Sources"}
          </h3>
          <ul className="mt-2.5 grid list-none gap-4 p-0">
            {sources.map((source) => (
              <li key={source.entityId}>
                <Link
                  className="text-link focus-visible:outline-ring max-tablet:min-h-11 inline-flex min-h-7 items-center underline underline-offset-[3px] focus-visible:outline-2 focus-visible:outline-offset-3"
                  to={`/entities/${source.entityId}`}
                >
                  {source.label}
                </Link>
                <code className="text-caption text-muted-foreground mt-1 block">
                  {source.entityId}
                </code>
                {!single &&
                  (source.availability !== undefined ||
                    source.enabled !== undefined) && (
                    <dl className="mt-2 grid gap-2 tabular-nums">
                      <EntityFacts source={source} />
                    </dl>
                  )}
              </li>
            ))}
          </ul>
        </section>
      )}
      {children !== undefined && (
        <section className="border-gray-4 text-muted-foreground mt-[22px] grid gap-3 border-t pt-[18px]">
          {children}
        </section>
      )}
    </>
  );
  return phone ? (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent
        placement="bottom"
        data-placement="bottom"
        aria-modal="true"
        onClick={closeOnNavigation}
      >
        {content}
      </DialogContent>
    </Dialog>
  ) : (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent
        className="block"
        align="end"
        sideOffset={8}
        collisionPadding={12}
        aria-labelledby={headingId}
        aria-describedby={descriptionId}
        onClick={closeOnNavigation}
      >
        {content}
      </PopoverContent>
    </Popover>
  );
};
