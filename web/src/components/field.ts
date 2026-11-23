/** Shared text-field treatment. Inputs, textareas, and native selects own their native behavior. */
export const fieldStyles =
  "w-full min-w-0 rounded-md border border-input bg-card px-2 text-label font-normal text-foreground transition-colors placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive";

export const fieldSizeStyles = {
  default: "h-8 py-1",
  touch: "h-11 py-2",
  auto: "h-8 py-1 pointer-coarse:h-11 pointer-coarse:py-2",
};

export type ControlSize = keyof typeof fieldSizeStyles;
