import { cn } from "../../../lib/utils";

/** A tone accompanies status words; the dot alone never conveys the state. */
export const StatusDot = ({
  tone = "neutral",
  pending = false,
}: {
  tone?: "neutral" | "info" | "warn" | "error" | "ok" | "dimmed";
  /** Breathes only while a Command awaits an outcome. */
  pending?: boolean;
}) => (
  <span
    aria-hidden="true"
    data-slot={pending ? "pending-command-dot" : undefined}
    className={cn(
      "size-1.5 shrink-0 self-center rounded-full",
      tone === "neutral" && "bg-gray-7",
      tone === "info" && "bg-info",
      tone === "warn" && "bg-warning",
      tone === "error" && "bg-destructive",
      tone === "ok" && "bg-success",
      tone === "dimmed" && "bg-gray-6",
      pending && "motion-safe:animate-pending"
    )}
  />
);
