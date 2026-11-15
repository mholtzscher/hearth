import { Switch as SwitchPrimitive } from "radix-ui";
import * as React from "react";

import { cn } from "../lib/utils.ts";

type SwitchProps = React.ComponentProps<typeof SwitchPrimitive.Root> & {
  /** The touch target is 44px high; the default target is 32px. */
  size?: "touch" | "default";
};

const Switch = ({ className, size = "default", ...props }: SwitchProps) => (
  <SwitchPrimitive.Root
    data-slot="switch"
    data-size={size}
    className={cn(
      "group/switch focus-visible:outline-ring inline-flex w-11 shrink-0 cursor-pointer items-center justify-center rounded-md border-0 bg-transparent p-0 outline-none focus-visible:outline-2 focus-visible:outline-offset-[3px] focus-visible:outline-solid disabled:cursor-not-allowed disabled:opacity-50 data-[size=default]:h-8 data-[size=touch]:h-11",
      className
    )}
    {...props}
  >
    <span
      data-slot="switch-track"
      className="rounded-card border-gray-7 bg-gray-3 ease-out-expo group-data-checked/switch:border-violet group-data-checked/switch:bg-violet group-aria-invalid/switch:border-destructive pointer-events-none relative h-6 w-11 border transition-[background-color,border-color] duration-[160ms] motion-reduce:transition-none"
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="bg-gray-8 ease-out-expo group-data-checked/switch:bg-gray-10 absolute top-[2px] left-[2px] size-[18px] rounded-full transition-[translate,background-color] duration-[160ms] group-data-checked/switch:translate-x-5 motion-reduce:transition-none"
      />
    </span>
  </SwitchPrimitive.Root>
);

export { Switch };
export type { SwitchProps };
