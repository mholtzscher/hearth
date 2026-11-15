import type { Resolved, SeriesData, SeriesWindow } from "../../bindings/types";
import { tooMuchHistory } from "../../bindings/history";
import { cn } from "../../../../lib/utils";

const windowWords: Record<SeriesWindow, string> = { "1h": "1 h", "6h": "6 h", "24h": "24 h", "7d": "7 d" };
const bucketWords: Record<SeriesWindow, string> = { "1h": "1-minute", "6h": "1-minute", "24h": "5-minute", "7d": "30-minute" };
const WIDTH = 288, HEIGHT = 44, INSET = 3;

/** The trend's method, for the card's Source disclosure. */
export function trendMethod(window: SeriesWindow) {
  return <p>The {windowWords[window]} line averages the samples in each {bucketWords[window]} interval; the shaded band spans their minimum and maximum. Gaps mean no samples.</p>;
}

/**
 * A small history line under a reading: the mean and its min–max band, with
 * gaps preserved. The range sentence is its text equivalent; the drawing is
 * hidden from assistive technology.
 */
export function Trend({ window, resource, dimmed, className, bandClassName, classNames }: { window: SeriesWindow; resource: Resolved<SeriesData>; dimmed: boolean; className?: string; bandClassName?: string; classNames?: Partial<Record<"plot" | "drawing" | "mean", string>> }) {
  const data = resource.data;
  const rootClass = cn("mt-3 grid gap-1", className);
  const plotClass = cn("relative h-11", classNames?.plot);
  const textClass = "text-label leading-normal text-muted-foreground tabular-nums";
  if (!data) {
    const words = resource.status === "loading" ? "Loading history…" : resource.status === "error" ? (resource.error === tooMuchHistory ? tooMuchHistory : "Couldn't load history.") : `No history in the last ${windowWords[window]}.`;
    return <div data-slot="trend" className={rootClass} data-value={dimmed ? "dimmed" : "live"}><div data-slot="trend-plot" className={plotClass} /><p className={textClass}>{words}</p></div>;
  }
  const sampled = data.buckets.filter(bucket => bucket.mean !== null);
  if (sampled.length === 0) {
    return <div data-slot="trend" className={rootClass} data-value={dimmed ? "dimmed" : "live"}><div data-slot="trend-plot" className={plotClass} /><p className={textClass}>No history in the last {windowWords[window]}.</p>{resource.refreshFailed && <p className={textClass}>Couldn't refresh history. Showing the last interval.</p>}</div>;
  }
  const lows = sampled.map(bucket => bucket.min ?? bucket.mean!), highs = sampled.map(bucket => bucket.max ?? bucket.mean!);
  const low = Math.min(...lows), high = Math.max(...highs);
  const x = (index: number) => data.buckets.length > 1 ? index / (data.buckets.length - 1) * WIDTH : WIDTH;
  const y = (value: number) => HEIGHT - INSET - (high > low ? (value - low) / (high - low) : 0.5) * (HEIGHT - 2 * INSET);
  // Each run of consecutive samples is one segment, so a gap stays a gap.
  const runs: number[][] = [];
  data.buckets.forEach((bucket, index) => {
    if (bucket.mean === null) return;
    const last = runs[runs.length - 1];
    if (last && last[last.length - 1] === index - 1) last.push(index); else runs.push([index]);
  });
  const point = (index: number, value: number) => `${x(index).toFixed(2)},${y(value).toFixed(2)}`;
  const mean = runs.map(run => run.length === 1
    ? `M${point(run[0], data.buckets[run[0]].mean!)}h0.01`
    : `M${run.map(index => point(index, data.buckets[index].mean!)).join("L")}`).join("");
  const band = runs.filter(run => run.length > 1).map(run => {
    const top = run.map(index => point(index, data.buckets[index].max ?? data.buckets[index].mean!));
    const bottom = [...run].reverse().map(index => point(index, data.buckets[index].min ?? data.buckets[index].mean!));
    return `M${top.join("L")}L${bottom.join("L")}Z`;
  }).join("");
  const digits = data.format.fraction_digits;
  const unit = data.format.unit_label ? ` ${data.format.unit_label}` : "";
  return <div data-slot="trend" className={rootClass} data-value={dimmed ? "dimmed" : "live"}>
    <div data-slot="trend-plot" className={plotClass} aria-hidden="true">
      <svg data-slot="trend-drawing" className={cn("block h-11 w-full overflow-visible", classNames?.drawing)} viewBox={`0 0 ${WIDTH} ${HEIGHT}`} preserveAspectRatio="none" focusable="false">
        {!dimmed && <path data-slot="trend-band" className={cn("fill-gray-10 [fill-opacity:.08]", bandClassName)} d={band} />}
        <path data-slot="trend-mean" className={cn("fill-none [stroke-width:1.5] [stroke-linejoin:round] [stroke-linecap:round] [vector-effect:non-scaling-stroke]", dimmed ? "stroke-gray-7 [stroke-dasharray:4_3] opacity-60" : "stroke-gray-9", classNames?.mean)} d={mean} />
      </svg>
    </div>
    <p className={textClass}>{windowWords[window]} range {low.toFixed(digits)}–{high.toFixed(digits)}{unit}</p>
    {resource.refreshFailed && <p className={textClass}>Couldn't refresh history. Showing the last interval.</p>}
  </div>;
}
