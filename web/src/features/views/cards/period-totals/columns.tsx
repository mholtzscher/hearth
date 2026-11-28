import type { PeriodTotal } from "../../bindings/types";
import { barClass, periodName } from "./parts";
import { barDuration, compactDuration } from "./periods";
import type { PeriodSummary } from "./periods";
import type { PeriodTotalsPeriod } from "./schema";

const Axis = ({ children }: { children: string }) => (
  <span className="text-caption text-muted-foreground bg-card absolute top-0 right-0 w-8 -translate-y-1/2 text-right leading-tight">
    {children}
  </span>
);

/** Days: seven bars, each labelled with its total, so the plot needs no value axis. */
const DayColumns = ({
  periods,
  summary,
}: {
  periods: PeriodTotal[];
  summary: PeriodSummary;
}) => {
  const { average, peak } = summary;
  return (
    <>
      <div className="relative pe-10">
        <div className="border-gray-4 grid h-44 grid-cols-7 items-end gap-x-2 border-b">
          {periods.map((period) => (
            <div
              className="flex h-full flex-col items-center justify-end gap-1"
              key={period.start}
            >
              {period.seconds === null ? (
                <span className="text-caption text-gray-6 pb-1">—</span>
              ) : (
                <>
                  <span className="text-caption text-foreground bg-card relative z-10 px-0.5 leading-tight whitespace-nowrap">
                    {barDuration(period.seconds)}
                  </span>
                  <span
                    className={`${barClass} w-full max-w-8 rounded-t-xs`}
                    data-partial={period.partial || undefined}
                    style={{ height: `${(period.seconds / peak) * 84}%` }}
                  />
                </>
              )}
            </div>
          ))}
        </div>
        {average !== null && (
          // The average's label sits in a gutter, so it never covers a bar's own label.
          <span
            className="border-gray-6 absolute inset-x-0 border-t"
            style={{ bottom: `${(average / peak) * 84}%` }}
          >
            <Axis>{`avg ${compactDuration(average)}`}</Axis>
          </span>
        )}
      </div>
      <div className="text-caption text-muted-foreground mt-2 grid grid-cols-7 gap-x-2 pe-10 text-center">
        {periods.map((period) => (
          <span key={period.start}>
            {periodName(period, "day")}
            {period.partial && <span className="block">so far</span>}
            {period.seconds === null && (
              <span className="block">No history</span>
            )}
          </span>
        ))}
      </div>
    </>
  );
};

/**
 * Hours: 24 bars are too narrow to label, so the value axis names the longest
 * period at the top, and the hour axis labels every sixth bar.
 */
const HourColumns = ({
  periods,
  summary,
}: {
  periods: PeriodTotal[];
  summary: PeriodSummary;
}) => (
  <>
    <div className="relative pe-10">
      <div className="border-gray-4 grid h-36 grid-cols-[repeat(24,minmax(0,1fr))] items-end gap-x-px border-b @min-[421px]:gap-x-0.5">
        {periods.map((period) => (
          <div className="flex h-full flex-col justify-end" key={period.start}>
            {period.seconds === null ? (
              <span className="border-gray-5 h-1.5 border-t border-dashed" />
            ) : (
              <span
                className={`${barClass} w-full rounded-t-xs`}
                data-partial={period.partial || undefined}
                style={{
                  height: `${Math.max(2, (period.seconds / summary.peak) * 100)}%`,
                  minHeight: period.seconds > 0 ? 2 : 0,
                }}
              />
            )}
          </div>
        ))}
      </div>
      {summary.longest && (
        <span className="border-gray-5 absolute inset-x-0 top-0 border-t border-dashed">
          <Axis>{compactDuration(summary.peak)}</Axis>
        </span>
      )}
    </div>
    <div className="text-caption text-muted-foreground mt-1.5 grid grid-cols-[repeat(24,minmax(0,1fr))] pe-10">
      {periods.map((period) => (
        <span className="relative h-4" key={period.start}>
          {new Date(period.start).getHours() % 6 === 0 && (
            <span className="absolute start-0 whitespace-nowrap">
              {periodName(period, "hour")}
            </span>
          )}
        </span>
      ))}
    </div>
    <p className="text-caption text-muted-foreground mt-2 flex items-center gap-2">
      <span
        className={`${barClass} inline-block h-3 w-2 rounded-t-xs`}
        data-partial
        aria-hidden="true"
      />
      The latest hour is still being counted.
    </p>
  </>
);

export const Columns = ({
  kind,
  periods,
  summary,
}: {
  kind: PeriodTotalsPeriod;
  periods: PeriodTotal[];
  summary: PeriodSummary;
}) =>
  kind === "day" ? (
    <DayColumns periods={periods} summary={summary} />
  ) : (
    <HourColumns periods={periods} summary={summary} />
  );
