import { Dialog as DialogPrimitive } from "radix-ui";
import * as React from "react";

import { cn } from "../lib/utils.ts";

const Dialog = (props: React.ComponentProps<typeof DialogPrimitive.Root>) => (
  <DialogPrimitive.Root data-slot="dialog" {...props} />
);

const DialogTrigger = (
  props: React.ComponentProps<typeof DialogPrimitive.Trigger>
) => <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />;

type DialogContentProps = React.ComponentProps<
  typeof DialogPrimitive.Content
> & {
  /** Bottom becomes a phone sheet at 720px and below; otherwise the dialog is centered. */
  placement?: "center" | "bottom";
};

const DialogContent = ({
  className,
  placement = "center",
  forceMount,
  ...props
}: DialogContentProps) => (
  <DialogPrimitive.Portal forceMount={forceMount}>
    <DialogPrimitive.Overlay
      forceMount={forceMount}
      data-slot="dialog-overlay"
      className={cn(
        "bg-background/[.667] fixed inset-0 z-[100]",
        placement === "bottom" && "max-tablet:bg-background/60"
      )}
    />
    <DialogPrimitive.Content
      data-slot="dialog-content"
      forceMount={forceMount}
      className={cn(
        "border-gray-4 bg-gray-2 text-label text-foreground fixed top-1/2 left-1/2 z-[101] w-[min(460px,calc(100vw-40px))] -translate-x-1/2 -translate-y-1/2 rounded-xl border p-6 leading-normal shadow-[0_18px_60px_#0008] outline-none",
        placement === "center"
          ? "short:max-h-[calc(100dvh-40px)] short:overflow-y-auto"
          : "tablet:short:max-h-[calc(100dvh-40px)] tablet:short:overflow-y-auto",
        placement === "bottom" &&
          "max-tablet:inset-x-0 max-tablet:top-auto max-tablet:bottom-0 max-tablet:max-h-[85dvh] max-tablet:w-full max-tablet:translate-x-0 max-tablet:translate-y-0 max-tablet:overflow-auto max-tablet:overscroll-contain max-tablet:rounded-b-none max-tablet:border-0 max-tablet:px-[22px] max-tablet:pt-[22px] max-tablet:pb-[max(24px,env(safe-area-inset-bottom))] max-tablet:before:mx-auto max-tablet:before:mt-[-10px] max-tablet:before:mb-[18px] max-tablet:before:block max-tablet:before:h-1 max-tablet:before:w-8 max-tablet:before:rounded-[2px] max-tablet:before:bg-gray-6",
        className
      )}
      {...props}
    />
  </DialogPrimitive.Portal>
);

const DialogTitle = ({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) => (
  <DialogPrimitive.Title
    data-slot="dialog-title"
    className={cn("text-compact-reading", className)}
    {...props}
  />
);

const DialogDescription = ({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) => (
  <DialogPrimitive.Description
    data-slot="dialog-description"
    className={cn("text-muted-foreground mt-[14px] mb-5", className)}
    {...props}
  />
);

const DialogFooter = ({ className, ...props }: React.ComponentProps<"div">) => (
  <div
    data-slot="dialog-footer"
    className={cn("flex flex-wrap justify-end gap-2", className)}
    {...props}
  />
);

const DialogClose = (
  props: React.ComponentProps<typeof DialogPrimitive.Close>
) => <DialogPrimitive.Close data-slot="dialog-close" {...props} />;

export {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
};
export type { DialogContentProps };
