/** Native range inputs keep their browser-specific track and thumb geometry. */
export const sliderClasses =
  "[--fill:0%] [--track:var(--gray-4)] [--level:var(--violet)] [--knob:var(--gray-10)] block h-11 w-full m-0 cursor-pointer appearance-none bg-transparent disabled:cursor-not-allowed disabled:[--track:var(--gray-3)] disabled:[--level:var(--gray-5)] disabled:[--knob:var(--gray-6)] focus-visible:outline-none " +
  "[&::-webkit-slider-runnable-track]:h-1 [&::-webkit-slider-runnable-track]:rounded-[2px] [&::-webkit-slider-runnable-track]:[background:linear-gradient(to_right,var(--level)_var(--fill),var(--track)_var(--fill))] " +
  "[&::-moz-range-track]:h-1 [&::-moz-range-track]:rounded-[2px] [&::-moz-range-track]:[background:var(--track)] [&::-moz-range-progress]:h-1 [&::-moz-range-progress]:rounded-[2px] [&::-moz-range-progress]:[background:var(--level)] " +
  "[&::-webkit-slider-thumb]:-mt-[7px] [&::-webkit-slider-thumb]:size-[18px] [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:border-0 [&::-webkit-slider-thumb]:[background:var(--knob)] " +
  "[&::-moz-range-thumb]:size-[18px] [&::-moz-range-thumb]:rounded-full [&::-moz-range-thumb]:border-0 [&::-moz-range-thumb]:[background:var(--knob)] " +
  "focus-visible:[&::-webkit-slider-thumb]:outline-2 focus-visible:[&::-webkit-slider-thumb]:outline-offset-3 focus-visible:[&::-webkit-slider-thumb]:outline-ring focus-visible:[&::-moz-range-thumb]:outline-2 focus-visible:[&::-moz-range-thumb]:outline-offset-3 focus-visible:[&::-moz-range-thumb]:outline-ring";

export const sliderLabelClasses =
  "flex justify-between gap-2 text-label leading-normal text-muted-foreground tabular-nums";
