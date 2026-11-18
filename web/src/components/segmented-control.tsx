import type { ReactNode } from "react";

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

/** Segments sit 3px inside the frame (1px border, 2px padding), so their corners shrink by 3px to stay concentric. */
const segmentClasses =
  "min-w-0 rounded-[calc(var(--radius-md)-3px)] border-0 bg-transparent px-2 text-label leading-normal text-gray-9 [overflow-wrap:anywhere] " +
  "hover:not-disabled:aria-[pressed=false]:bg-gray-3 hover:not-disabled:aria-[pressed=false]:text-foreground " +
  "disabled:opacity-50 " +
  "aria-[pressed=true]:bg-violet-soft aria-[pressed=true]:text-violet-text aria-[pressed=true]:shadow-[inset_0_0_0_1px_var(--violet)]";

export const SegmentedControl = <T extends string>({
  label,
  value,
  onValueChange,
  options,
  size = "default",
  disabled,
}: SegmentedControlProps<T>) => (
  <fieldset
    data-slot="segmented-control"
    aria-label={label}
    className="border-gray-4 bg-card grid min-w-0 auto-cols-fr grid-flow-col gap-0.5 rounded-md border p-0.5"
  >
    {options.map((option) => (
      <button
        key={option.value}
        data-slot="segment"
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
