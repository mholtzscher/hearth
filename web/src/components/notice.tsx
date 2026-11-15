import type { ReactNode } from "react";

import { cn } from "../lib/utils.ts";

interface NoticeProps {
  /** Choose alert for an error or status for non-urgent feedback. */
  role: "alert" | "status";
  className?: string;
  children: ReactNode;
}

const noticeClasses =
  "border-destructive/45 bg-destructive/8 text-caption text-foreground my-3 rounded-md border p-3 leading-normal";

type StatusNoticeProps = Omit<NoticeProps, "role">;

const StatusNotice = ({ className, children }: StatusNoticeProps) => (
  <output data-slot="notice" className={cn(noticeClasses, "block", className)}>
    {children}
  </output>
);

const Notice = ({ role, className, children }: NoticeProps) => {
  if (role === "status") {
    return <StatusNotice className={className}>{children}</StatusNotice>;
  }
  return (
    <div
      data-slot="notice"
      role={role}
      className={cn(noticeClasses, className)}
    >
      {children}
    </div>
  );
};

export { Notice, type NoticeProps, StatusNotice, type StatusNoticeProps };
