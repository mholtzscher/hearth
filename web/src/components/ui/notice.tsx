import type { ReactNode } from "react";
import { cn } from "../../lib/utils.ts";

type NoticeProps = {
  /** Choose alert for an error or status for non-urgent feedback. */
  role: "alert" | "status";
  className?: string;
  children: ReactNode;
};

function Notice({ role, className, children }: NoticeProps) {
  return (
    <div
      data-slot="notice"
      role={role}
      className={cn("my-3 rounded-md border border-destructive/45 bg-destructive/8 p-3 text-caption leading-normal text-foreground", className)}
    >
      {children}
    </div>
  );
}

export { Notice, type NoticeProps };
