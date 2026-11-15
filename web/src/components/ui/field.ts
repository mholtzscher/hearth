/** Shared text-field treatment. Inputs, textareas, and native selects own their native behavior. */
export const fieldStyles =
  "w-full min-w-0 rounded-md border border-input bg-card px-2 text-label leading-normal font-normal text-foreground outline-none transition-colors duration-150 ease-out-expo motion-reduce:transition-none placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-ring focus-visible:outline-offset-3 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive";

export const fieldSizeStyles = {
  default: "h-8 py-1",
  touch: "h-11 py-2",
  auto: "h-8 py-1 pointer-coarse:h-11 pointer-coarse:py-2",
};

export type ControlSize = keyof typeof fieldSizeStyles;
