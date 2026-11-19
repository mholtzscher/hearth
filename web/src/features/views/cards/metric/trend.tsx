import { cn } from "../../../../lib/utils";
import { tooMuchHistory } from "../../bindings/history";
import type { Resolved, SeriesData, SeriesWindow } from "../../bindings/types";

const windowWords: Record<SeriesWindow, string> = {
  "1h": "1 h",
  "6h": "6 h",
  "24h": "24 h",
  "7d": "7 d",
};
const bucketWords: Record<SeriesWindow, string> = {
  "1h": "1-minute",
  "6h": "1-minute",
  "24h": "5-minute",
  "7d": "30-minute",
};
const HEIGHT = 44;
const INSET = 3;
const WIDTH = 288;

interface TrendPoint {
  index: number;
  mean: number;
  min: number | null;
  max: number | null;
}

const historyWords = (
  resource: Resolved<SeriesData>,
  window: SeriesWindow
): string => {
  if (resource.status === "loading") {
    return "Loading history…";
  }
  if (resource.status === "error") {
    return resource.error === tooMuchHistory
      ? tooMuchHistory
      : "Couldn't load history.";
  }
  return `No history in the last ${windowWords[window]}.`;
};

const runsFor = (sampled: TrendPoint[]): TrendPoint[][] => {
  const runs: TrendPoint[][] = [];
  for (const bucket of sampled) {
    const last = runs.at(-1);
    const previous = last?.at(-1);
    if (last && previous && previous.index === bucket.index - 1) {
      last.push(bucket);
    } else {
      runs.push([bucket]);
    }
  }
  return runs;
};

const xFor = (index: number, count: number): number =>
  count > 1 ? (index / (count - 1)) * WIDTH : WIDTH;

const yFor = (value: number, low: number, high: number): number =>
  HEIGHT -
  INSET -
  (high > low ? (value - low) / (high - low) : 0.5) * (HEIGHT - 2 * INSET);

const pointAt = (
  x: (index: number) => number,
  y: (value: number) => number,
  index: number,
  value: number
): string => `${x(index).toFixed(2)},${y(value).toFixed(2)}`;

const meanPath = (
  runs: TrendPoint[][],
  x: (index: number) => number,
  y: (value: number) => number
): string =>
  runs
    .map((run) => {
      const [first] = run;
      return run.length === 1
        ? `M${pointAt(x, y, first.index, first.mean)}h0.01`
        : `M${run
            .map((bucket) => pointAt(x, y, bucket.index, bucket.mean))
            .join("L")}`;
    })
    .join("");

const bandPath = (
  runs: TrendPoint[][],
  x: (index: number) => number,
  y: (value: number) => number
): string =>
  runs
    .filter((run) => run.length > 1)
    .map((run) => {
      const top = run.map((bucket) =>
        pointAt(x, y, bucket.index, bucket.max ?? bucket.mean)
      );
      const bottom = run
        .toReversed()
        .map((bucket) =>
          pointAt(x, y, bucket.index, bucket.min ?? bucket.mean)
        );
      return `M${top.join("L")}L${bottom.join("L")}Z`;
    })
    .join("");

/** The trend's method, for the card's Source disclosure. */
export const trendMethod = (window: SeriesWindow) => (
  <p>
    The {windowWords[window]} line averages the samples in each{" "}
    {bucketWords[window]} interval; the shaded band spans their minimum and
    maximum. Gaps mean no samples.
  </p>
);

/**
 * A small history line under a reading: the mean and its min–max band, with
 * gaps preserved. The range sentence is its text equivalent; the drawing is
 * hidden from assistive technology.
 */
export const Trend = ({
  window,
  resource,
  dimmed,
  className,
  bandClassName,
  classNames,
}: {
  window: SeriesWindow;
  resource: Resolved<SeriesData>;
  dimmed: boolean;
  className?: string;
  bandClassName?: string;
  classNames?: Partial<Record<"plot" | "drawing" | "mean", string>>;
}) => {
  const { data } = resource;
  const rootClass = cn("mt-3 grid gap-1", className);
  const plotClass = cn("relative h-11", classNames?.plot);
  const textClass = "text-label text-muted-foreground";
  if (!data) {
    const words = historyWords(resource, window);
    return (
      <div
        data-slot="trend"
        className={rootClass}
        data-value={dimmed ? "dimmed" : "live"}
      >
        <div data-slot="trend-plot" className={plotClass} />
        <p className={textClass}>{words}</p>
      </div>
    );
  }
  const sampled: TrendPoint[] = data.buckets.flatMap((bucket, index) =>
    bucket.mean === null
      ? []
      : [
          {
            index,
            mean: bucket.mean,
            min: bucket.min,
            max: bucket.max,
          },
        ]
  );
  if (sampled.length === 0) {
    return (
      <div
        data-slot="trend"
        className={rootClass}
        data-value={dimmed ? "dimmed" : "live"}
      >
        <div data-slot="trend-plot" className={plotClass} />
        <p className={textClass}>
          No history in the last {windowWords[window]}.
        </p>
        {resource.refreshFailed && (
          <p className={textClass}>
            Couldn&apos;t refresh history. Showing the last interval.
          </p>
        )}
      </div>
    );
  }
  const lows = sampled.map((bucket) => bucket.min ?? bucket.mean);
  const highs = sampled.map((bucket) => bucket.max ?? bucket.mean);
  const low = Math.min(...lows);
  const high = Math.max(...highs);
  const x = (index: number) => xFor(index, data.buckets.length);
  const y = (value: number) => yFor(value, low, high);
  // Each run of consecutive samples is one segment, so a gap stays a gap.
  const runs = runsFor(sampled);
  const mean = meanPath(runs, x, y);
  const band = bandPath(runs, x, y);
  const digits = data.format.fraction_digits;
  const unit = data.format.unit_label ? ` ${data.format.unit_label}` : "";
  return (
    <div
      data-slot="trend"
      className={rootClass}
      data-value={dimmed ? "dimmed" : "live"}
    >
      <div data-slot="trend-plot" className={plotClass} aria-hidden="true">
        <svg
          data-slot="trend-drawing"
          className={cn(
            "block h-11 w-full overflow-visible",
            classNames?.drawing
          )}
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          preserveAspectRatio="none"
          focusable="false"
        >
          {!dimmed && (
            <path
              data-slot="trend-band"
              className={cn("fill-gray-10 [fill-opacity:.08]", bandClassName)}
              d={band}
            />
          )}
          <path
            data-slot="trend-mean"
            className={cn(
              "fill-none [stroke-width:1.5] [stroke-linecap:round] [stroke-linejoin:round] [vector-effect:non-scaling-stroke]",
              dimmed
                ? "stroke-gray-7 opacity-60 [stroke-dasharray:4_3]"
                : "stroke-gray-9",
              classNames?.mean
            )}
            d={mean}
          />
        </svg>
      </div>
      <p className={textClass}>
        {windowWords[window]} range {low.toFixed(digits)}–{high.toFixed(digits)}
        {unit}
      </p>
      {resource.refreshFailed && (
        <p className={textClass}>
          Couldn&apos;t refresh history. Showing the last interval.
        </p>
      )}
    </div>
  );
};
