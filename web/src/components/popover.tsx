import { Popover as PopoverPrimitive } from "radix-ui";
import * as React from "react";

import { cn } from "../lib/utils.ts";

const Popover = ({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Root>) => (
  <PopoverPrimitive.Root data-slot="popover" {...props} />
);

const PopoverTrigger = ({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Trigger>) => (
  <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />
);

const PopoverContent = ({
  className,
  align = "center",
  sideOffset = 4,
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Content>) => (
  <PopoverPrimitive.Portal>
    <PopoverPrimitive.Content
      data-slot="popover-content"
      align={align}
      sideOffset={sideOffset}
      className={cn(
        "rounded-card bg-popover text-label text-popover-foreground z-[81] flex max-h-[min(720px,var(--radix-popover-content-available-height,calc(100dvh-24px)))] w-[min(440px,calc(100vw-24px))] flex-col gap-2.5 overflow-auto overscroll-contain p-[22px] [overflow-wrap:anywhere] shadow-[0_18px_60px_#0008] outline-none",
        className
      )}
      {...props}
    />
  </PopoverPrimitive.Portal>
);

const PopoverAnchor = ({
  ...props
}: React.ComponentProps<typeof PopoverPrimitive.Anchor>) => (
  <PopoverPrimitive.Anchor data-slot="popover-anchor" {...props} />
);

const PopoverHeader = ({
  className,
  ...props
}: React.ComponentProps<"div">) => (
  <div
    data-slot="popover-header"
    className={cn("text-label flex flex-col gap-0.5", className)}
    {...props}
  />
);

const PopoverTitle = ({ className, ...props }: React.ComponentProps<"h2">) => (
  <div
    data-slot="popover-title"
    className={cn("font-medium", className)}
    {...props}
  />
);

const PopoverDescription = ({
  className,
  ...props
}: React.ComponentProps<"p">) => (
  <p
    data-slot="popover-description"
    className={cn("text-muted-foreground", className)}
    {...props}
  />
);

export {
  Popover,
  PopoverAnchor,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
};
