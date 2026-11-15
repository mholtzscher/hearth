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
  return <div className="group/range mt-3.5 grid gap-1.5" data-value={dimmed ? "dimmed" : "live"}>
    <div className="relative h-1 rounded-[2px] bg-gray-3" aria-hidden="true">
      <span className="absolute inset-y-0 left-0 rounded-[2px] bg-gray-7 group-data-[value=dimmed]/range:bg-gray-5" style={{ width: `${share * 100}%` }} />
      <span data-slot="range-marker" className="absolute top-1/2 -ml-px h-3.5 w-0.5 -translate-y-1/2 rounded-[1px] bg-gray-10 group-data-[value=dimmed]/range:bg-gray-8" style={{ left: `${share * 100}%` }} />
    </div>
    <p className="flex justify-between gap-2 text-label leading-normal text-muted-foreground tabular-nums" aria-hidden="true"><span>{bound(minimum, digits)}</span><span>{bound(maximum, digits)}{unit}</span></p>
    <p className="sr-only">Range {bound(minimum, digits)} to {bound(maximum, digits)}{unit}</p>
    {outside && <p className="text-label leading-normal text-gray-9">{outside}</p>}
  </div>;
}
