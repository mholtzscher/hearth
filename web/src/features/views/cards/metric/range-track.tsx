import type { MetricData } from "../../bindings/types";

/** Bounds read best without trailing zeros: 5–30 °C, not 5.0–30.0 °C. */
const bound = (value: number, digits: number): string =>
  String(Number(value.toFixed(digits)));

/**
 * A reading against its field's declared range: a thin neutral track with the
 * bounds at its ends. A value outside the range pins the marker to the end and
 * says so in words.
 */
export const RangeTrack = ({
  data,
  dimmed,
}: {
  data: MetricData & { range: NonNullable<MetricData["range"]> };
  dimmed: boolean;
}) => {
  const { minimum, maximum } = data.range;
  const digits = data.format.fraction_digits;
  const unit = data.format.unit_label ? ` ${data.format.unit_label}` : "";
  const share = Math.min(
    1,
    Math.max(0, (data.value - minimum) / (maximum - minimum))
  );
  let outside: string | undefined;
  if (data.value > maximum) {
    outside = `Above the ${bound(maximum, digits)}${unit} range`;
  } else if (data.value < minimum) {
    outside = `Below the ${bound(minimum, digits)}${unit} range`;
  }
  return (
    <div
      className="group/range mt-3.5 grid gap-1.5"
      data-value={dimmed ? "dimmed" : "live"}
    >
      <div className="bg-gray-3 relative h-1 rounded-xs" aria-hidden="true">
        <span
          className="bg-gray-7 group-data-[value=dimmed]/range:bg-gray-5 absolute inset-y-0 left-0 rounded-xs"
          style={{ width: `${share * 100}%` }}
        />
        <span
          data-slot="range-marker"
          className="bg-gray-10 group-data-[value=dimmed]/range:bg-gray-8 absolute top-1/2 -ml-px h-3.5 w-0.5 -translate-y-1/2 rounded-xs"
          style={{ left: `${share * 100}%` }}
        />
      </div>
      <p
        className="text-label text-muted-foreground flex justify-between gap-2 leading-normal tabular-nums"
        aria-hidden="true"
      >
        <span>{bound(minimum, digits)}</span>
        <span>
          {bound(maximum, digits)}
          {unit}
        </span>
      </p>
      <p className="sr-only">
        Range {bound(minimum, digits)} to {bound(maximum, digits)}
        {unit}
      </p>
      {outside && (
        <p className="text-label text-gray-9 leading-normal">{outside}</p>
      )}
    </div>
  );
};
