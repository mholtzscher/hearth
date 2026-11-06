import type { MetricData } from "../../bindings/types";

/** Bounds read best without trailing zeros: 5–30 °C, not 5.0–30.0 °C. */
function bound(value: number, digits: number): string {
  return String(Number(value.toFixed(digits)));
}

/**
 * A reading against its field's declared range: a thin neutral track with the
 * bounds at its ends. A value outside the range pins the marker to the end and
 * says so in words.
 */
export function RangeTrack({ data, dimmed }: { data: MetricData & { range: NonNullable<MetricData["range"]> }; dimmed: boolean }) {
  const { minimum, maximum } = data.range;
  const digits = data.format.fraction_digits;
  const unit = data.format.unit_label ? ` ${data.format.unit_label}` : "";
  const share = Math.min(1, Math.max(0, (data.value - minimum) / (maximum - minimum)));
  const outside = data.value > maximum ? `Above the ${bound(maximum, digits)}${unit} range`
    : data.value < minimum ? `Below the ${bound(minimum, digits)}${unit} range` : undefined;
  return <div className="view-range" data-value={dimmed ? "dimmed" : "live"}>
    <div className="view-range-track" aria-hidden="true">
      <span className="view-range-fill" style={{ width: `${share * 100}%` }} />
      <span className="view-range-marker" style={{ left: `${share * 100}%` }} />
    </div>
    <p className="view-range-ends" aria-hidden="true"><span>{bound(minimum, digits)}</span><span>{bound(maximum, digits)}{unit}</span></p>
    <p className="sr-only">Range {bound(minimum, digits)} to {bound(maximum, digits)}{unit}</p>
    {outside && <p className="view-range-note">{outside}</p>}
  </div>;
}
