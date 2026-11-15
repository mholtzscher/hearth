import * as React from "react"
import { cn } from "../../lib/utils.ts"
import { Switch as SwitchPrimitive } from "radix-ui"

type SwitchProps = React.ComponentProps<typeof SwitchPrimitive.Root> & {
  /** The touch target is 44px high; the default target is 32px. */
  size?: "touch" | "default"
}

function Switch({
  className,
  size = "default",
  ...props
}: SwitchProps) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        "group/switch inline-flex w-11 shrink-0 cursor-pointer items-center justify-center rounded-md border-0 bg-transparent p-0 outline-none focus-visible:outline-2 focus-visible:outline-solid focus-visible:outline-ring focus-visible:outline-offset-[3px] data-[size=default]:h-8 data-[size=touch]:h-11 disabled:cursor-not-allowed disabled:opacity-50",
        className
      )}
      {...props}
    >
      <span data-slot="switch-track" className="pointer-events-none relative h-6 w-11 rounded-card border border-gray-7 bg-gray-3 transition-[background-color,border-color] duration-[160ms] ease-out-expo motion-reduce:transition-none group-data-checked/switch:border-violet group-data-checked/switch:bg-violet group-aria-invalid/switch:border-destructive">
        <SwitchPrimitive.Thumb
          data-slot="switch-thumb"
          className="absolute top-[2px] left-[2px] size-[18px] rounded-full bg-gray-8 transition-[translate,background-color] duration-[160ms] ease-out-expo motion-reduce:transition-none group-data-checked/switch:translate-x-5 group-data-checked/switch:bg-gray-10"
        />
      </span>
    </SwitchPrimitive.Root>
  )
}

export { Switch }
export type { SwitchProps }
