import type { ReactNode } from "react";

export type SegmentedControlProps<T extends string> = {
  /** The accessible name of the group. */
  label: string;
  value: T;
  onValueChange: (value: T) => void;
  options: Array<{ value: T; label: ReactNode; disabled?: boolean }>;
  /** The minimum height of each segment: 32px by default or 44px for touch. */
  size?: "default" | "touch";
  disabled?: boolean;
};

const segmentClasses = "min-w-0 rounded-md border-0 bg-transparent px-2 text-label leading-normal text-gray-9 [overflow-wrap:anywhere] "
  + "hover:not-disabled:aria-[pressed=false]:bg-gray-3 hover:not-disabled:aria-[pressed=false]:text-foreground "
  + "focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-ring focus-visible:outline-offset-3 disabled:opacity-50 "
  + "aria-[pressed=true]:bg-violet-soft aria-[pressed=true]:text-violet-text aria-[pressed=true]:shadow-[inset_0_0_0_1px_var(--violet)]";

export function SegmentedControl<T extends string>({
  label, value, onValueChange, options, size = "default", disabled,
}: SegmentedControlProps<T>) {
  return <div data-slot="segmented-control" role="group" aria-label={label}
    className="grid auto-cols-fr grid-flow-col gap-0.5 rounded-md border border-gray-4 bg-card p-0.5">
    {options.map(option => <button key={option.value} data-slot="segment" type="button"
      aria-pressed={value === option.value} disabled={disabled || option.disabled}
      onClick={() => onValueChange(option.value)}
      className={`${segmentClasses} ${size === "touch" ? "min-h-11" : "min-h-8"}`}>
      {option.label}
    </button>)}
  </div>;
}
