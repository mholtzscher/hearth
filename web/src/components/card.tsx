import { Slot } from "radix-ui";
import * as React from "react";

import { cn } from "../lib/utils.ts";

/**
 * The card surface (DESIGN.md, Shapes): 12px corners, a 1px neutral border, and
 * the card background. Development pages use the compact size. View data cards
 * add their title, status, and footer through DataCard.
 */
const Card = ({
  className,
  size = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"div"> & {
  /** `compact` is the denser Development card: 12px padding and 14px text. */
  size?: "default" | "compact";
  /** Render the child element, such as an `article` or `section`, as the card. */
  asChild?: boolean;
}) => {
  const Comp = asChild ? Slot.Root : "div";
  return (
    <Comp
      data-slot="card"
      data-size={size}
      className={cn(
        "rounded-card border-border bg-card text-card-foreground min-w-0 border",
        size === "compact" ? "text-label p-3 leading-normal" : "p-[18px]",
        className
      )}
      {...props}
    />
  );
};

export { Card };
