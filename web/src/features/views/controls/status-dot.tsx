import { cn } from "../../../lib/utils";

/** A tone accompanies status words; the dot alone never conveys the state. */
export const StatusDot = ({
  tone = "neutral",
}: {
  tone?: "neutral" | "info" | "warn" | "error" | "ok" | "dimmed";
}) => (
  <span
    aria-hidden="true"
    className={cn(
      "size-1.5 shrink-0 self-center rounded-full",
      tone === "neutral" && "bg-gray-7",
      tone === "info" && "bg-info",
      tone === "warn" && "bg-warning",
      tone === "error" && "bg-destructive",
      tone === "ok" && "bg-success",
      tone === "dimmed" && "bg-gray-6"
    )}
  />
);
