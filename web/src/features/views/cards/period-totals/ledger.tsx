import type { PeriodTotal } from "../../bindings/types";
import { barClass, periodName } from "./parts";
import { compactDuration } from "./periods";
import type { PeriodSummary } from "./periods";
import type { PeriodTotalsPeriod } from "./schema";

/** One row per period, read like a table, with the average as a rule down the rows. */
export const Ledger = ({
  kind,
  periods,
  summary,
}: {
  kind: PeriodTotalsPeriod;
  periods: PeriodTotal[];
  summary: PeriodSummary;
}) => {
  const { average, peak } = summary;
  const rowHeight = kind === "day" ? "h-7" : "h-5";
  return (
    <>
      <span />
      <span className="text-caption text-muted-foreground relative h-5">
        {average !== null && (
          <span
            className="absolute -translate-x-1/2 whitespace-nowrap"
            style={{ left: `${(average / peak) * 100}%` }}
          >
            avg {compactDuration(average)}
          </span>
        )}
      </span>
      <span />
      {periods.map((period) => (
        <div className="contents" key={period.start}>
          <span className="text-muted-foreground">
            {periodName(period, kind)}
          </span>
          <span className={`relative flex items-center ${rowHeight}`}>
            {average !== null && (
              <span
                className="bg-gray-5 absolute inset-y-0 w-px"
                style={{ left: `${(average / peak) * 100}%` }}
              />
            )}
            {period.seconds === null ? (
              <span className="text-muted-foreground bg-card relative pe-2">
                No history
              </span>
            ) : (
              <span
                className={`${barClass} relative ${kind === "day" ? "h-2.5" : "h-2"} rounded-xs`}
                data-partial={period.partial || undefined}
                style={{
                  width: `${(period.seconds / peak) * 100}%`,
                  minWidth: period.seconds > 0 ? 2 : 0,
                }}
              />
            )}
          </span>
          <span className="text-right whitespace-nowrap">
            {period.partial && (
              <span className="text-muted-foreground">so far </span>
            )}
            {period.seconds === null ? (
              <span className="text-gray-6">—</span>
            ) : (
              compactDuration(period.seconds)
            )}
          </span>
        </div>
      ))}
    </>
  );
};
