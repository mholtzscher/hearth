import { useState } from "react";
import type { CSSProperties } from "react";

import { Button } from "../../../../components/button";
import { feedEntries } from "../../bindings/changes";
import type { FeedEntry } from "../../bindings/changes";
import { useMinuteClock } from "../observation";
import type { TimelineRow } from "./lanes";

const Pulses = ({ pulses }: { pulses: NonNullable<FeedEntry["pulses"]> }) => {
  const [firstPulse] = pulses;
  if (!firstPulse) {
    return null;
  }
  const { from: first } = firstPulse;
  const last = Math.max(pulses.at(-1)?.to ?? first, first + 1);
  return (
    <div
      className="bg-gray-2 relative mt-1.5 h-2.5 rounded-xs"
      aria-hidden="true"
    >
      {pulses.map((pulse, index) => (
        <span
          className="absolute inset-y-0 min-w-0.5 rounded-xs bg-(--timeline-color)"
          key={index}
          style={{
            left: `${(100 * (pulse.from - first)) / (last - first)}%`,
            width: `${Math.max(0.6, (100 * (pulse.to - pulse.from)) / (last - first))}%`,
          }}
        />
      ))}
    </div>
  );
};

export const TimelineFeed = ({
  rows,
  windowWords,
}: {
  rows: TimelineRow[];
  windowWords: string;
}) => {
  const [shown, setShown] = useState(12);
  const now = useMinuteClock();
  const entries = feedEntries(
    rows.flatMap((row, index) =>
      row.resource.data?.timeline
        ? [{ label: row.label, data: row.resource.data.timeline, index }]
        : []
    ),
    now
  );
  // Preserve series colors when preceding rows have no history.
  const indices = rows.flatMap((row, index) =>
    row.resource.data?.timeline ? [index] : []
  );
  const visible = entries.slice(0, shown);
  const groups: { key: string; label: string; entries: typeof visible }[] = [];
  for (const entry of visible) {
    let group = groups.at(-1);
    if (!group || group.key !== entry.groupKey) {
      group = { key: entry.groupKey, label: entry.group, entries: [] };
      groups.push(group);
    }
    group.entries.push(entry);
  }
  const hasHistory = rows.some((row) => row.resource.data?.timeline);
  return (
    <div className="grid gap-1">
      {visible.length === 0 ? (
        <p className="text-label text-muted-foreground py-6">
          {hasHistory
            ? `No changes in the last ${windowWords}`
            : "Historical readings will appear here."}
        </p>
      ) : (
        groups.map((group, groupIndex) => (
          <section key={group.key}>
            <h5
              className={`text-label text-muted-foreground mb-1 font-medium ${groupIndex === 0 ? "mt-0" : "mt-3.5"}`}
            >
              {group.label}
            </h5>
            <ol className="list-none">
              {group.entries.map((entry) => (
                <li
                  key={`${entry.row}:${entry.at}`}
                  data-slot="timeline-feed-entry"
                  className="grid min-h-[30px] grid-cols-[4.6rem_14px_minmax(0,1fr)] gap-x-2.5 gap-y-0"
                  style={
                    {
                      "--timeline-color": `var(--chart-${((indices[entry.row] ?? 0) % 8) + 1})`,
                    } as CSSProperties
                  }
                >
                  <time
                    className="text-muted-foreground text-right whitespace-nowrap"
                    dateTime={new Date(entry.at).toISOString()}
                  >
                    {new Date(entry.at).toLocaleTimeString([], {
                      hour: "numeric",
                      minute: "2-digit",
                    })}
                  </time>
                  <span
                    className="before:bg-gray-4 relative before:absolute before:inset-y-0 before:left-1.5 before:w-px before:content-[''] after:absolute after:top-1.5 after:left-0.5 after:size-[9px] after:rounded-full after:bg-(--timeline-color) after:shadow-[0_0_0_3px_var(--gray-1)] after:content-['']"
                    aria-hidden="true"
                  />
                  <div className="min-w-0 pb-2.5">
                    <b className="font-medium">{entry.label}</b>{" "}
                    <span className="text-muted-foreground">{entry.text}</span>
                    {entry.pulses && <Pulses pulses={entry.pulses} />}
                  </div>
                </li>
              ))}
            </ol>
          </section>
        ))
      )}
      {entries.length > shown && (
        <Button
          variant="outline"
          size="touch"
          className="ms-[calc(4.6rem+34px)] mt-1.5 justify-self-start @max-[420px]:ms-0"
          type="button"
          onClick={() => setShown((n) => n + 12)}
        >
          Show {entries.length - shown} earlier changes
        </Button>
      )}
    </div>
  );
};
