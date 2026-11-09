import type { Resolved, SeriesData, SeriesWindow } from "../../bindings/types";
import { tooMuchHistory } from "../../bindings/history";

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
export function Trend({ window, resource, dimmed }: { window: SeriesWindow; resource: Resolved<SeriesData>; dimmed: boolean }) {
  const data = resource.data;
  if (!data) {
    const words = resource.status === "loading" ? "Loading history…" : resource.status === "error" ? (resource.error === tooMuchHistory ? tooMuchHistory : "Couldn't load history.") : `No history in the last ${windowWords[window]}.`;
    return <div className="view-trend" data-value={dimmed ? "dimmed" : "live"}><div className="view-trend-plot" /><p>{words}</p></div>;
  }
  const sampled = data.buckets.filter(bucket => bucket.mean !== null);
  if (sampled.length === 0) {
    return <div className="view-trend" data-value={dimmed ? "dimmed" : "live"}><div className="view-trend-plot" /><p>No history in the last {windowWords[window]}.</p>{resource.refreshFailed && <p>Couldn't refresh history. Showing the last interval.</p>}</div>;
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
  return <div className="view-trend" data-value={dimmed ? "dimmed" : "live"}>
    <div className="view-trend-plot" aria-hidden="true">
      <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} preserveAspectRatio="none" focusable="false">
        {!dimmed && <path className="view-trend-band" d={band} />}
        <path className="view-trend-mean" d={mean} />
      </svg>
    </div>
    <p>{windowWords[window]} range {low.toFixed(digits)}–{high.toFixed(digits)}{unit}</p>
    {resource.refreshFailed && <p>Couldn't refresh history. Showing the last interval.</p>}
  </div>;
}
