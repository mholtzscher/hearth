import { useState } from "react";
import type { CSSProperties, ReactNode } from "react";

export interface SegmentedControlProps<T extends string> {
  /** The accessible name of the group. */
  label: string;
  value: T;
  onValueChange: (value: T) => void;
  options: { value: T; label: ReactNode; disabled?: boolean }[];
  /** The minimum height of each segment: 32px by default or 44px for touch. */
  size?: "default" | "touch";
  disabled?: boolean;
}

/** Segments sit inside the control frame, so they take the smaller detail radius. */
const segmentClasses =
  "min-w-0 rounded-xs px-2 text-label text-gray-9 [overflow-wrap:anywhere] transition-colors duration-feedback " +
  "hover:not-disabled:aria-[pressed=false]:bg-gray-3 hover:not-disabled:aria-[pressed=false]:text-foreground " +
  "aria-[pressed=true]:text-violet-text aria-[pressed=true]:duration-state";

/**
 * One selection surface shared by every segment. The columns have equal widths
 * and 2px gaps, so the surface is one column wide and moves a column and a gap
 * per step (Shift, DESIGN.md Motion).
 */
const selectionClasses =
  "pointer-events-none absolute inset-y-0.5 start-0.5 -z-10 rounded-xs bg-violet-soft shadow-[inset_0_0_0_1px_var(--violet)] " +
  "w-[calc((100%-4px-(var(--segment-count)-1)*2px)/var(--segment-count))] translate-x-[calc(var(--segment-index)*(100%+2px))] " +
  "transition-[translate,opacity] duration-layout data-[state=disabled]:opacity-50 data-[state=empty]:opacity-0 data-[state=empty]:duration-feedback " +
  "data-appearing:transition-opacity data-appearing:duration-state motion-reduce:transition-opacity motion-reduce:duration-state";

/**
 * Tracks where the selection surface rests and whether it is appearing rather
 * than moving. A cleared value keeps the last position, so the surface fades
 * out where it was.
 */
const useSelection = (index: number) => {
  const [selection, setSelection] = useState({
    index,
    resting: Math.max(index, 0),
    appearing: false,
  });
  if (selection.index !== index) {
    const next = {
      index,
      resting: index < 0 ? selection.resting : index,
      appearing: selection.index < 0,
    };
    setSelection(next);
    return next;
  }
  return selection;
};

const selectionState = (selected: boolean, disabled: boolean) => {
  if (!selected) {
    return "empty";
  }
  return disabled ? "disabled" : "selected";
};

export const SegmentedControl = <T extends string>({
  label,
  value,
  onValueChange,
  options,
  size = "default",
  disabled,
}: SegmentedControlProps<T>) => {
  const index = options.findIndex((option) => option.value === value);
  const { resting, appearing } = useSelection(index);
  return (
    <fieldset
      data-slot="segmented-control"
      aria-label={label}
      className="border-gray-4 bg-card relative isolate grid min-w-0 auto-cols-fr grid-flow-col gap-0.5 rounded-md border p-0.5"
      style={
        {
          "--segment-count": options.length,
          "--segment-index": resting,
        } as CSSProperties
      }
    >
      <span
        aria-hidden="true"
        data-slot="segment-selection"
        data-reduced-motion="fade"
        data-state={selectionState(
          index !== -1,
          Boolean(disabled || options[index]?.disabled)
        )}
        data-appearing={appearing || undefined}
        className={selectionClasses}
      />
      {options.map((option) => (
        <button
          key={option.value}
          data-slot="segment"
          data-reduced-motion="fade"
          type="button"
          aria-pressed={value === option.value}
          disabled={disabled || option.disabled}
          onClick={() => onValueChange(option.value)}
          className={`${segmentClasses} ${size === "touch" ? "min-h-11" : "min-h-8"}`}
        >
          {option.label}
        </button>
      ))}
    </fieldset>
  );
};
