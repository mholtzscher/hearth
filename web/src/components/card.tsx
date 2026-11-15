import * as React from "react"
import { Slot } from "radix-ui"
import { cn } from "../lib/utils.ts"

/**
 * The card surface (DESIGN.md, Shapes): 12px corners, a 1px neutral border, and
 * the card background. Development pages use the compact size. View data cards
 * add their title, status, and footer through DataCard.
 */
function Card({
  className,
  size = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"div"> & {
  /** `compact` is the denser Development card: 12px padding and 14px text. */
  size?: "default" | "compact"
  /** Render the child element, such as an `article` or `section`, as the card. */
  asChild?: boolean
}) {
  const Comp = asChild ? Slot.Root : "div"
  return (
    <Comp
      data-slot="card"
      data-size={size}
      className={cn(
        "min-w-0 rounded-card border border-border bg-card text-card-foreground",
        size === "compact" ? "p-3 text-label leading-normal" : "p-[18px]",
        className
      )}
      {...props}
    />
  )
}

export { Card }
