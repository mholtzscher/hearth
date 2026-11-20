import { StatusList } from "../data-card";
import { cardStatus } from "../status";
import { Axis, rowSummary, sinceText, totalsText, Track } from "./lanes";
import type { TimelineRow } from "./lanes";

export const TimelineLedger = ({
  rows,
  windowWords,
}: {
  rows: TimelineRow[];
  windowWords: string;
}) => {
  const first = rows.find((row) => row.resource.data?.timeline)?.resource.data
    ?.timeline;
  return (
    <section aria-label="State timeline">
      <ul className="sr-only">
        {rows.map((row) => (
          <li key={row.key}>{rowSummary(row, windowWords)}</li>
        ))}
      </ul>
      <ol className="grid list-none">
        {rows.map((row) => {
          const status = cardStatus(row.resource);
          return (
            <li
              className="border-border grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-0.5 border-b py-3 first:pt-0 last:border-b-0 last:pb-1"
              key={row.key}
            >
              <span className="text-muted-foreground">{row.label}</span>
              <span className="text-muted-foreground text-right">
                {totalsText(row.resource.data?.timeline, row.trueWord)}
              </span>
              <div
                data-slot="timeline-current"
                className="col-span-full flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5"
              >
                <strong
                  className="text-compact-reading data-[value=empty]:text-gray-6 data-[value=dimmed]:text-gray-8"
                  data-value={status.value}
                >
                  {row.resource.data?.current?.text ?? "—"}
                </strong>
                <span className="text-muted-foreground">{sinceText(row)}</span>
                <StatusList entries={status.entries} />
              </div>
              <Track
                data={row.resource.data?.timeline}
                color="var(--gray-9)"
                dimmed={status.value === "dimmed"}
                compact
              />
            </li>
          );
        })}
      </ol>
      <Axis data={first} className="mt-1.5" />
    </section>
  );
};
